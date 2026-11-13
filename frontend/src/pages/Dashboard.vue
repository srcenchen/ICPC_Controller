<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { Monitor, CircleCheck, CircleClose, Tickets, VideoCamera, Operation, FolderOpened, Notification, Warning } from "@element-plus/icons-vue";
import { api } from "@/lib/api";
import { store, go, isCloudAll, onAdmin, setRoom, toastError, fleetDevices } from "@/lib/store";
import { formatTime, statusLabel, statusType, targetLabel, timeAgo } from "@/lib/utils";
import type { CommandLog, DistributeTask, Stats } from "@/lib/types";
import DistributionProgress from "@/components/DistributionProgress.vue";
import SnapshotsCard from "@/components/SnapshotsCard.vue";

const stats = ref<Stats | null>(null);
const task = ref<DistributeTask | null>(null);

const local = computed(() => {
  const list = store.devices;
  return {
    total: list.length,
    online: list.filter((d) => d.connected).length,
    checked: list.filter((d) => d.checkin_status === 1).length,
  };
});

const cloud = computed(() => {
  const list = fleetDevices.value;
  return {
    total: list.length,
    online: list.filter((d) => d.connected).length,
    checked: list.filter((d) => d.checkin_status === 1).length,
    recent: store.rooms
      .flatMap((room) => (room.recent_commands || []).map((c) => ({ ...c, room: room.name })))
      .sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at))
      .slice(0, 12),
  };
});

/** Machines that need attention: online but over a health threshold. */
const alerts = computed(() =>
  store.devices
    .filter((d) => d.connected)
    .map((d) => {
      const issues: string[] = [];
      if ((d.cpu_pct ?? 0) >= 90) issues.push(`CPU ${d.cpu_pct!.toFixed(0)}%`);
      if ((d.mem_pct ?? 0) >= 90) issues.push(`内存 ${d.mem_pct!.toFixed(0)}%`);
      if ((d.disk_pct ?? 0) >= 90) issues.push(`磁盘 ${d.disk_pct!.toFixed(0)}%`);
      if ((d.temp_c ?? 0) >= 85) issues.push(`温度 ${d.temp_c!.toFixed(0)}°C`);
      return { d, issues };
    })
    .filter((x) => x.issues.length),
);

const offlineCheckedIn = computed(() => store.devices.filter((d) => !d.connected && d.checkin_status === 1));

let statsTimer = 0;
let taskTimer = 0;
async function loadStats() {
  try {
    stats.value = await api<Stats>("/api/stats");
  } catch (err) {
    if (!stats.value) toastError(err);
  }
}
async function loadTask() {
  task.value = await api<DistributeTask>("/api/distribution/status").catch(() => null);
}

let off: (() => void) | undefined;
onMounted(() => {
  if (isCloudAll.value) return;
  loadStats();
  loadTask();
  off = onAdmin(({ event }) => {
    if (event.startsWith("distribute_") && !taskTimer) taskTimer = window.setTimeout(() => { taskTimer = 0; loadTask(); }, 500);
    if (["command_result", "device_connected", "device_disconnected", "checkin_updated"].includes(event) && !statsTimer) {
      statsTimer = window.setTimeout(() => { statsTimer = 0; loadStats(); }, 600);
    }
  });
});
onBeforeUnmount(() => {
  off?.();
  clearTimeout(statsTimer);
  clearTimeout(taskTimer);
});

function openCommand(c: CommandLog) {
  store.pendingCommandID = c.id;
  go("commands");
}
</script>

