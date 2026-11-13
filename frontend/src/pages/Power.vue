<script setup lang="ts">
import { onMounted, ref } from "vue";
import { Sunrise, SwitchButton, RefreshRight, Delete } from "@element-plus/icons-vue";
import { api } from "@/lib/api";
import { store, isCloudAll, run, toast, confirmAction, takePendingTargets, queueFleet, onlineDevices } from "@/lib/store";
import { formatTime, statusLabel, statusType } from "@/lib/utils";
import type { Schedule } from "@/lib/types";
import TargetPicker from "@/components/TargetPicker.vue";
import FleetTable from "@/components/FleetTable.vue";
import JobsTable from "@/components/JobsTable.vue";

const ACTIONS: Record<string, string> = { shutdown: "关机", reboot: "重启", wol: "唤醒" };
const mode = ref<"online" | "picked">("online");
const picked = ref<number[]>([]);
const schedules = ref<Schedule[]>([]);
const action = ref("shutdown");
const when = ref<Date | null>(null);
const note = ref("");
const jobs = ref<{ load: () => void } | null>(null);

async function load() {
  if (!isCloudAll.value) schedules.value = (await api<Schedule[]>("/api/power/schedules").catch(() => [])) || [];
}

function targetText() {
  if (isCloudAll.value) return `${store.fleet.length} 台已选设备`;
  return mode.value === "online" ? "全部设备" : `${picked.value.length} 台设备`;
}

function needTargets() {
  if (isCloudAll.value && !store.fleet.length) return toast("请先勾选设备", "warning"), false;
  if (!isCloudAll.value && mode.value === "picked" && !picked.value.length) return toast("请先勾选设备", "warning"), false;
  return true;
}

async function wake() {
  if (!needTargets()) return;
  if (isCloudAll.value) {
    await run(() => queueFleet(store.fleet, "wol"), "唤醒已入队");
    return jobs.value?.load();
  }
  // "全部在线" makes no sense for wake-up: target every known device instead.
  const body = mode.value === "online" ? { target_type: "all" } : { target_type: "list", device_ids: picked.value };
  const result = await run(() => api<{ sent: number; total: number }>("/api/power/wol", { json: body }));
  if (result) toast(`已发送 ${result.sent} / ${result.total} 个唤醒包`, "success");
}

async function powerCommand(kind: "shutdown" | "reboot") {
  if (!needTargets()) return;
  const command = kind === "shutdown" ? "shutdown -h now" : "reboot";
  const target = isCloudAll.value ? targetText() : mode.value === "online" ? `全部在线设备（${onlineDevices.value.length} 台）` : `${picked.value.length} 台设备`;
  if (!(await confirmAction(`确定对 ${target} 执行${ACTIONS[kind]}？选手未保存的内容会丢失。`, { danger: true, confirmText: ACTIONS[kind] }))) return;
  if (isCloudAll.value) {
    await run(() => queueFleet(store.fleet, "command", { command }), "已加入队列");
    return jobs.value?.load();
  }
  const body = mode.value === "online" ? { target_type: "broadcast", command } : { target_type: "list", target_ids: picked.value, command };
  await run(() => api("/api/commands", { json: body }), `已下发${ACTIONS[kind]}`);
}

async function createSchedule() {
  if (!when.value) return toast("请选择执行时间", "warning");
  if (when.value.getTime() < Date.now()) return toast("执行时间不能早于现在", "warning");
  if (!needTargets()) return;
  if (isCloudAll.value) {
    await run(() => queueFleet(store.fleet, "schedule", { action: action.value, run_at: when.value!.toISOString(), note: note.value }), "计划已入队");
    return jobs.value?.load();
  }
  const ok = await run(() => api("/api/power/schedules", {
    json: {
      action: action.value,
      run_at: when.value!.toISOString(),
      note: note.value,
      target_type: mode.value === "online" ? "all" : "list",
      device_ids: mode.value === "online" ? [] : picked.value,
    },
  }), "已创建计划");
  if (ok !== undefined) {
    note.value = "";
    load();
  }
}

async function removeSchedule(s: Schedule) {
  if (!(await confirmAction(`删除 ${formatTime(s.run_at)} 的${ACTIONS[s.action] || s.action}计划？`))) return;
  await run(() => api("/api/power/schedules/" + s.id, { method: "DELETE" }), "已删除");
  load();
}

