<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { CaretRight, RefreshRight, Delete, CopyDocument, Monitor, Search } from "@element-plus/icons-vue";
import { api } from "@/lib/api";
import { store, isCloudAll, onAdmin, run, toast, confirmAction, takePendingTargets, queueFleet, onlineDevices } from "@/lib/store";
import { formatTime, matchDevice, statusLabel, statusType, targetLabel } from "@/lib/utils";
import type { CommandLog, Preset } from "@/lib/types";
import TargetPicker from "@/components/TargetPicker.vue";
import CodeEditor from "@/components/CodeEditor.vue";
import FleetTable from "@/components/FleetTable.vue";
import JobsTable from "@/components/JobsTable.vue";

type Row = { deviceID: number; commandID: number; status: string; output: string; duration: number };

const presets = ref<Preset[]>([]);
const command = ref(localStorage.getItem("icpc-last-cmd") || "hostname && uptime");
const mode = ref<"online" | "picked">("online");
const picked = ref<number[]>([]);
const rows = ref<Record<number, Row>>({});
const lastCommand = ref("");
const known = new Map<number, number>(); // child command id -> device id
const history = ref<CommandLog[]>([]);
const detail = ref<CommandLog | null>(null);
const busy = ref(false);
const resultFilter = ref<"all" | "failed" | "running">("all");
const resultQuery = ref("");
const expanded = ref<number[]>([]);

const list = computed(() => Object.values(rows.value).sort((a, b) => a.deviceID - b.deviceID));
const done = computed(() => list.value.filter((r) => ["completed", "failed", "timeout", "cancelled"].includes(r.status)).length);
const failedIDs = computed(() => list.value.filter((r) => r.status === "failed" || r.status === "timeout").map((r) => r.deviceID));
const runningCount = computed(() => list.value.length - done.value);
const filtered = computed(() =>
  list.value.filter((r) => {
    const byStatus = resultFilter.value === "all" || (resultFilter.value === "failed" ? failedIDs.value.includes(r.deviceID) : !["completed", "failed", "timeout", "cancelled"].includes(r.status));
    if (!byStatus) return false;
    const q = resultQuery.value.trim();
    if (!q) return true;
    const d = store.devices.find((x) => x.assigned_id === r.deviceID);
    return (d && matchDevice(d, q)) || ("#" + r.deviceID).includes(q) || r.output.toLowerCase().includes(q.toLowerCase());
  }),
);
const targetCount = computed(() => (mode.value === "online" ? onlineDevices.value.length : picked.value.length));

function nameOf(id: number) {
  const d = store.devices.find((x) => x.assigned_id === id);
  if (!d) return "";
  return [d.student_name || d.hostname, d.student_num].filter(Boolean).join(" ");
}

async function loadHistory() {
  history.value = (await api<CommandLog[]>("/api/commands?limit=50").catch(() => [])) || [];
}

async function execute(ids?: number[]) {
  const text = command.value.trim();
  if (!text) return toast("请输入命令", "warning");
  if (isCloudAll.value) return executeFleet(text);
  const targets = ids || (mode.value === "online" ? onlineDevices.value.map((d) => d.assigned_id) : picked.value);
  if (!targets.length) return toast(mode.value === "online" ? "当前没有在线设备" : "请先在左侧勾选设备", "warning");
  if (/\b(shutdown|reboot|poweroff|rm\s+-rf|mkfs|dd\s+if=)/.test(text) && !(await confirmAction(`命令包含危险操作，将在 ${targets.length} 台设备上执行：\n${text}`, { danger: true, confirmText: "执行" }))) return;
  busy.value = true;
  try {
    const body = mode.value === "online" && !ids ? { target_type: "broadcast", command: text } : { target_type: "list", target_ids: targets, command: text };
    const created = await api<CommandLog>("/api/commands", { json: body });
    const next: Record<number, Row> = {};
    known.clear();
    (created.children || []).forEach((child) => {
      if (child.target_id == null) return;
      next[child.target_id] = { deviceID: child.target_id, commandID: child.id, status: child.status, output: child.error_output || "", duration: 0 };
      known.set(child.id, child.target_id);
    });
    rows.value = next;
    lastCommand.value = text;
    expanded.value = [];
    resultFilter.value = "all";
    localStorage.setItem("icpc-last-cmd", text);
    toast(`已派发到 ${created.children?.length || targets.length} 台`, "success");
    loadHistory();
  } catch (err) {
    toast(err instanceof Error ? err.message : "执行失败", "error");
  } finally {
    busy.value = false;
  }
}

