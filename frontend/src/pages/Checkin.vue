<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { Search, Download, Sort, RefreshLeft } from "@element-plus/icons-vue";
import { api, apiPath } from "@/lib/api";
import { store, isCloudAll, run, confirmAction, loadDevices, fleetDevices, toast } from "@/lib/store";
import { checkinLabel, checkinType, downloadCSV, formatTime, matchDevice } from "@/lib/utils";
import type { CheckinStats, Device } from "@/lib/types";
import CheckinDialog from "@/components/CheckinDialog.vue";
import FleetTable from "@/components/FleetTable.vue";

const query = ref("");
const status = ref<"" | "0" | "1" | "2">("");
const checkinID = ref<number | null>(null);
const swapFrom = ref<number | null>(null);
const swapTo = ref<number | null>(null);
const serverStats = ref<CheckinStats | null>(null);

const stats = computed<CheckinStats>(() => {
  const list = isCloudAll.value ? fleetDevices.value : store.devices;
  const inn = list.filter((d) => d.checkin_status === 1).length;
  const out = list.filter((d) => d.checkin_status === 2).length;
  return { total: list.length, checked_in: inn, checked_out: out, not_checked: list.length - inn - out };
});

const shown = computed(() =>
  store.devices.filter((d) => (status.value === "" || d.checkin_status === Number(status.value)) && matchDevice(d, query.value)),
);
const swapCandidates = computed(() => store.devices.filter((d) => d.assigned_id !== swapFrom.value));
const swapQuery = ref("");
const filteredSwapCandidates = computed(() =>
  swapCandidates.value.filter((d) => matchDevice(d, swapQuery.value)),
);

async function loadStats() {
  if (!isCloudAll.value) serverStats.value = await api<CheckinStats>("/api/checkin/stats").catch(() => null);
}
watch(() => store.devices, loadStats);
onMounted(loadStats);

async function act(d: Device, kind: "checkout" | "restore" | "reset") {
  const label = { checkout: "签退", restore: "恢复签到", reset: "清除签到信息" }[kind];
  if (kind !== "restore" && !(await confirmAction(`为 #${d.assigned_id} ${d.student_name || ""} ${label}？`, { danger: kind === "reset" }))) return;
  await run(() => api(`/api/checkin/${d.assigned_id}/${kind}`, { method: "POST" }), `#${d.assigned_id} 已${label}`);
  loadDevices();
}

async function doSwap() {
  if (swapFrom.value == null || swapTo.value == null) return toast("请选择目标设备", "warning");
  const ok = await run(() => api("/api/checkin/swap", { json: { from_assigned_id: swapFrom.value, to_assigned_id: swapTo.value } }), `已把 #${swapFrom.value} 的签到换到 #${swapTo.value}`);
  if (ok !== undefined) {
    swapFrom.value = null;
    swapTo.value = null;
    loadDevices();
  }
}

async function resetAll() {
  if (!(await confirmAction("清空本机房全部签到记录？此操作不可撤销，建议先导出 Excel。", { danger: true, confirmText: "全部清空" }))) return;
  await run(() => api("/api/checkin/reset-all", { method: "POST" }), "已清空全部签到");
  loadDevices();
}

