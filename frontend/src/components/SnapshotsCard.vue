<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { Plus, Delete, ArrowUp, ArrowDown, View, RefreshLeft } from "@element-plus/icons-vue";
import { api } from "@/lib/api";
import { onAdmin, confirmAction, run, toast, go } from "@/lib/store";
import { formatBytes, formatTime } from "@/lib/utils";
import type { DistFile, Preset, Snapshot, SnapshotOp } from "@/lib/types";
import CodeEditor from "@/components/CodeEditor.vue";

const KINDS: Record<string, string> = {
  command: "命令",
  distribute: "分发",
  wol: "唤醒",
  schedule: "电源计划",
  broadcast: "广播",
};

const ADD_TABS = [
  { label: "执行命令", value: "command" },
  { label: "文件分发", value: "distribute" },
  { label: "网络管控", value: "network" },
  { label: "批量唤醒", value: "wol" },
  { label: "电源计划", value: "schedule" },
] as const;
type AddTab = (typeof ADD_TABS)[number]["value"];

const active = ref<Snapshot | null>(null);
const history = ref<Snapshot[]>([]);
const ops = ref<SnapshotOp[]>([]);
const scope = ref("");
const isRelay = ref(false);
const name = ref("");
const showHistory = ref(false);
const previewSnap = ref<Snapshot | null>(null);
const previewOps = ref<SnapshotOp[]>([]);

// Add / Insert dialog state
const addVisible = ref(false);
const submitting = ref(false);
const presets = ref<Preset[]>([]);
const distFiles = ref<DistFile[]>([]);
const addForm = ref({
  position: "end" as string, // "end" | "start" | String(op.id)
  tab: "command" as AddTab,
  command: "",
  files: [] as string[],
  saveDir: "/home/cwxu/桌面",
  postCmd: "",
  netAction: "network_apply" as "network_apply" | "network_remove",
  schedAction: "shutdown" as "wol" | "reboot" | "shutdown",
  schedWhen: null as Date | null,
  schedNote: "",
  summary: "",
  skipOnline: false,
});

const isRemoteBacked = computed(() => !!(active.value?.remote_id && active.value.remote_id > 0));
const hasRemoteOps = computed(() => isRemoteBacked.value || ops.value.some((o) => o.source === "remote"));
const remoteCount = computed(() => ops.value.filter((o) => o.source === "remote").length);
const localCount = computed(() => ops.value.filter((o) => o.source !== "remote").length);
const inactiveHistory = computed(() => history.value.filter((s) => !s.active && s.id !== active.value?.id));

async function load() {
  const data = await api<{ active: Snapshot | null; history?: Snapshot[]; scope: string; relay?: boolean }>("/api/snapshots");
  active.value = data.active;
  history.value = data.history || [];
  scope.value = data.scope;
  isRelay.value = !!data.relay;
  ops.value = data.active ? (await api<SnapshotOp[]>(`/api/snapshots/${data.active.id}/ops`)) || [] : [];
}

async function start() {
  if (!name.value.trim()) return;
  const msg =
    scope.value === "cloud"
      ? "启用后，全平台操作将自动进入队列并同步至所有机房；后接入的机房与晚开机的设备会自动按顺序补执行。继续？"
      : "启用后，对本机房“全部设备”的操作会进入队列，并自动按顺序补发给之后才上线的设备。继续？";
  if (!(await confirmAction(msg))) return;
  if ((await run(() => api("/api/snapshots", { json: { name: name.value.trim() } }), "快照已启用")) !== undefined) {
    name.value = "";
    load();
  }
}

async function end() {
  if (!active.value) return;
  const msg =
    scope.value === "cloud"
      ? "结束云端快照后，各机房同步快照也将随之结束，新上线设备不再补执行这些操作。继续？"
      : "结束后，新上线的设备不再补执行这些操作。继续？";
  if (!(await confirmAction(msg))) return;
  await run(() => api(`/api/snapshots/${active.value!.id}/end`, { method: "POST" }), "快照已结束");
  load();
}

async function removeSnapshot(snap: Snapshot) {
  const warn = snap.active
    ? `确定删除正在记录中的快照「${snap.name}」及其全部操作队列？`
    : `确定删除历史快照「${snap.name}」？`;
  if (!(await confirmAction(warn, { danger: true, confirmText: "删除" }))) return;
  if ((await run(() => api(`/api/snapshots/${snap.id}`, { method: "DELETE" }), "快照已删除")) !== undefined) {
    if (previewSnap.value?.id === snap.id) previewSnap.value = null;
    load();
  }
}

