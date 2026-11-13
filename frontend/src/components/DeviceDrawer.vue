<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { Monitor, VideoCamera, Operation, Delete, Tickets, Refresh } from "@element-plus/icons-vue";
import { api } from "@/lib/api";
import { store, deviceByID, actOn, confirmAction, loadDevices, run } from "@/lib/store";
import { checkinLabel, checkinType, formatBytes, formatDisk, formatGPU, formatTime, parseIP, timeAgo } from "@/lib/utils";
import type { Device, DeviceEvent } from "@/lib/types";
import CheckinDialog from "./CheckinDialog.vue";

const open = computed({
  get: () => store.detailID != null,
  set: (value) => { if (!value) store.detailID = null; },
});
const tab = ref("overview");
const detail = ref<Device | null>(null);
const events = ref<DeviceEvent[]>([]);
const loading = ref(false);
const checkinID = ref<number | null>(null);

// Live list data wins for fast-changing fields (status, health, check-in).
const device = computed<Device | null>(() => {
  const live = deviceByID(store.detailID);
  return live ? { ...(detail.value || {}), ...live } as Device : detail.value;
});

async function load(id: number) {
  loading.value = true;
  try {
    const [d, ev] = await Promise.all([
      api<Device>("/api/devices/" + id),
      api<DeviceEvent[]>("/api/devices/" + id + "/events").catch(() => []),
    ]);
    if (store.detailID !== id) return;
    detail.value = d;
    events.value = ev || [];
  } catch {
    detail.value = deviceByID(id) || null;
  } finally {
    loading.value = false;
  }
}

watch(() => store.detailID, (id) => {
  detail.value = null;
  events.value = [];
  tab.value = "overview";
  if (id != null) load(id);
});

function gauge(value?: number) {
  return value == null || value < 0 ? 0 : Math.min(100, Math.round(value));
}
function gaugeStatus(value?: number) {
  if (value == null) return undefined;
  return value >= 90 ? "exception" : value >= 75 ? "warning" : "success";
}
function uptimeText(seconds?: number) {
  if (!seconds) return "—";
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  return (d ? d + " 天 " : "") + (h ? h + " 小时 " : "") + m + " 分钟";
}
function eventLabel(event: string) {
  return ({ online: "上线", offline: "下线", registered: "注册" } as Record<string, string>)[event] || event || "—";
}
function eventType(event: string) {
  return event === "online" ? "success" : event === "offline" ? "danger" : "primary";
}

async function checkout(d: Device) {
  if (!(await confirmAction(`为 #${d.assigned_id} ${d.student_name || ""} 签退？`))) return;
  await run(() => api(`/api/checkin/${d.assigned_id}/checkout`, { method: "POST" }), "已签退");
  loadDevices();
}

async function remove(d: Device) {
  const text = d.connected
    ? `#${d.assigned_id} 当前在线，删除记录后它会重新注册并可能拿到新编号。确定删除？`
    : `删除离线设备 #${d.assigned_id} 的记录？签到与事件记录将一并移除。`;
  if (!(await confirmAction(text, { danger: true, confirmText: "删除" }))) return;
  const ok = await run(() => api("/api/devices/" + d.assigned_id, { method: "DELETE" }), "已删除");
  if (ok !== undefined) {
    store.detailID = null;
    loadDevices();
  }
}
</script>