function exportCloud() {
  const rows: (string | number)[][] = [["机房", "编号", "选手", "学号", "状态"]];
  fleetDevices.value.forEach((d) => rows.push([d.room_name, d.assigned_id, d.student_name || "", d.student_num || "", checkinLabel(d.checkin_status)]));
  downloadCSV("icpc-checkin.csv", rows);
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ isCloudAll ? "签到总览" : "签到" }}</h1>
        <p>{{ isCloudAll ? "签到操作请进入具体机房完成，这里只汇总。" : "选手通常在选手机上自行签到；这里用于补签、签退、换机。" }}</p>
      </div>
      <div class="actions">
        <el-button v-if="isCloudAll" :icon="Download" @click="exportCloud">导出 CSV</el-button>
        <template v-else>
          <el-button tag="a" :href="apiPath('/api/checkin/export')" :icon="Download">导出 Excel</el-button>
          <el-button type="danger" plain :icon="RefreshLeft" @click="resetAll">全部清空</el-button>
        </template>
      </div>
    </div>

    <div class="metrics">
      <button class="metric" @click="status = ''"><div><div class="value">{{ stats.total }}</div><div class="label">设备</div></div></button>
      <button class="metric" @click="status = '1'"><div><div class="value ok">{{ stats.checked_in }}</div><div class="label">已签到</div></div>
        <el-progress type="circle" :width="44" :stroke-width="5" :percentage="stats.total ? Math.round((stats.checked_in / stats.total) * 100) : 0" class="ring" /></button>
      <button class="metric" @click="status = '0'"><div><div class="value">{{ stats.not_checked }}</div><div class="label">未签到</div></div></button>
      <button class="metric" @click="status = '2'"><div><div class="value warn">{{ stats.checked_out }}</div><div class="label">已签退</div></div></button>
    </div>

    <FleetTable v-if="isCloudAll" :selectable="false" manage-page="checkin" height="calc(100vh - 360px)" />

    <el-card v-else shadow="never" body-style="padding: 12px 16px">
      <div class="row toolbar">
        <el-input v-model="query" :prefix-icon="Search" placeholder="搜索姓名 / 学号 / 编号" clearable style="width: 280px" />
        <el-radio-group v-model="status">
          <el-radio-button value="">全部</el-radio-button>
          <el-radio-button value="1">已签到</el-radio-button>
          <el-radio-button value="0">未签到</el-radio-button>
          <el-radio-button value="2">已签退</el-radio-button>
        </el-radio-group>
      </div>
      <el-table :data="shown" row-key="assigned_id" height="calc(100vh - 380px)" :empty-text="store.devices.length ? '没有匹配的设备' : '还没有设备'" :default-sort="{ prop: 'assigned_id', order: 'ascending' }">
        <el-table-column label="编号" prop="assigned_id" width="90" sortable>
          <template #default="{ row }"><el-button link type="primary" class="mono" @click="store.detailID = row.assigned_id"><b>#{{ row.assigned_id }}</b></el-button></template>
        </el-table-column>
        <el-table-column label="姓名" prop="student_name" min-width="110" sortable><template #default="{ row }">{{ row.student_name || "—" }}</template></el-table-column>
        <el-table-column label="学号" prop="student_num" min-width="130" sortable><template #default="{ row }"><span class="mono">{{ row.student_num || "—" }}</span></template></el-table-column>
        <el-table-column label="状态" width="100" sortable :sort-by="(row: Device) => row.checkin_status">
          <template #default="{ row }"><el-tag size="small" :type="checkinType(row.checkin_status)">{{ checkinLabel(row.checkin_status) }}</el-tag></template>
        </el-table-column>
        <el-table-column label="签到时间" width="170"><template #default="{ row }"><span class="small">{{ row.checkin_status ? formatTime(row.checkin_time) : "—" }}</span></template></el-table-column>
        <el-table-column label="设备" width="90"><template #default="{ row }"><span class="row nowrap"><span class="dot" :class="{ on: row.connected }" />{{ row.connected ? "在线" : "离线" }}</span></template></el-table-column>
        <el-table-column label="操作" width="230" align="right" fixed="right">
          <template #default="{ row }">
            <div class="ops">
              <el-button v-if="row.checkin_status !== 1" size="small" type="primary" @click="checkinID = row.assigned_id">{{ row.checkin_status === 2 ? "重新签到" : "签到" }}</el-button>
              <el-button v-if="row.checkin_status === 1" size="small" @click="act(row, 'checkout')">签退</el-button>
              <el-button v-if="row.checkin_status === 2" size="small" @click="act(row, 'restore')">恢复</el-button>
              <el-button v-if="row.checkin_status !== 0" size="small" :icon="Sort" @click="(swapFrom = row.assigned_id), (swapTo = null), (swapQuery = '')">换机</el-button>
              <el-dropdown v-if="row.checkin_status !== 0" trigger="click">
                <el-button size="small" text>…</el-button>
                <template #dropdown><el-dropdown-menu><el-dropdown-item @click="act(row, 'reset')"><span style="color: var(--el-color-danger)">清除签到信息</span></el-dropdown-item></el-dropdown-menu></template>
              </el-dropdown>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <CheckinDialog :id="checkinID" @close="checkinID = null" />

    <el-dialog :model-value="swapFrom != null" title="换机" width="440px" append-to-body @update:model-value="(v: boolean) => !v && (swapFrom = null)">
      <p class="muted" style="margin-top: 0">把 <b>#{{ swapFrom }}</b> 的签到信息转移到另一台设备（目标若已有签到，两者互换）。</p>
      <el-select
        v-model="swapTo"
        filterable
        placeholder="选择目标设备（可输入编号 / 姓名 / 学号搜索）"
        style="width: 100%"
        :filter-method="(q: string) => { swapQuery = q; }"
      >
        <el-option
          v-for="d in filteredSwapCandidates"
          :key="d.assigned_id"
          :value="d.assigned_id"
          :label="`#${d.assigned_id} ${d.student_name || '空闲'} ${d.student_num || ''}`"
        >
          <span class="row nowrap"><span class="dot" :class="{ on: d.connected }" /><b class="mono">#{{ d.assigned_id }}</b>{{ d.student_name || "空闲" }} <span class="muted small">{{ d.student_num }}</span></span>
        </el-option>
      </el-select>
      <template #footer>
        <el-button @click="swapFrom = null">取消</el-button>
        <el-button type="primary" :disabled="swapTo == null" @click="doSwap">确认换机</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.toolbar { margin-bottom: 12px; }
.value.ok { color: var(--el-color-success); }
.value.warn { color: var(--el-color-warning); }
.ring { margin-left: auto; }
.ops { display: inline-flex; gap: 6px; justify-content: flex-end; }
.ops .el-button + .el-button { margin-left: 0; }
</style>
