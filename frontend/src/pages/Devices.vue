<script setup lang="ts">
import { computed, ref } from "vue";
import { Search, Download, Monitor, VideoCamera, More, Operation, FolderOpened, SwitchButton, Delete } from "@element-plus/icons-vue";
import { api, apiPath } from "@/lib/api";
import { store, isCloud, isCloudAll, actOn, confirmAction, run, loadDevices, refreshRooms, fleetDevices } from "@/lib/store";
import { checkinLabel, checkinType, downloadCSV, matchDevice, parseIP, timeAgo } from "@/lib/utils";
import type { Device } from "@/lib/types";
import FleetTable from "@/components/FleetTable.vue";

const query = ref("");
const status = ref<"" | "online" | "offline">("");
const checkin = ref<"" | "0" | "1" | "2">("");
const selected = ref<Device[]>([]);
const tableRef = ref<{ clearSelection: () => void } | null>(null);

const shown = computed(() =>
  store.devices.filter((d) => {
    if (status.value === "online" && !d.connected) return false;
    if (status.value === "offline" && d.connected) return false;
    if (checkin.value !== "" && d.checkin_status !== Number(checkin.value)) return false;
    return matchDevice(d, query.value);
  }),
);
const counts = computed(() => ({
  online: store.devices.filter((d) => d.connected).length,
  offline: store.devices.filter((d) => !d.connected).length,
}));
const selectedIDs = computed(() => selected.value.map((d) => d.assigned_id));

function healthColor(value?: number) {
  if (value == null) return undefined;
  return value >= 90 ? "var(--el-color-danger)" : value >= 75 ? "var(--el-color-warning)" : "var(--el-color-success)";
}

async function resetContest(roomIDs?: string[]) {
  if (!(await confirmAction("赛事重置会清空所有设备记录与签到状态，已连接的机器会重新注册并重新编号。此操作不可撤销，确定继续？", { danger: true, confirmText: "重置", title: "赛事重置" }))) return;
  await run(() => api("/api/contest/reset", { json: { room_ids: roomIDs || [] } }), "赛事重置已提交");
  loadDevices();
  refreshRooms().catch(() => {});
}

async function removeOffline() {
  const ids = selected.value.filter((d) => !d.connected).map((d) => d.assigned_id);
  if (!ids.length) return;
  if (!(await confirmAction(`删除 ${ids.length} 台离线设备的记录？签到与事件记录将一并删除。`, { danger: true, confirmText: "删除" }))) return;
  for (const id of ids) await api("/api/devices/" + id, { method: "DELETE" }).catch(() => {});
  tableRef.value?.clearSelection();
  loadDevices();
}

function exportFleet() {
  const rows: (string | number)[][] = [["机房", "编号", "主机名", "选手", "学号", "签到", "在线"]];
  fleetDevices.value.forEach((d) => rows.push([d.room_name, d.assigned_id, d.hostname, d.student_name || "", d.student_num || "", checkinLabel(d.checkin_status), d.connected ? "在线" : "离线"]));
  downloadCSV("icpc-devices.csv", rows);
}

function onCommand(cmd: string, d: Device) {
  if (cmd === "detail") store.detailID = d.assigned_id;
  if (cmd === "command") actOn("commands", [d.assigned_id]);
}
</script>