async function openHistoryPreview(snap: Snapshot) {
  const list = await run(() => api<SnapshotOp[]>(`/api/snapshots/${snap.id}/ops`));
  if (list !== undefined) {
    previewSnap.value = snap;
    previewOps.value = list || [];
  }
}

function canMove(row: SnapshotOp, index: number, delta: -1 | 1) {
  const next = index + delta;
  if (next < 0 || next >= ops.value.length) return false;
  if (isRemoteBacked.value && row.source === "remote") return false;
  return true;
}

async function moveOp(index: number, delta: -1 | 1) {
  if (!active.value) return;
  const row = ops.value[index];
  if (!row || !canMove(row, index, delta)) return;
  const copy = ops.value.slice();
  const [moved] = copy.splice(index, 1);
  copy.splice(index + delta, 0, moved);
  const ok = await run(() =>
    api(`/api/snapshots/${active.value!.id}/ops/reorder`, {
      method: "PUT",
      json: { op_ids: copy.map((o) => o.id) },
    }),
  );
  if (ok !== undefined) load();
}

async function removeOp(op: SnapshotOp) {
  if (!active.value) return;
  const isRemote = op.source === "remote";
  const prompt = isRemote
    ? "该步骤同步自云端快照，跳过后仅在本机房不执行（不影响其他机房，随时可恢复）。继续？"
    : scope.value === "cloud"
      ? "从云端快照删除后，所有机房的对应远端步骤也会同步移除。继续？"
      : "从队列删除后，之后新上线的设备不再执行该步骤。继续？";
  if (!(await confirmAction(prompt, { danger: !isRemote, confirmText: isRemote ? "本机房跳过" : "删除" }))) return;
  if ((await run(() => api(`/api/snapshots/${active.value!.id}/ops/${op.id}`, { method: "DELETE" }), isRemote ? "已在本机房跳过该远端步骤" : "步骤已删除")) !== undefined) {
    load();
  }
}

async function restoreRemote() {
  if (!active.value) return;
  if ((await run(() => api(`/api/snapshots/${active.value!.id}/ops/restore-remote`, { method: "POST" }), "已恢复跳过的远端步骤")) !== undefined) {
    load();
  }
}

function openAddDialog(afterID: number | null) {
  if (!active.value) return;
  addForm.value = {
    position: afterID === null ? "end" : afterID === 0 ? "start" : String(afterID),
    tab: "command",
    command: "",
    files: [],
    saveDir: localStorage.getItem("icpc-dist-save-dir") || "/home/cwxu/桌面",
    postCmd: "",
    netAction: "network_apply",
    schedAction: "shutdown",
    schedWhen: null,
    schedNote: "",
    summary: "",
    skipOnline: false,
  };
  addVisible.value = true;
  api<Preset[]>("/api/presets").then((p) => (presets.value = p || [])).catch(() => {});
  api<DistFile[]>("/api/distribution/files").then((f) => (distFiles.value = f || [])).catch(() => {});
}

async function submitCustomOp() {
  if (!active.value) return;
  const f = addForm.value;
  let kind: string = f.tab;
  if (f.tab === "command" && !f.command.trim()) return toast("请输入要执行的命令", "warning");
  if (f.tab === "distribute") {
    if (!f.files.length) return toast("请选择要分发的文件", "warning");
    if (!f.saveDir.trim().startsWith("/")) return toast("保存目录必须是绝对路径", "warning");
  }
  if (f.tab === "network") {
    kind = f.netAction;
  }
  if (f.tab === "schedule") {
    if (!f.schedWhen) return toast("请选择执行时间", "warning");
    if (f.schedWhen.getTime() <= Date.now()) return toast("执行时间必须晚于当前时间", "warning");
  }

  let afterOpID: number | null = null;
  if (f.position === "start") afterOpID = 0;
  else if (f.position !== "end") afterOpID = Number(f.position);

  submitting.value = true;
  const res = await run(
    () =>
      api<SnapshotOp>(`/api/snapshots/${active.value!.id}/ops`, {
        json: {
          kind,
          command: f.command,
          files: f.files,
          save_dir: f.saveDir.trim(),
          post_cmd: f.postCmd.trim(),
          action: f.schedAction,
          run_at: f.schedWhen ? f.schedWhen.toISOString() : "",
          note: f.schedNote.trim(),
          summary: f.summary.trim(),
          after_op_id: afterOpID,
          skip_online: f.skipOnline,
        },
      }),
    isRemoteBacked.value ? "已插入本机房操作" : "已添加快照操作",
  );
  submitting.value = false;
  if (res !== undefined) {
    addVisible.value = false;
    load();
  }
}

