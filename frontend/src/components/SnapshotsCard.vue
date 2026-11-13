<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from "vue";
import { api } from "@/lib/api";
import { onAdmin, confirmAction, run } from "@/lib/store";
import { formatTime } from "@/lib/utils";
import type { Snapshot, SnapshotOp } from "@/lib/types";

const KINDS: Record<string, string> = { command: "命令", distribute: "分发", wol: "唤醒", schedule: "电源计划", broadcast: "广播" };
const active = ref<Snapshot | null>(null);
const ops = ref<SnapshotOp[]>([]);
const scope = ref("");
const name = ref("");

async function load() {
  const data = await api<{ active: Snapshot | null; scope: string }>("/api/snapshots");
  active.value = data.active;
  scope.value = data.scope;
  ops.value = data.active ? (await api<SnapshotOp[]>(`/api/snapshots/${data.active.id}/ops`)) || [] : [];
}

async function start() {
  if (!name.value.trim()) return;
  if (!(await confirmAction("启用后，之后对“全部设备”的操作会记录下来，并自动补发给之后才上线的设备。继续？"))) return;
  if ((await run(() => api("/api/snapshots", { json: { name: name.value.trim() } }), "快照已启用")) !== undefined) {
    name.value = "";
    load();
  }
}
async function end() {
  if (!active.value || !(await confirmAction("结束后，新上线的设备不再补执行这些操作。"))) return;
  await run(() => api(`/api/snapshots/${active.value!.id}/end`, { method: "POST" }), "快照已结束");
  load();
}
async function removeOp(op: SnapshotOp) {
  if (!active.value || !(await confirmAction("从队列移除后，之后上线的设备不再执行它。"))) return;
  await run(() => api(`/api/snapshots/${active.value!.id}/ops/${op.id}`, { method: "DELETE" }));
  load();
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
      <div class="card-title">操作快照
        <el-tooltip content="开启后，对全体设备的命令、分发等操作会进入队列；比赛中途才开机的设备上线后会自动补执行。" placement="top">
          <el-tag size="small" type="info">{{ scope === "cloud" ? "云端队列" : "本机房队列" }}</el-tag>
        </el-tooltip>
        <div class="actions">
          <template v-if="active">
            <el-tag type="success">记录中：{{ active.name }}</el-tag>
            <el-button size="small" type="danger" plain @click="end">结束</el-button>
          </template>
        </div>
      </div>
    </template>
    <div v-if="!active" class="row">
      <el-input v-model="name" placeholder="快照名称，例如：2026 校赛" style="max-width: 280px" @keyup.enter="start" />
      <el-button type="primary" :disabled="!name.trim()" @click="start">开始记录</el-button>
      <span class="muted small">用于让晚开机的设备自动补上之前的全员操作</span>
    </div>
    <template v-else>
      <div class="muted small" style="margin-bottom: 8px">开始于 {{ formatTime(active.created_at) }} · 共 {{ active.op_count }} 条</div>
      <el-table :data="ops" size="small" max-height="260" empty-text="队列还是空的">
        <el-table-column label="类型" width="90"><template #default="{ row }">{{ KINDS[row.kind] || row.kind }}</template></el-table-column>
        <el-table-column label="内容" prop="summary" min-width="200" show-overflow-tooltip />
        <el-table-column label="已补发" prop="delivered" width="80" />
        <el-table-column width="70" align="right"><template #default="{ row }"><el-button link type="danger" size="small" @click="removeOp(row)">移除</el-button></template></el-table-column>
      </el-table>
    </template>
  </el-card>
</template>