<template>
  <div class="page">
    <template v-if="isCloudAll">
      <div class="page-head">
        <div><h1>全部设备</h1><p>编号仅在同一机房内唯一。勾选后可到命令、分发、电源页统一操作。</p></div>
        <div class="actions">
          <el-button :icon="Download" @click="exportFleet">导出 CSV</el-button>
          <el-button type="danger" plain @click="resetContest()">赛事重置</el-button>
        </div>
      </div>
      <FleetTable height="calc(100vh - 280px)" />
    </template>

    <template v-else>
      <div class="page-head">
        <div><h1>设备</h1><p>点击任意一行查看详情；勾选多台后可批量操作。</p></div>
        <div class="actions">
          <el-button tag="a" :href="apiPath('/api/devices/export')" :icon="Download">导出 Excel</el-button>
          <el-dropdown trigger="click">
            <el-button :icon="More" />
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item @click="resetContest(isCloud && store.roomID ? [store.roomID] : undefined)">
                  <span style="color: var(--el-color-danger)">赛事重置（清空全部设备）</span>
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </div>

      <el-card shadow="never" body-style="padding: 12px 16px">
        <div class="row toolbar">
          <el-input v-model="query" :prefix-icon="Search" placeholder="搜索编号 / 姓名 / 学号 / IP / MAC" clearable class="search" />
          <el-radio-group v-model="status">
            <el-radio-button value="">全部 {{ store.devices.length }}</el-radio-button>
            <el-radio-button value="online">在线 {{ counts.online }}</el-radio-button>
            <el-radio-button value="offline">离线 {{ counts.offline }}</el-radio-button>
          </el-radio-group>
          <el-select v-model="checkin" placeholder="签到状态" clearable style="width: 120px">
            <el-option label="未签到" value="0" />
            <el-option label="已签到" value="1" />
            <el-option label="已签退" value="2" />
          </el-select>
        </div>

        <transition name="el-fade-in">
          <div v-if="selected.length" class="bulk">
            <span>已选 <b>{{ selected.length }}</b> 台</span>
            <el-button size="small" :icon="Operation" @click="actOn('commands', selectedIDs)">发命令</el-button>
            <el-button size="small" :icon="FolderOpened" @click="actOn('distribute', selectedIDs)">分发文件</el-button>
            <el-button size="small" :icon="SwitchButton" @click="actOn('power', selectedIDs)">电源</el-button>
            <el-button size="small" :icon="Delete" type="danger" plain :disabled="!selected.some((d) => !d.connected)" @click="removeOffline">删除离线记录</el-button>
            <span class="spacer" />
            <el-button size="small" text @click="tableRef?.clearSelection()">取消选择</el-button>
          </div>
        </transition>

        <el-table
          ref="tableRef"
          v-loading="store.devicesLoading"
          :data="shown"
          row-key="assigned_id"
          height="calc(100vh - 300px)"
          row-class-name="clickable-row"
          :empty-text="store.devicesError || (store.devices.length ? '没有匹配的设备' : '还没有设备注册')"
          :default-sort="{ prop: 'assigned_id', order: 'ascending' }"
          @selection-change="(rows: Device[]) => (selected = rows)"
          @row-click="(row: Device) => (store.detailID = row.assigned_id)"
        >
          <el-table-column type="selection" width="40" reserve-selection />
          <el-table-column label="编号" prop="assigned_id" width="80" sortable>
            <template #default="{ row }"><b class="mono">#{{ row.assigned_id }}</b></template>
          </el-table-column>
          <el-table-column label="选手" min-width="160" sortable :sort-by="(row: Device) => row.student_num || '~'">
            <template #default="{ row }">
              <div v-if="row.student_name">{{ row.student_name }}<div class="muted small mono">{{ row.student_num }}</div></div>
              <span v-else class="muted">—</span>
            </template>
          </el-table-column>
          <el-table-column label="主机 / IP" min-width="170">
            <template #default="{ row }">
              <div class="ellipsis">{{ row.hostname }}</div>
              <div class="muted small mono">{{ parseIP(row.local_ip) || "—" }}</div>
            </template>
          </el-table-column>
          <el-table-column label="健康" width="170">
            <template #default="{ row }">
              <div v-if="row.connected && row.cpu_pct != null" class="health">
                <span>CPU</span><el-progress :percentage="Math.round(row.cpu_pct)" :color="healthColor(row.cpu_pct)" :stroke-width="5" />
                <span>内存</span><el-progress :percentage="Math.round(row.mem_pct || 0)" :color="healthColor(row.mem_pct)" :stroke-width="5" />
              </div>
              <span v-else class="muted small">—</span>
            </template>
          </el-table-column>
          <el-table-column label="签到" width="90" sortable :sort-by="(row: Device) => row.checkin_status">
            <template #default="{ row }"><el-tag size="small" :type="checkinType(row.checkin_status)">{{ checkinLabel(row.checkin_status) }}</el-tag></template>
          </el-table-column>
          <el-table-column label="状态" width="110" sortable :sort-by="(row: Device) => (row.connected ? 0 : 1)">
            <template #default="{ row }">
              <span class="row nowrap"><span class="dot" :class="{ on: row.connected }" />{{ row.connected ? "在线" : "离线" }}</span>
              <div v-if="!row.connected" class="muted small">{{ timeAgo(row.last_seen) }}</div>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="150" fixed="right" align="right">
            <template #default="{ row }">
              <div class="row nowrap ops" @click.stop>
                <el-tooltip content="终端"><el-button size="small" circle :icon="Monitor" :disabled="!row.connected" @click="store.terminalID = row.assigned_id" /></el-tooltip>
                <el-tooltip content="看屏幕"><el-button size="small" circle :icon="VideoCamera" :disabled="!row.connected" @click="store.screenID = row.assigned_id" /></el-tooltip>
                <el-dropdown trigger="click" @command="(c: string) => onCommand(c, row)">
                  <el-button size="small" circle :icon="More" />
                  <template #dropdown>
                    <el-dropdown-menu>
                      <el-dropdown-item command="detail">查看详情</el-dropdown-item>
                      <el-dropdown-item command="command">对它发命令</el-dropdown-item>
                    </el-dropdown-menu>
                  </template>
                </el-dropdown>
              </div>
            </template>
          </el-table-column>
        </el-table>
      </el-card>
    </template>
  </div>
</template>

<style scoped>
.toolbar { margin-bottom: 12px; }
.search { width: 300px; }
.bulk { display: flex; align-items: center; gap: 8px; padding: 8px 12px; margin-bottom: 10px; border-radius: 8px; background: var(--el-color-primary-light-9); flex-wrap: wrap; }
.bulk .el-button + .el-button { margin-left: 0; }
.health { display: grid; grid-template-columns: 30px 1fr; align-items: center; gap: 0 6px; font-size: 11px; color: var(--el-text-color-secondary); }
.health :deep(.el-progress__text) { font-size: 11px !important; min-width: 34px; }
.ops { justify-content: flex-end; gap: 6px; }
.ops .el-button + .el-button { margin-left: 0; }
</style>
