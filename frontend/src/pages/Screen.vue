<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { Search, Monitor, InfoFilled, VideoPause, VideoPlay, Refresh } from "@element-plus/icons-vue";
import { api } from "@/lib/api";
import { store, isCloudAll, run, onlineDevices } from "@/lib/store";
import { checkinLabel, matchDevice, parseIP } from "@/lib/utils";
import { framePool } from "@/lib/framePool";
import ScreenTile from "@/components/ScreenTile.vue";

const enabled = ref<boolean | null>(null);
const query = ref("");
const checkinFilter = ref<"" | "1" | "0">("");
const manualPause = ref(false);
const refreshKey = ref(0);
const size = ref(Number(localStorage.getItem("icpc-screen-size")) || 260);
const interval = ref(Number(localStorage.getItem("icpc-screen-interval")) || 2000);

watch(size, (v) => localStorage.setItem("icpc-screen-size", String(v)));
watch(interval, (v) => localStorage.setItem("icpc-screen-interval", String(v)));

// Cap concurrent frame fetches well below the browser's 6-connection/origin HTTP/1.1 limit
// so dozens or hundreds of machines never block API calls or hang the page.
framePool.setLimit(store.roomID ? 3 : 4);

const shown = computed(() =>
  onlineDevices.value.filter((d) => {
    if (checkinFilter.value === "1" && d.checkin_status !== 1) return false;
    if (checkinFilter.value === "0" && d.checkin_status === 1) return false;
    return matchDevice(d, query.value);
  }),
);
const paused = computed(() => manualPause.value || store.screenID != null || store.terminalID != null || enabled.value === false);

async function loadState() {
  if (isCloudAll.value) return;
  const res = await api<{ enabled: boolean }>("/api/settings/screen").catch(() => null);
  enabled.value = res ? !!res.enabled : null;
}

onMounted(loadState);

async function toggle(next: boolean) {
  const res = await run(() => api<{ enabled: boolean }>("/api/settings/screen", { method: "PUT", json: { enabled: next } }), next ? "已开启屏幕采集" : "已关闭屏幕采集");
  if (res !== undefined) {
    enabled.value = next;
    if (next) refreshKey.value++;
  }
}
</script>

<template>
  <div class="page">
    <el-empty v-if="isCloudAll" description="屏幕画面只在具体机房内查看。请先在左上角选择一个机房。" />
    <template v-else>
      <div class="page-head">
        <div>
          <h1>屏幕墙</h1>
          <p>并发限流轮询截图（支持百台选手机同时预览不卡死），点击任意画面查看实时高清流。</p>
        </div>
        <div class="actions">
          <span class="muted small">选手机屏幕采集</span>
          <el-switch :model-value="!!enabled" :loading="enabled === null" inline-prompt active-text="开启" inactive-text="关闭" @change="(v: string | number | boolean) => toggle(!!v)" />
        </div>
      </div>

      <el-alert v-if="enabled === false" type="warning" show-icon :closable="false" title="屏幕采集当前已关闭，选手机不会推送画面。点击右上角开关即可开启。" />

      <el-card shadow="never" body-style="padding: 12px 16px">
        <div class="row">
          <el-input v-model="query" :prefix-icon="Search" placeholder="搜索姓名 / 学号 / 编号 / 主机名 / IP" clearable style="width: 280px" />
          <el-radio-group v-model="checkinFilter">
            <el-radio-button value="">全部在线</el-radio-button>
            <el-radio-button value="1">已签到</el-radio-button>
            <el-radio-button value="0">未签到</el-radio-button>
          </el-radio-group>
          <span class="spacer" />
          <el-button :icon="manualPause ? VideoPlay : VideoPause" @click="manualPause = !manualPause">{{ manualPause ? "继续轮询" : "暂停" }}</el-button>
          <el-button :icon="Refresh" :disabled="!enabled" @click="refreshKey++">刷新</el-button>
          <span class="muted small">间隔</span>
          <el-select v-model="interval" style="width: 96px">
            <el-option :value="1000" label="1 秒" />
            <el-option :value="2000" label="2 秒" />
            <el-option :value="5000" label="5 秒" />
            <el-option :value="10000" label="10 秒" />
          </el-select>
          <span class="muted small">尺寸</span>
          <el-slider v-model="size" :min="180" :max="520" :step="20" style="width: 120px" :show-tooltip="false" />
          <el-tag type="info">{{ shown.length }} / {{ onlineDevices.length }} 台在线</el-tag>
        </div>
      </el-card>

      <div v-if="shown.length" :key="refreshKey" class="wall" :style="{ gridTemplateColumns: `repeat(auto-fill, minmax(${size}px, 1fr))` }">
        <div v-for="d in shown" :key="d.assigned_id" class="tile" @click="store.screenID = d.assigned_id">
          <ScreenTile :id="d.assigned_id" :room="store.roomID" :interval="interval" :paused="paused" />
          <div class="caption">
            <b class="mono">#{{ d.assigned_id }}</b>
            <div class="grow ellipsis">
              <span>{{ d.student_name || d.hostname }}</span>
              <span class="muted small"> {{ d.student_num || parseIP(d.local_ip) || checkinLabel(d.checkin_status) }}</span>
            </div>
            <el-tooltip content="设备详情">
              <el-button size="small" text circle :icon="InfoFilled" @click.stop="store.detailID = d.assigned_id" />
            </el-tooltip>
            <el-tooltip content="远程终端">
              <el-button size="small" text circle :icon="Monitor" @click.stop="store.terminalID = d.assigned_id" />
            </el-tooltip>
          </div>
        </div>
      </div>
      <el-empty v-else :description="onlineDevices.length ? '没有匹配的在线设备' : '当前没有在线设备'" />
    </template>
  </div>
</template>

<style scoped>
.wall { display: grid; gap: 12px; }
.tile { background: var(--el-bg-color); border: 1px solid var(--app-border); border-radius: 10px; overflow: hidden; cursor: pointer; transition: box-shadow .15s, border-color .15s; }
.tile:hover { border-color: var(--el-color-primary-light-5); box-shadow: var(--el-box-shadow-light); }
.caption { display: flex; align-items: center; gap: 4px; padding: 6px 8px 6px 10px; font-size: 13px; }
.caption .el-button + .el-button { margin-left: 0; }
</style>