onMounted(() => {
  const ids = takePendingTargets();
  if (ids.length) {
    mode.value = "picked";
    picked.value = ids;
  }
  load();
});
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div><h1>电源</h1><p>唤醒通过网卡魔术包（需 BIOS 开启 Wake-on-LAN）；关机和重启下发到选手机执行。</p></div>
    </div>
    <FleetTable v-if="isCloudAll" height="320px" />
    <div :class="isCloudAll ? 'stack' : 'workspace'">
      <el-card v-if="!isCloudAll" shadow="never">
        <template #header><div class="card-title">目标设备</div></template>
        <TargetPicker v-model:mode="mode" v-model:selected="picked" :devices="store.devices" height="calc(100vh - 470px)" />
      </el-card>
      <div class="stack">
        <el-card shadow="never">
          <template #header><div class="card-title">立即操作<div class="actions muted small">目标：{{ targetText() }}</div></div></template>
          <div class="power-buttons">
            <button class="pbtn wake" @click="wake"><el-icon><Sunrise /></el-icon><b>唤醒</b><span>发送魔术包开机</span></button>
            <button class="pbtn reboot" @click="powerCommand('reboot')"><el-icon><RefreshRight /></el-icon><b>重启</b><span>立即重启在线设备</span></button>
            <button class="pbtn off" @click="powerCommand('shutdown')"><el-icon><SwitchButton /></el-icon><b>关机</b><span>立即关闭在线设备</span></button>
          </div>
        </el-card>
        <el-card shadow="never">
          <template #header><div class="card-title">定时计划</div></template>
          <div class="row">
            <el-radio-group v-model="action">
              <el-radio-button value="wol">唤醒</el-radio-button>
              <el-radio-button value="reboot">重启</el-radio-button>
              <el-radio-button value="shutdown">关机</el-radio-button>
            </el-radio-group>
            <el-date-picker v-model="when" type="datetime" placeholder="执行时间" format="YYYY-MM-DD HH:mm" :disabled-date="(d: Date) => d.getTime() < Date.now() - 86400000" style="width: 200px" />
            <el-input v-model="note" placeholder="备注（可选）" style="width: 200px" />
            <el-button type="primary" @click="createSchedule">创建计划</el-button>
          </div>
          <el-table v-if="!isCloudAll" :data="schedules" size="small" style="margin-top: 12px" empty-text="没有计划">
            <el-table-column label="执行时间" width="180"><template #default="{ row }">{{ formatTime(row.run_at) }}</template></el-table-column>
            <el-table-column label="动作" width="80"><template #default="{ row }">{{ ACTIONS[row.action] || row.action }}</template></el-table-column>
            <el-table-column label="目标" min-width="160">
              <template #default="{ row }">{{ row.target_type === "all" ? "全部设备" : (row.target_ids || []).map((id: number) => "#" + id).join(" ") }}</template>
            </el-table-column>
            <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag size="small" :type="statusType(row.status)">{{ statusLabel(row.status) }}</el-tag></template></el-table-column>
            <el-table-column label="备注" prop="note" min-width="120" show-overflow-tooltip />
            <el-table-column width="60" align="right"><template #default="{ row }"><el-button link type="danger" :icon="Delete" @click="removeSchedule(row)" /></template></el-table-column>
          </el-table>
        </el-card>
        <JobsTable v-if="isCloudAll" ref="jobs" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.power-buttons { display: grid; grid-template-columns: repeat(3, 1fr); gap: 12px; }
.pbtn { display: flex; flex-direction: column; align-items: center; gap: 4px; padding: 18px 8px; border-radius: 10px; border: 1px solid var(--app-border); background: var(--el-bg-color); cursor: pointer; font: inherit; color: inherit; transition: all .15s; }
.pbtn .el-icon { font-size: 28px; }
.pbtn span { font-size: 12px; color: var(--el-text-color-secondary); }
.pbtn.wake .el-icon { color: var(--el-color-success); }
.pbtn.reboot .el-icon { color: var(--el-color-warning); }
.pbtn.off .el-icon { color: var(--el-color-danger); }
.pbtn:hover { box-shadow: var(--el-box-shadow-light); border-color: var(--el-color-primary-light-5); }
</style>