<template>
  <el-drawer v-model="open" size="min(620px, 100vw)" append-to-body class="device-drawer" :with-header="false">
    <div v-if="device" v-loading="loading && !detail" class="drawer-body">
      <div class="head">
        <div class="id mono">#{{ device.assigned_id }}</div>
        <div class="grow">
          <div class="title">
            {{ device.student_name || "未签到" }}
            <span v-if="device.student_num" class="muted num">{{ device.student_num }}</span>
          </div>
          <div class="muted small">{{ device.hostname }} · {{ parseIP(device.local_ip) || "无 IP" }}</div>
        </div>
        <div class="tags">
          <el-tag :type="device.connected ? 'success' : 'info'" effect="dark" round>{{ device.connected ? "在线" : "离线" }}</el-tag>
          <el-tag :type="checkinType(device.checkin_status)" round>{{ checkinLabel(device.checkin_status) }}</el-tag>
        </div>
      </div>

      <div class="actions">
        <el-button type="primary" :icon="Monitor" :disabled="!device.connected" @click="store.terminalID = device.assigned_id">终端</el-button>
        <el-button :icon="VideoCamera" :disabled="!device.connected" @click="store.screenID = device.assigned_id">看屏幕</el-button>
        <el-button :icon="Operation" @click="actOn('commands', [device.assigned_id])">发命令</el-button>
        <el-button v-if="device.checkin_status !== 1" :icon="Tickets" @click="checkinID = device.assigned_id">签到</el-button>
        <el-button v-else :icon="Tickets" @click="checkout(device)">签退</el-button>
        <span class="spacer" />
        <el-tooltip content="刷新"><el-button :icon="Refresh" circle @click="load(device.assigned_id)" /></el-tooltip>
        <el-tooltip content="删除设备记录"><el-button :icon="Delete" circle type="danger" plain @click="remove(device)" /></el-tooltip>
      </div>

      <el-tabs v-model="tab">
        <el-tab-pane label="概览" name="overview">
          <div class="gauges">
            <div class="gauge">
              <el-progress type="dashboard" :width="96" :percentage="gauge(device.cpu_pct)" :status="gaugeStatus(device.cpu_pct)" />
              <span>CPU</span>
            </div>
            <div class="gauge">
              <el-progress type="dashboard" :width="96" :percentage="gauge(device.mem_pct)" :status="gaugeStatus(device.mem_pct)" />
              <span>内存</span>
            </div>
            <div class="gauge">
              <el-progress type="dashboard" :width="96" :percentage="gauge(device.disk_pct)" :status="gaugeStatus(device.disk_pct)" />
              <span>磁盘</span>
            </div>
          </div>
          <el-descriptions :column="2" border size="small">
            <el-descriptions-item label="选手">{{ device.student_name || "—" }}</el-descriptions-item>
            <el-descriptions-item label="学号">{{ device.student_num || "—" }}</el-descriptions-item>
            <el-descriptions-item label="签到时间">{{ formatTime(device.checkin_time) }}</el-descriptions-item>
            <el-descriptions-item label="签退时间">{{ formatTime(device.checkout_time) }}</el-descriptions-item>
            <el-descriptions-item label="IP"><span class="mono">{{ parseIP(device.local_ip) || "—" }}</span></el-descriptions-item>
            <el-descriptions-item label="MAC"><span class="mono">{{ device.mac_address || "—" }}</span></el-descriptions-item>
            <el-descriptions-item label="温度">{{ device.temp_c && device.temp_c > 0 ? device.temp_c.toFixed(0) + " °C" : "—" }}</el-descriptions-item>
            <el-descriptions-item label="负载">{{ device.load1 != null ? device.load1.toFixed(2) : "—" }}</el-descriptions-item>
            <el-descriptions-item label="运行时长">{{ uptimeText(device.uptime) }}</el-descriptions-item>
            <el-descriptions-item label="最近在线">{{ device.connected ? "现在" : timeAgo(device.last_seen) }}</el-descriptions-item>
            <el-descriptions-item label="客户端版本">{{ device.client_version || "—" }}</el-descriptions-item>
            <el-descriptions-item label="首次注册">{{ formatTime(device.first_seen) }}</el-descriptions-item>
          </el-descriptions>
        </el-tab-pane>

        <el-tab-pane label="硬件与系统" name="hardware">
          <el-descriptions :column="1" border size="small">
            <el-descriptions-item label="主机名">{{ device.hostname }}</el-descriptions-item>
            <el-descriptions-item label="系统">{{ device.os_pretty_name || device.os_name || "—" }}</el-descriptions-item>
            <el-descriptions-item label="内核">{{ device.kernel_release || "—" }} {{ device.kernel_arch }}</el-descriptions-item>
            <el-descriptions-item label="CPU">
              {{ device.cpu_model || "—" }}
              <span v-if="device.cpu_logical_cores" class="muted">（{{ device.cpu_physical_cores }} 核 / {{ device.cpu_logical_cores }} 线程）</span>
            </el-descriptions-item>
            <el-descriptions-item label="内存">{{ formatBytes(device.memory_used) }} / {{ formatBytes(device.memory_total) }}</el-descriptions-item>
            <el-descriptions-item label="显卡">{{ formatGPU(device.gpu_info) || "—" }}</el-descriptions-item>
            <el-descriptions-item label="磁盘">{{ formatDisk(device.disk_info) || "—" }}</el-descriptions-item>
            <el-descriptions-item label="桌面">{{ device.de_name || "—" }}</el-descriptions-item>
            <el-descriptions-item label="Shell">{{ device.shell || "—" }}</el-descriptions-item>
            <el-descriptions-item label="用户">{{ device.username || "—" }}</el-descriptions-item>
          </el-descriptions>
        </el-tab-pane>

        <el-tab-pane :label="`上下线记录 (${events.length})`" name="events">
          <el-timeline v-if="events.length" class="timeline">
            <el-timeline-item v-for="event in events" :key="event.id" :timestamp="formatTime(event.at)" :type="eventType(event.event)" placement="top">
              <b>{{ eventLabel(event.event) }}</b>
              <span v-if="event.detail" class="muted small"> · {{ event.detail }}</span>
            </el-timeline-item>
          </el-timeline>
          <el-empty v-else description="没有记录" :image-size="72" />
        </el-tab-pane>
      </el-tabs>
    </div>
    <el-empty v-else-if="!loading" description="设备不存在或已被删除" />
    <CheckinDialog :id="checkinID" @close="checkinID = null" />
  </el-drawer>
</template>

<style scoped>
.drawer-body { display: flex; flex-direction: column; gap: 14px; }
.head { display: flex; align-items: center; gap: 14px; }
.id { font-size: 26px; font-weight: 700; color: var(--el-color-primary); min-width: 64px; }
.title { font-size: 18px; font-weight: 600; }
.num { font-size: 14px; font-weight: normal; margin-left: 6px; }
.tags { display: flex; flex-direction: column; gap: 6px; align-items: flex-end; }
.actions { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; }
.actions .el-button + .el-button { margin-left: 0; }
.gauges { display: flex; justify-content: space-around; margin: 4px 0 16px; }
.gauge { display: flex; flex-direction: column; align-items: center; font-size: 12px; color: var(--el-text-color-secondary); }
.timeline { padding: 8px 4px; max-height: 60vh; overflow: auto; }
</style>
