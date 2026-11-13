<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { UploadFilled, Delete, VideoPause, RefreshRight, Back, CaretRight, Search } from "@element-plus/icons-vue";
import type { UploadFile } from "element-plus";
import { api, apiPath } from "@/lib/api";
import { store, isCloudAll, onAdmin, run, toast, confirmAction, takePendingTargets, queueFleet, onlineDevices } from "@/lib/store";
import { formatBytes, matchDevice, statusLabel, statusType } from "@/lib/utils";
import type { DistFile, DistributeTask } from "@/lib/types";
import TargetPicker from "@/components/TargetPicker.vue";
import FleetTable from "@/components/FleetTable.vue";
import JobsTable from "@/components/JobsTable.vue";
import DistributionProgress from "@/components/DistributionProgress.vue";

const ISSUES = new Set(["failed", "stalled", "cancelled"]);

const DEFAULT_SAVE_DIR = "/home/cwxu/桌面";
const files = ref<DistFile[]>([]);
const pickedFiles = ref<string[]>([]);
const task = ref<DistributeTask | null>(null);
const saveDir = ref(localStorage.getItem("icpc-dist-save-dir") || DEFAULT_SAVE_DIR);
const postCmd = ref("");
const serverIP = ref("");
const mode = ref<"online" | "picked">("online");
const picked = ref<number[]>([]);
const progressFilter = ref<"all" | "issues" | "done">("all");
const progressQuery = ref("");
const starting = ref(false);
const jobs = ref<{ load: () => void } | null>(null);

watch(saveDir, (v) => localStorage.setItem("icpc-dist-save-dir", v));

const active = computed(() => !!task.value && ["running", "completed", "stopped", "failed"].includes(task.value.status) && !!task.value.files?.length);
const progresses = computed(() =>
  Object.values(task.value?.progresses || {}).sort((a, b) => Number(ISSUES.has(b.status)) - Number(ISSUES.has(a.status)) || a.device_id - b.device_id),
);
const shownProgress = computed(() =>
  progresses.value.filter((p) => {
    const byStatus = progressFilter.value === "all" || (progressFilter.value === "issues" ? ISSUES.has(p.status) : p.status === "completed");
    if (!byStatus) return false;
    const q = progressQuery.value.trim();
    if (!q) return true;
    const d = store.devices.find((x) => x.assigned_id === p.device_id);
    return (d && matchDevice(d, q)) || ("#" + p.device_id).includes(q) || (p.hostname || "").toLowerCase().includes(q.toLowerCase());
  }),
);
const issueCount = computed(() => progresses.value.filter((p) => ISSUES.has(p.status)).length);
const totalSize = computed(() => files.value.filter((f) => pickedFiles.value.includes(f.name)).reduce((s, f) => s + f.size, 0));

async function loadFiles() {
  files.value = (await api<DistFile[]>("/api/distribution/files").catch(() => [])) || [];
  pickedFiles.value = pickedFiles.value.filter((n) => files.value.some((f) => f.name === n));
}
async function loadTask() {
  if (isCloudAll.value) return;
  const next = await api<DistributeTask>("/api/distribution/status").catch(() => null);
  task.value = next;
  if (next?.suggested_ip && !serverIP.value) serverIP.value = next.suggested_ip;
}

function onUploaded(_: unknown, file: UploadFile) {
  toast(`${file.name} 上传完成`, "success");
  loadFiles().then(() => {
    if (!pickedFiles.value.includes(file.name)) pickedFiles.value.push(file.name);
  });
}
function onUploadError(err: Error, file: UploadFile) {
  let message = err.message;
  try {
    message = JSON.parse(message).error || message;
  } catch {
    /* plain text */
  }
  toast(`${file.name} 上传失败：${message}`, "error");
}

async function removeFiles() {
  if (!pickedFiles.value.length) return;
  if (!(await confirmAction(`从文件库删除 ${pickedFiles.value.length} 个文件？已发到选手机上的副本不受影响。`, { danger: true, confirmText: "删除" }))) return;
  await run(() => api("/api/distribution/delete", { json: { filenames: pickedFiles.value } }), "已删除");
  pickedFiles.value = [];
  loadFiles();
}

async function start() {
  if (!pickedFiles.value.length) return toast("请先勾选要分发的文件", "warning");
  if (!saveDir.value.trim().startsWith("/")) return toast("保存目录必须是绝对路径", "warning");
  if (isCloudAll.value) {
    if (!store.fleet.length) return toast("请先勾选设备", "warning");
    if (!(await confirmAction(`向 ${store.fleet.length} 台设备分发 ${pickedFiles.value.length} 个文件？`))) return;
    await run(() => queueFleet(store.fleet, "distribute", { files: pickedFiles.value, save_dir: saveDir.value, post_cmd: postCmd.value }), "已加入队列");
    jobs.value?.load();
    return;
  }
  const ids = mode.value === "picked" ? picked.value : [];
  if (mode.value === "picked" && !ids.length) return toast("请勾选设备，或切换为“全部在线”", "warning");
  if (mode.value === "online" && !onlineDevices.value.length) return toast("当前没有在线设备", "warning");
  starting.value = true;
  await run(() => api("/api/distribution/start", { json: { files: pickedFiles.value, save_dir: saveDir.value.trim(), post_cmd: postCmd.value, server_ip: serverIP.value.trim(), target_ids: ids } }), "分发已开始");
  starting.value = false;
  loadTask();
}