async function executeFleet(text: string) {
  if (!store.fleet.length) return toast("请先在上方勾选设备", "warning");
  if (!(await confirmAction(`向 ${store.fleet.length} 台设备执行？每个机房只投递一次。\n${text}`))) return;
  await run(() => queueFleet(store.fleet, "command", { command: text }), "已加入队列");
  jobs.value?.load();
}

async function cancel(row: Row) {
  await run(() => api(`/api/commands/${row.commandID}/cancel`, { method: "POST" }), "已发送终止");
}

async function openHistory(row: CommandLog) {
  detail.value = await run(() => api<CommandLog>("/api/commands/" + row.id)) || null;
}

async function clearHistory() {
  if (!(await confirmAction("清空全部命令历史？", { danger: true }))) return;
  await run(() => api("/api/commands/clear", { method: "POST" }), "已清空");
  loadHistory();
}

function rerunFrom(c: CommandLog) {
  command.value = c.command;
  detail.value = null;
  window.scrollTo({ top: 0, behavior: "smooth" });
}

function copy(text: string) {
  navigator.clipboard.writeText(text).then(() => toast("已复制", "success")).catch(() => toast("复制失败", "error"));
}

const jobs = ref<{ load: () => void } | null>(null);
const cloudHistory = computed(() =>
  store.rooms
    .flatMap((room) => (room.recent_commands || []).map((c) => ({ ...c, room: room.name })))
    .sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at))
    .slice(0, 40),
);