function sourceLabel(op: SnapshotOp) {
  return op.source === "remote" ? "远端" : "本机房";
}

let off: (() => void) | undefined;
onMounted(() => {
  load().catch(() => {});
  off = onAdmin((e) => e.event === "snapshot_updated" && load().catch(() => {}));
});
onBeforeUnmount(() => off?.());
</script>

<template>
  <el-card shadow="never">
    <template #header>
      <div class="card-title">
        操作快照
        <el-tooltip
          :content="
            scope === 'cloud'
              ? '云端主快照会自动同步至所有机房；各机房可在此远端序列基础上插入机房专属操作。'
              : isRemoteBacked
                ? '本机房正在跟随云端快照；本机房的全员操作或自定义添加将作为本地插入项按顺序执行。'
                : '开启后，对全体设备的命令、分发等操作会进入队列；中途才开机的设备上线后会自动按顺序补执行。'
          "
          placement="top"
        >
          <el-tag v-if="scope === 'cloud'" size="small" type="primary" effect="plain">云端主队列</el-tag>
          <el-tag v-else-if="isRemoteBacked" size="small" type="primary" effect="plain">同步自云端</el-tag>
          <el-tag v-else size="small" type="info">{{ isRelay ? "机房队列" : "本机房队列" }}</el-tag>
        </el-tooltip>

        <div class="actions">
          <template v-if="active">
            <el-tag type="success" effect="light">{{ isRemoteBacked ? `跟随云端：${active.name}` : `记录中：${active.name}` }}</el-tag>
            <el-button size="small" type="primary" :icon="Plus" @click="openAddDialog(null)">
              {{ isRemoteBacked ? "插入操作" : "添加操作" }}
            </el-button>
            <el-button v-if="active.skipped_remote_count" size="small" :icon="RefreshLeft" @click="restoreRemote">
              恢复远端跳过项 ({{ active.skipped_remote_count }})
            </el-button>
            <template v-if="!isRemoteBacked">
              <el-button size="small" type="warning" plain @click="end">结束</el-button>
              <el-button size="small" type="danger" text :icon="Delete" @click="removeSnapshot(active)">删除</el-button>
            </template>
          </template>
          <el-button v-if="inactiveHistory.length" size="small" text @click="showHistory = !showHistory">
            历史快照 ({{ inactiveHistory.length }})
          </el-button>
        </div>
      </div>
    </template>

    <div v-if="!active" class="row">
      <el-input v-model="name" placeholder="快照名称，例如：2026 校赛" style="max-width: 280px" @keyup.enter="start" />
      <el-button type="primary" :disabled="!name.trim()" @click="start">开始记录</el-button>
      <span class="muted small">
        {{
          scope === "cloud"
            ? "开启后自动下发至各机房作为远端基准，各机房可在任意步骤前后插入本地操作"
            : isRelay
              ? "云端开启快照后会自动同步至此；也可在此单独开启本机房快照"
              : "用于让晚开机的设备按顺序自动补上之前的全员操作"
        }}
      </span>
    </div>

    <template v-else>
      <div class="row meta-bar">
        <span class="muted small">
          开始于 {{ formatTime(active.created_at) }} · 共 {{ ops.length }} 步
          <template v-if="hasRemoteOps">（远端 {{ remoteCount }} · 本机房插入 {{ localCount }}）</template>
        </span>
        <el-button v-if="ops.length" size="small" link type="primary" @click="openAddDialog(0)">在最前插入</el-button>
        <span class="spacer" />
        <span v-if="isRemoteBacked" class="muted small">本机房的全员操作会自动追加，也可在任意远端步骤后插入并调整位置</span>
      </div>

      <el-table :data="ops" row-key="id" size="small" max-height="320" empty-text="队列为空，可执行全员操作或点击右上角“添加/插入操作”">
        <el-table-column label="#" width="52">
          <template #default="{ $index }"><span class="mono muted">{{ $index + 1 }}</span></template>
        </el-table-column>
        <el-table-column v-if="hasRemoteOps" label="来源" width="84">
          <template #default="{ row }">
            <el-tag size="small" :type="row.source === 'remote' ? 'info' : 'primary'" :effect="row.source === 'remote' ? 'plain' : 'light'">
              {{ sourceLabel(row) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="类型" width="90">
          <template #default="{ row }">
            <el-tag size="small" type="info" effect="plain">{{ KINDS[row.kind] || row.kind }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作内容" min-width="220" show-overflow-tooltip>
          <template #default="{ row }">
            <span>{{ row.summary }}</span>
            <el-tag v-if="row.source === 'remote' && row.room_ready === false" size="small" type="warning" style="margin-left: 6px">同步中</el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="scope === 'cloud' ? '已投递机房' : '已送达设备'" width="100">
          <template #default="{ row }">
            <span class="mono">{{ row.delivered }}</span>
            <span class="muted small"> {{ scope === "cloud" ? "个" : "台" }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="186" align="right">
          <template #default="{ row, $index }">
            <div class="op-actions">
              <el-tooltip :content="isRemoteBacked && row.source === 'remote' ? '远端步骤顺序由云端统一维护；请移动本机房插入的步骤' : '上移'" placement="top">
                <span>
                  <el-button size="small" text circle :icon="ArrowUp" :disabled="!canMove(row, $index, -1)" @click="moveOp($index, -1)" />
                </span>
              </el-tooltip>
              <el-tooltip :content="isRemoteBacked && row.source === 'remote' ? '远端步骤顺序由云端统一维护；请移动本机房插入的步骤' : '下移'" placement="top">
                <span>
                  <el-button size="small" text circle :icon="ArrowDown" :disabled="!canMove(row, $index, 1)" @click="moveOp($index, 1)" />
                </span>
              </el-tooltip>
              <el-button link type="primary" size="small" @click="openAddDialog(row.id)">插入</el-button>
              <el-button link type="danger" size="small" @click="removeOp(row)">
                {{ row.source === "remote" ? "跳过" : "删除" }}
              </el-button>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </template>

    <div v-if="showHistory && inactiveHistory.length" class="history-box">
      <div class="muted small" style="margin-bottom: 6px; font-weight: 600">历史快照</div>
      <el-table :data="inactiveHistory" size="small" max-height="200">
        <el-table-column label="名称" prop="name" min-width="160" />
        <el-table-column label="创建时间" width="170">
          <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="步骤数" width="80">
          <template #default="{ row }">{{ row.op_count }} 步</template>
        </el-table-column>
        <el-table-column width="120" align="right">
          <template #default="{ row }">
            <el-button link type="primary" size="small" :icon="View" @click="openHistoryPreview(row)">查看</el-button>
            <el-button link type="danger" size="small" @click="removeSnapshot(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <!-- Custom Add / Insert Dialog -->
    <el-dialog
      v-model="addVisible"
      :title="isRemoteBacked ? '基于远端快照插入本机房操作' : '自定义添加快照操作'"
      width="min(640px, 94vw)"
      append-to-body
    >
      <el-form label-position="top">
        <el-form-item label="插入位置">
          <el-select v-model="addForm.position" style="width: 100%">
            <el-option value="end" label="追加到队列末尾（默认）" />
            <el-option v-if="ops.length" value="start" label="插入到最前（第 1 步之前）" />
            <el-option
              v-for="(op, idx) in ops"
              :key="op.id"
              :value="String(op.id)"
              :label="`在 #${idx + 1} ${hasRemoteOps ? '[' + sourceLabel(op) + '] ' : ''}${op.summary} 之后`"
            />
          </el-select>
        </el-form-item>

        <el-form-item label="操作类型">
          <el-segmented v-model="addForm.tab" :options="ADD_TABS as unknown as { label: string; value: string }[]" />
        </el-form-item>

        <template v-if="addForm.tab === 'command'">
          <div v-if="presets.length" class="preset-row">
            <span class="muted small">快捷预设：</span>
            <el-button v-for="p in presets" :key="p.name" size="small" round @click="addForm.command = p.command">{{ p.name }}</el-button>
          </div>
          <el-form-item label="Shell 命令（root 执行）">
            <CodeEditor v-model="addForm.command" :rows="5" placeholder="例如：apt-get update && echo ready" />
          </el-form-item>
        </template>

        <template v-else-if="addForm.tab === 'distribute'">
          <el-form-item label="选择文件（来自文件库）">
            <el-select v-model="addForm.files" multiple placeholder="选择已上传到文件库的文件" style="width: 100%">
              <el-option v-for="f in distFiles" :key="f.name" :value="f.name" :label="`${f.name} (${formatBytes(f.size)})`" />
            </el-select>
            <div v-if="!distFiles.length" class="muted small" style="margin-top: 4px">
              当前文件库为空，请先前往 <el-button link type="primary" size="small" @click="(addVisible = false), go('distribute')">文件分发</el-button> 上传文件。
            </div>
          </el-form-item>
          <el-form-item label="选手机保存目录">
            <el-input v-model="addForm.saveDir" class="mono" placeholder="/home/cwxu/桌面" />
          </el-form-item>
          <el-form-item label="文件到达后执行命令（可选）">
            <el-input v-model="addForm.postCmd" class="mono" placeholder="例如：chown -R cwxu:cwxu /home/cwxu/桌面" />
          </el-form-item>
        </template>

        <template v-else-if="addForm.tab === 'network'">
          <el-form-item label="网络管控动作">
            <el-radio-group v-model="addForm.netAction">
              <el-radio-button value="network_apply">启用当前白名单规则</el-radio-button>
              <el-radio-button value="network_remove">解除网络限制</el-radio-button>
            </el-radio-group>
          </el-form-item>
        </template>

        <template v-else-if="addForm.tab === 'wol'">
          <el-alert type="info" :closable="false" show-icon title="将通过局域网发送 Wake-on-LAN 魔术包唤醒已登记 MAC 地址的选手机。" style="margin-bottom: 14px" />
        </template>

        <template v-else-if="addForm.tab === 'schedule'">
          <div class="row" style="margin-bottom: 14px">
            <el-radio-group v-model="addForm.schedAction">
              <el-radio-button value="wol">唤醒</el-radio-button>
              <el-radio-button value="reboot">重启</el-radio-button>
              <el-radio-button value="shutdown">关机</el-radio-button>
            </el-radio-group>
            <el-date-picker
              v-model="addForm.schedWhen"
              type="datetime"
              placeholder="选择执行时间"
              format="YYYY-MM-DD HH:mm"
              style="flex: 1; min-width: 200px"
            />
          </div>
          <el-form-item label="计划备注（可选）">
            <el-input v-model="addForm.schedNote" placeholder="例如：比赛结束后统一关机" />
          </el-form-item>
        </template>

        <el-form-item label="自定义说明摘要（可选）">
          <el-input v-model="addForm.summary" placeholder="留空则根据操作内容自动生成" maxlength="100" />
        </el-form-item>

        <el-form-item label="生效范围">
          <el-radio-group v-model="addForm.skipOnline">
            <el-radio :value="false">立即对当前在线{{ scope === "cloud" ? "机房与设备" : "设备" }}执行，并记录给后续上线目标</el-radio>
            <el-radio :value="true">仅对后续新上线设备生效（跳过当前在线）</el-radio>
          </el-radio-group>
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="addVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submitCustomOp">
          {{ isRemoteBacked ? "插入到机房队列" : "添加到队列" }}
        </el-button>
      </template>
    </el-dialog>

    <!-- Historical Snapshot Preview Dialog -->
    <el-dialog :model-value="!!previewSnap" :title="`历史快照 · ${previewSnap?.name || ''}`" width="min(680px, 94vw)" append-to-body @update:model-value="(v: boolean) => !v && (previewSnap = null)">
      <el-table :data="previewOps" size="small" max-height="360" empty-text="该快照没有记录步骤">
        <el-table-column label="#" width="52"><template #default="{ $index }"><span class="mono muted">{{ $index + 1 }}</span></template></el-table-column>
        <el-table-column label="来源" width="84">
          <template #default="{ row }">
            <el-tag size="small" :type="row.source === 'remote' ? 'info' : 'primary'" effect="plain">{{ sourceLabel(row) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="类型" width="90"><template #default="{ row }">{{ KINDS[row.kind] || row.kind }}</template></el-table-column>
        <el-table-column label="内容" prop="summary" min-width="200" show-overflow-tooltip />
        <el-table-column label="累计送达" prop="delivered" width="90" />
      </el-table>
      <template #footer>
        <el-button @click="previewSnap = null">关闭</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>

<style scoped>
.meta-bar { margin-bottom: 8px; }
.op-actions { display: inline-flex; align-items: center; justify-content: flex-end; gap: 2px; }
.op-actions .el-button + .el-button { margin-left: 2px; }
.history-box { margin-top: 14px; padding-top: 12px; border-top: 1px solid var(--app-border); }
.preset-row { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; margin-bottom: 10px; }
.preset-row .el-button + .el-button { margin-left: 0; }
</style>