async function stop() {
  if (!(await confirmAction("停止当前分发？未完成的设备会标记为已取消，之后可以补传。", { danger: true, confirmText: "停止" }))) return;
  await run(() => api("/api/distribution/stop", { method: "POST" }));
  loadTask();
}
async function reset() {
  await run(() => api("/api/distribution/reset", { method: "POST" }));
  task.value = null;
  loadTask();
}
async function retry(deviceID: number) {
  await run(() => api("/api/distribution/retry", { json: { device_id: deviceID } }), deviceID ? `已重试 #${deviceID}` : "已开始补传缺失文件");
  loadTask();
}

let timer = 0;
let off: (() => void) | undefined;
onMounted(() => {
  const ids = takePendingTargets();
  if (ids.length) {
    mode.value = "picked";
    picked.value = ids;
  }
  loadFiles();
  loadTask();
  off = onAdmin(({ event }) => {
    if (event.startsWith("distribute_") && !timer) timer = window.setTimeout(() => { timer = 0; loadTask(); }, 500);
  });
});
// Fallback polling while running, in case progress events are sparse.
let poll = 0;
watch(() => task.value?.status, (s) => {
  clearInterval(poll);
  if (s === "running") poll = window.setInterval(loadTask, 2000);
}, { immediate: true });
onBeforeUnmount(() => {
  off?.();
  clearTimeout(timer);
  clearInterval(poll);
});