let off: (() => void) | undefined;
onMounted(async () => {
  const ids = takePendingTargets();
  if (ids.length) {
    mode.value = "picked";
    picked.value = ids;
  }
  presets.value = (await api<Preset[]>("/api/presets").catch(() => [])) || [];
  if (isCloudAll.value) return;
  loadHistory();
  if (store.pendingCommandID != null) {
    const id = store.pendingCommandID;
    store.pendingCommandID = null;
    openHistory({ id } as CommandLog);
  }
  off = onAdmin((event) => {
    const data = event.data || {};
    const commandID = Number(data.command_id || 0);
    const deviceID = known.get(commandID);
    if (deviceID == null) return;
    const row = rows.value[deviceID] || { deviceID, commandID, status: "running", output: "", duration: 0 };
    if (event.event === "command_output") {
      rows.value[deviceID] = { ...row, output: row.output + String(data.line ?? "") + "\n", status: row.status === "dispatched" || row.status === "pending" ? "running" : row.status };
    } else if (event.event === "command_result") {
      const extra = data.error_output ? String(data.error_output) + "\n" : "";
      rows.value[deviceID] = { ...row, status: String(data.status || "completed"), output: row.output + extra, duration: Number(data.duration_ms || 0) };
      if (!Object.values(rows.value).some((r) => !["completed", "failed", "timeout", "cancelled"].includes(r.status))) loadHistory();
    }
  });
});
onBeforeUnmount(() => off?.());
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ isCloudAll ? "跨机房命令" : "批量命令" }}</h1>
        <p v-if="isCloudAll">每个机房只收到一条任务，包含该机房全部选中设备；离线中转恢复后自动投递。</p>
        <p v-else>以 root 身份在选手机上执行 bash 命令，结果实时回传。<kbd>Ctrl</kbd> + <kbd>Enter</kbd> 快速执行。</p>
      </div>
    </div>

    <FleetTable v-if="isCloudAll" height="320px" />

    <div :class="isCloudAll ? 'stack' : 'workspace'">
      <el-card v-if="!isCloudAll" shadow="never" class="sticky">
        <template #header><div class="card-title">执行目标</div></template>
        <TargetPicker v-model:mode="mode" v-model:selected="picked" :devices="store.devices" :error="store.devicesError" height="calc(100vh - 470px)" />
      </el-card>

      <div class="stack">
        <el-card shadow="never">
          <template #header>
            <div class="card-title">命令
              <div class="actions muted small">
                目标：<b>{{ isCloudAll ? store.fleet.length : targetCount }}</b> 台{{ !isCloudAll && mode === "online" ? "（全部在线）" : "" }}
              </div>
            </div>
          </template>
          <div v-if="presets.length" class="presets">
            <el-tooltip v-for="p in presets" :key="p.name" :content="p.desc || p.command" placement="top" :show-after="400">
              <el-button size="small" round @click="command = p.command">{{ p.name }}</el-button>
            </el-tooltip>
          </div>
          <CodeEditor v-model="command" :rows="7" placeholder="例如：hostname && uptime" @submit="execute()" />
          <div class="row run">
            <el-button type="primary" :icon="CaretRight" :loading="busy" @click="execute()">
              执行{{ isCloudAll ? "（入队）" : "" }}
            </el-button>
            <el-button v-if="!isCloudAll" :icon="RefreshRight" :disabled="!failedIDs.length" @click="execute(failedIDs)">重跑失败的 {{ failedIDs.length || "" }} 台</el-button>
            <span class="spacer" />
            <span class="muted small">命令在后台执行，关闭页面不影响</span>
          </div>
        </el-card>

        <JobsTable v-if="isCloudAll" ref="jobs" />

        <el-card v-if="!isCloudAll" shadow="never">
          <template #header>
            <div class="card-title">本次结果
              <span v-if="lastCommand" class="mono small muted ellipsis last">{{ lastCommand }}</span>
              <div class="actions">
                <template v-if="list.length">
                  <el-input v-model="resultQuery" size="small" :prefix-icon="Search" placeholder="搜姓名/学号/输出" clearable style="width: 170px" />
                  <el-progress :percentage="Math.round((done / list.length) * 100)" :stroke-width="6" style="width: 110px" :status="failedIDs.length ? 'warning' : done === list.length ? 'success' : undefined" />
                  <el-radio-group v-model="resultFilter" size="small">
                    <el-radio-button value="all">全部 {{ list.length }}</el-radio-button>
                    <el-radio-button value="running">进行中 {{ runningCount }}</el-radio-button>
                    <el-radio-button value="failed">失败 {{ failedIDs.length }}</el-radio-button>
                  </el-radio-group>
                </template>
              </div>
            </div>
          </template>
          <el-table
            :data="filtered"
            row-key="deviceID"
            size="small"
            max-height="480"
            :expand-row-keys="expanded.map(String)"
            empty-text="执行后这里按设备列出输出"
            row-class-name="clickable-row"
            @row-click="(r: Row) => (expanded = expanded.includes(r.deviceID) ? expanded.filter((x) => x !== r.deviceID) : [...expanded, r.deviceID])"
            @expand-change="(_: Row, rowsOpen: Row[]) => (expanded = rowsOpen.map((x) => x.deviceID))"
          >
            <el-table-column type="expand">
              <template #default="{ row }">
                <div class="expand">
                  <pre class="output">{{ row.output || "（暂无输出）" }}</pre>
                  <div class="row">
                    <el-button size="small" :icon="CopyDocument" @click="copy(row.output)">复制输出</el-button>
                    <el-button size="small" :icon="Monitor" @click="store.terminalID = row.deviceID">打开终端</el-button>
                  </div>
                </div>
              </template>
            </el-table-column>
            <el-table-column label="设备" width="170">
              <template #default="{ row }"><b class="mono">#{{ row.deviceID }}</b> <span class="muted small">{{ nameOf(row.deviceID) }}</span></template>
            </el-table-column>
            <el-table-column label="状态" width="100">
              <template #default="{ row }"><el-tag size="small" :type="statusType(row.status)">{{ statusLabel(row.status) }}</el-tag></template>
            </el-table-column>
            <el-table-column label="输出摘要" min-width="220">
              <template #default="{ row }"><span class="mono small ellipsis">{{ row.output.trim().split("\n").pop() || "—" }}</span></template>
            </el-table-column>
            <el-table-column label="耗时" width="90">
              <template #default="{ row }">{{ row.duration ? (row.duration / 1000).toFixed(1) + " s" : "—" }}</template>
            </el-table-column>
            <el-table-column width="70" align="right">
              <template #default="{ row }">
                <el-button v-if="['running', 'dispatched', 'pending'].includes(row.status)" size="small" link type="danger" @click.stop="cancel(row)">终止</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </div>
    </div>

    <el-card shadow="never">
      <template #header>
        <div class="card-title">{{ isCloudAll ? "各机房最近命令" : "历史记录" }}
          <div v-if="!isCloudAll" class="actions"><el-button size="small" text type="danger" :icon="Delete" @click="clearHistory">清空</el-button></div>
        </div>
      </template>
      <el-table :data="isCloudAll ? cloudHistory : history" size="small" max-height="360" empty-text="暂无记录" :row-class-name="isCloudAll ? '' : 'clickable-row'" @row-click="(r: CommandLog) => !isCloudAll && openHistory(r)">
        <el-table-column label="时间" width="170"><template #default="{ row }">{{ formatTime(row.created_at) }}</template></el-table-column>
        <el-table-column v-if="isCloudAll" label="机房" prop="room" width="120" />
        <el-table-column label="目标" width="100"><template #default="{ row }">{{ targetLabel(row) }}</template></el-table-column>
        <el-table-column label="命令" min-width="260"><template #default="{ row }"><span class="mono small ellipsis">{{ row.command }}</span></template></el-table-column>
        <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag size="small" :type="statusType(row.status)">{{ statusLabel(row.status) }}</el-tag></template></el-table-column>
      </el-table>
    </el-card>

    <el-dialog :model-value="!!detail" width="min(900px, 94vw)" append-to-body @update:model-value="(v: boolean) => !v && (detail = null)">
      <template #header><b>命令 #{{ detail?.id }}</b> <span class="muted small">{{ formatTime(detail?.created_at) }}</span></template>
      <template v-if="detail">
        <pre class="output cmd">{{ detail.command }}</pre>
        <el-table v-if="detail.children?.length" :data="detail.children" size="small" max-height="420">
          <el-table-column type="expand">
            <template #default="{ row }"><pre class="output">{{ row.output || row.error_output || "（无输出）" }}</pre></template>
          </el-table-column>
          <el-table-column label="设备" width="160"><template #default="{ row }"><b class="mono">#{{ row.target_id }}</b> <span class="muted small">{{ nameOf(row.target_id) }}</span></template></el-table-column>
          <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag size="small" :type="statusType(row.status)">{{ statusLabel(row.status) }}</el-tag></template></el-table-column>
          <el-table-column label="输出" min-width="240"><template #default="{ row }"><span class="mono small ellipsis">{{ (row.output || row.error_output || "").trim().split("\n").pop() }}</span></template></el-table-column>
        </el-table>
        <pre v-else class="output">{{ detail.output || detail.error_output || "（无输出）" }}</pre>
      </template>
      <template #footer>
        <el-button @click="detail = null">关闭</el-button>
        <el-button type="primary" @click="detail && rerunFrom(detail)">填入编辑器</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.presets { display: flex; flex-wrap: wrap; gap: 6px; margin-bottom: 10px; }
.presets .el-button + .el-button { margin-left: 0; }
.run { margin-top: 12px; }
.last { max-width: 320px; }
.expand { padding: 4px 16px 8px 48px; display: flex; flex-direction: column; gap: 8px; }
.cmd { margin-bottom: 12px; }
</style>