<template>
  <div class="page">
    <!-- Cloud: all rooms -->
    <template v-if="isCloudAll">
      <div class="page-head"><div><h1>全部机房总览</h1><p>点击机房卡片进入该机房管理。</p></div></div>
      <div class="metrics">
        <button class="metric" @click="go('devices')"><span class="icon" style="background: var(--el-color-primary-light-9); color: var(--el-color-primary)"><el-icon><Monitor /></el-icon></span><div><div class="value">{{ cloud.total }}</div><div class="label">设备总数</div></div></button>
        <button class="metric" @click="go('devices')"><span class="icon" style="background: var(--el-color-success-light-9); color: var(--el-color-success)"><el-icon><CircleCheck /></el-icon></span><div><div class="value">{{ cloud.online }}</div><div class="label">在线</div></div></button>
        <button class="metric" @click="go('checkin')"><span class="icon" style="background: var(--el-color-warning-light-9); color: var(--el-color-warning)"><el-icon><Tickets /></el-icon></span><div><div class="value">{{ cloud.checked }}</div><div class="label">已签到</div></div></button>
        <div class="metric"><span class="icon" style="background: var(--el-fill-color); color: var(--el-text-color-secondary)"><el-icon><Notification /></el-icon></span><div><div class="value">{{ store.rooms.filter((r) => r.online).length }} / {{ store.rooms.length }}</div><div class="label">中转在线</div></div></div>
      </div>
      <div class="rooms">
        <el-card v-for="room in store.rooms" :key="room.id" shadow="hover" class="room" @click="setRoom(room.id, 'dashboard')">
          <div class="row"><b>{{ room.name }}</b><span class="spacer" /><el-tag size="small" :type="room.online ? 'success' : 'info'">{{ room.online ? "在线" : "离线" }}</el-tag></div>
          <div class="room-num">{{ room.devices.filter((d) => d.connected).length }}<span class="muted"> / {{ room.devices.length }}</span></div>
          <el-progress :percentage="room.devices.length ? Math.round((room.devices.filter((d) => d.connected).length / room.devices.length) * 100) : 0" :show-text="false" />
          <div class="muted small">最后上报 {{ timeAgo(room.last_seen) }}</div>
        </el-card>
        <el-empty v-if="!store.rooms.length" description="还没有机房连接" />
      </div>
      <el-card shadow="never">
        <template #header><div class="card-title">各机房最近命令</div></template>
        <el-table :data="cloud.recent" size="small" empty-text="暂无命令">
          <el-table-column label="机房" prop="room" width="140" />
          <el-table-column label="命令" min-width="260"><template #default="{ row }"><span class="mono ellipsis">{{ row.command }}</span></template></el-table-column>
          <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag size="small" :type="statusType(row.status)">{{ statusLabel(row.status) }}</el-tag></template></el-table-column>
          <el-table-column label="时间" width="110"><template #default="{ row }">{{ timeAgo(row.created_at) }}</template></el-table-column>
        </el-table>
      </el-card>
      <SnapshotsCard />
    </template>

    <!-- Single room -->
    <template v-else>
      <div class="metrics">
        <button class="metric" @click="go('devices')"><span class="icon" style="background: var(--el-color-primary-light-9); color: var(--el-color-primary)"><el-icon><Monitor /></el-icon></span><div><div class="value">{{ local.total }}</div><div class="label">设备总数</div></div></button>
        <button class="metric" @click="go('devices')"><span class="icon" style="background: var(--el-color-success-light-9); color: var(--el-color-success)"><el-icon><CircleCheck /></el-icon></span><div><div class="value">{{ local.online }}</div><div class="label">在线</div></div></button>
        <button class="metric" @click="go('devices')"><span class="icon" style="background: var(--el-fill-color); color: var(--el-text-color-secondary)"><el-icon><CircleClose /></el-icon></span><div><div class="value">{{ local.total - local.online }}</div><div class="label">离线</div></div></button>
        <button class="metric" @click="go('checkin')"><span class="icon" style="background: var(--el-color-warning-light-9); color: var(--el-color-warning)"><el-icon><Tickets /></el-icon></span><div><div class="value">{{ local.checked }}<span class="muted small"> / {{ local.total }}</span></div><div class="label">已签到</div></div></button>
      </div>

      <div class="quick">
        <el-button :icon="VideoCamera" @click="go('screen')">屏幕墙</el-button>
        <el-button :icon="Operation" @click="go('commands')">批量命令</el-button>
        <el-button :icon="FolderOpened" @click="go('distribute')">文件分发</el-button>
        <el-button :icon="Tickets" @click="go('checkin')">签到管理</el-button>
        <el-button :icon="Notification" @click="go('broadcast')">广播</el-button>
      </div>

      <el-card v-if="task?.status === 'running'" shadow="never">
        <template #header><div class="card-title">正在分发<div class="actions"><el-button size="small" link type="primary" @click="go('distribute')">查看详情</el-button></div></div></template>
        <DistributionProgress :task="task" />
      </el-card>

      <div class="grid2">
        <el-card shadow="never">
          <template #header>
            <div class="card-title"><el-icon color="var(--el-color-warning)"><Warning /></el-icon>需要关注
              <el-tag v-if="alerts.length + offlineCheckedIn.length" size="small" type="warning" round>{{ alerts.length + offlineCheckedIn.length }}</el-tag>
            </div>
          </template>
          <div v-if="!alerts.length && !offlineCheckedIn.length" class="empty-hint">一切正常</div>
          <div v-for="a in alerts" :key="'a' + a.d.assigned_id" class="alert" @click="store.detailID = a.d.assigned_id">
            <b class="mono">#{{ a.d.assigned_id }}</b>
            <span class="grow ellipsis">{{ a.d.student_name || a.d.hostname }}</span>
            <el-tag v-for="i in a.issues" :key="i" size="small" type="danger" effect="plain">{{ i }}</el-tag>
          </div>
          <div v-for="d in offlineCheckedIn" :key="'o' + d.assigned_id" class="alert" @click="store.detailID = d.assigned_id">
            <b class="mono">#{{ d.assigned_id }}</b>
            <span class="grow ellipsis">{{ d.student_name }} <span class="muted small">{{ d.student_num }}</span></span>
            <el-tag size="small" type="warning" effect="plain">已签到但离线 · {{ timeAgo(d.last_seen) }}</el-tag>
          </div>
        </el-card>

        <el-card shadow="never">
          <template #header><div class="card-title">最近命令<div class="actions"><el-button size="small" link type="primary" @click="go('commands')">去执行</el-button></div></div></template>
          <el-table :data="stats?.recent_commands || []" size="small" max-height="300" empty-text="还没有命令记录" row-class-name="clickable-row" @row-click="openCommand">
            <el-table-column label="时间" width="150"><template #default="{ row }"><span class="small">{{ formatTime(row.created_at) }}</span></template></el-table-column>
            <el-table-column label="目标" width="90"><template #default="{ row }">{{ targetLabel(row) }}</template></el-table-column>
            <el-table-column label="命令" min-width="180"><template #default="{ row }"><span class="mono small ellipsis">{{ row.command }}</span></template></el-table-column>
            <el-table-column label="状态" width="90"><template #default="{ row }"><el-tag size="small" :type="statusType(row.status)">{{ statusLabel(row.status) }}</el-tag></template></el-table-column>
          </el-table>
        </el-card>
      </div>

      <SnapshotsCard />
    </template>
  </div>
</template>

<style scoped>
.quick { display: flex; gap: 8px; flex-wrap: wrap; }
.quick .el-button + .el-button { margin-left: 0; }
.grid2 { display: grid; grid-template-columns: repeat(auto-fit, minmax(420px, 1fr)); gap: 16px; }
.alert { display: flex; align-items: center; gap: 8px; padding: 8px 6px; border-bottom: 1px solid var(--app-border); cursor: pointer; border-radius: 6px; }
.alert:hover { background: var(--el-fill-color-light); }
.rooms { display: grid; grid-template-columns: repeat(auto-fill, minmax(240px, 1fr)); gap: 12px; }
.room { cursor: pointer; }
.room :deep(.el-card__body) { display: flex; flex-direction: column; gap: 8px; }
.room-num { font-size: 28px; font-weight: 600; font-variant-numeric: tabular-nums; }
</style>