function nameOf(id: number) {
  const d = store.devices.find((x) => x.assigned_id === id);
  if (!d) return "";
  return [d.student_name || d.hostname, d.student_num].filter(Boolean).join(" ");
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ isCloudAll ? "云端文件分发" : "文件分发" }}</h1>
        <p v-if="isCloudAll">云端保存一份；各机房拉取并校验后，再在机房内 P2P 分发到选手机。</p>
        <p v-else>局域网 P2P 分发，逐文件 SHA-256 校验，失败自动重试，可断点补传。</p>
      </div>
    </div>

    <!-- Running / finished task -->
    <el-card v-if="active && task" shadow="never">
      <template #header>
        <div class="card-title">
          <el-tag :type="statusType(task.status)" effect="dark">{{ statusLabel(task.status) }}</el-tag>
          <span class="muted small">保存到 <span class="mono">{{ task.save_dir }}</span></span>
          <div class="actions">
            <el-button v-if="task.status === 'running'" type="danger" plain :icon="VideoPause" @click="stop">停止</el-button>
            <template v-else>
              <el-button v-if="issueCount" type="primary" :icon="RefreshRight" @click="retry(0)">补传缺失（{{ issueCount }} 台）</el-button>
              <el-button :icon="Back" @click="reset">新的分发</el-button>
            </template>
          </div>
        </div>
      </template>
      <DistributionProgress :task="task" />
      <div class="row" style="margin: 14px 0 8px">
        <el-input v-model="progressQuery" size="small" :prefix-icon="Search" placeholder="搜索编号 / 姓名 / 学号" clearable style="width: 220px" />
        <el-radio-group v-model="progressFilter" size="small">
          <el-radio-button value="all">全部 {{ progresses.length }}</el-radio-button>
          <el-radio-button value="issues">需处理 {{ issueCount }}</el-radio-button>
          <el-radio-button value="done">已完成 {{ progresses.filter((p) => p.status === "completed").length }}</el-radio-button>
        </el-radio-group>
        <span v-if="task.post_cmd" class="muted small">完成后执行：<span class="mono">{{ task.post_cmd }}</span></span>
      </div>
      <el-table :data="shownProgress" size="small" max-height="460" empty-text="等待设备回报">
        <el-table-column label="设备" width="180">
          <template #default="{ row }"><b class="mono">#{{ row.device_id }}</b> <span class="muted small">{{ nameOf(row.device_id) || row.hostname }}</span></template>
        </el-table-column>
        <el-table-column label="进度" min-width="220">
          <template #default="{ row }">
            <el-progress :percentage="Math.min(100, Math.round(row.percentage || 0))" :status="row.status === 'completed' ? 'success' : ISSUES.has(row.status) ? 'exception' : undefined" />
          </template>
        </el-table-column>
        <el-table-column label="速度" width="100"><template #default="{ row }">{{ row.speed_mbps ? row.speed_mbps.toFixed(1) + " MB/s" : "—" }}</template></el-table-column>
        <el-table-column label="状态" min-width="160">
          <template #default="{ row }">
            <el-tag size="small" :type="statusType(row.status)">{{ statusLabel(row.status) }}</el-tag>
            <span v-if="row.error" class="err small"> {{ row.error }}</span>
          </template>
        </el-table-column>
        <el-table-column width="80" align="right">
          <template #default="{ row }"><el-button v-if="ISSUES.has(row.status)" size="small" link type="primary" @click="retry(row.device_id)">重试</el-button></template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- Preparation -->
    <template v-else>
      <el-card shadow="never">
        <template #header>
          <div class="card-title"><span class="step">1</span>选择文件
            <div class="actions">
              <span v-if="pickedFiles.length" class="muted small">已选 {{ pickedFiles.length }} 个，共 {{ formatBytes(totalSize) }}</span>
              <el-button size="small" type="danger" plain :icon="Delete" :disabled="!pickedFiles.length" @click="removeFiles">删除选中</el-button>
            </div>
          </div>
        </template>
        <el-upload
          drag
          multiple
          :action="apiPath('/api/distribution/upload')"
          name="file"
          with-credentials
          :show-file-list="true"
          :on-success="onUploaded"
          :on-error="onUploadError"
          class="upload"
        >
          <el-icon class="el-icon--upload"><UploadFilled /></el-icon>
          <div class="el-upload__text">把文件拖到这里，或<em>点击上传</em></div>
        </el-upload>
        <el-table :data="files" size="small" max-height="300" empty-text="文件库为空，请先上传" row-key="name" row-class-name="clickable-row"
          @row-click="(f: DistFile) => (pickedFiles = pickedFiles.includes(f.name) ? pickedFiles.filter((n) => n !== f.name) : [...pickedFiles, f.name])">
          <el-table-column width="44">
            <template #header>
              <el-checkbox :model-value="files.length > 0 && pickedFiles.length === files.length" :indeterminate="pickedFiles.length > 0 && pickedFiles.length < files.length"
                @change="(v: string | number | boolean) => (pickedFiles = v ? files.map((f) => f.name) : [])" />
            </template>
            <template #default="{ row }"><el-checkbox :model-value="pickedFiles.includes(row.name)" class="passive" /></template>
          </el-table-column>
          <el-table-column label="文件名" prop="name" min-width="240" />
          <el-table-column label="大小" width="110"><template #default="{ row }">{{ formatBytes(row.size) }}</template></el-table-column>
          <el-table-column label="上传时间" prop="mod_time" width="200" />
        </el-table>
      </el-card>

      <FleetTable v-if="isCloudAll" height="320px" />

      <div :class="isCloudAll ? 'stack' : 'workspace'">
        <el-card v-if="!isCloudAll" shadow="never">
          <template #header><div class="card-title"><span class="step">2</span>选择目标</div></template>
          <TargetPicker v-model:mode="mode" v-model:selected="picked" :devices="store.devices" height="300px" />
        </el-card>
        <el-card shadow="never">
          <template #header><div class="card-title"><span class="step">{{ isCloudAll ? 2 : 3 }}</span>分发选项</div></template>
          <el-form label-position="top">
            <el-form-item label="保存目录（选手机上）">
              <el-input v-model="saveDir" class="mono" placeholder="/home/cwxu/桌面" />
              <div class="row quick-dirs">
                <span class="muted small">常用目录：</span>
                <el-button size="small" link type="primary" @click="saveDir = '/home/cwxu/桌面'">/home/cwxu/桌面</el-button>
                <el-button size="small" link type="primary" @click="saveDir = '/home/cwxu/Desktop'">/home/cwxu/Desktop</el-button>
                <el-button size="small" link type="primary" @click="saveDir = '/home/cwxu'">/home/cwxu</el-button>
                <el-button size="small" link type="primary" @click="saveDir = '/tmp'">/tmp</el-button>
              </div>
            </el-form-item>
            <el-form-item v-if="!isCloudAll" label="服务端宣告 IP">
              <el-input v-model="serverIP" placeholder="留空自动探测；多网卡时请填写选手机可访问的地址" />
            </el-form-item>
            <el-form-item label="全部文件到达后执行（可选）">
              <el-input v-model="postCmd" class="mono" placeholder="例如：chown -R cwxu:cwxu /home/cwxu/桌面" />
            </el-form-item>
          </el-form>
          <el-button type="primary" size="large" :icon="CaretRight" :loading="starting" :disabled="!pickedFiles.length" class="start" @click="start">
            {{ isCloudAll ? `向 ${store.fleet.length} 台设备分发（入队）` : `开始分发到 ${mode === "online" ? "全部在线（" + onlineDevices.length + " 台）" : picked.length + " 台设备"}` }}
          </el-button>
        </el-card>
      </div>
      <JobsTable v-if="isCloudAll" ref="jobs" />
    </template>
  </div>
</template>

<style scoped>
.step { display: inline-grid; place-items: center; width: 22px; height: 22px; border-radius: 50%; background: var(--el-color-primary); color: #fff; font-size: 12px; }
.upload { margin-bottom: 12px; }
.upload :deep(.el-upload-dragger) { padding: 18px; }
.start { width: 100%; }
.err { color: var(--el-color-danger); }
.passive { pointer-events: none; }
.quick-dirs { margin-top: 4px; gap: 4px 10px; }
.quick-dirs .el-button + .el-button { margin-left: 0; }
</style>
