<script setup lang="ts">
import { onMounted, ref } from "vue";
import { Refresh, Delete, Right } from "@element-plus/icons-vue";
import { api } from "@/lib/api";
import { store, isCloud, setRoom, go, refreshRooms, run, confirmAction } from "@/lib/store";
import { timeAgo } from "@/lib/utils";
import type { Room } from "@/lib/types";

const loading = ref(false);
async function refresh() {
  loading.value = true;
  await run(refreshRooms);
  loading.value = false;
}
async function remove(room: Room) {
  if (!(await confirmAction(`删除机房「${room.name}」？云端会通知该机房切换为单机模式；离线机房将在下次上线时收到通知。`, { danger: true, confirmText: "删除" }))) return;
  const ok = await run(() => api("/api/cluster/rooms/" + encodeURIComponent(room.id), { method: "DELETE" }), "已删除机房");
  if (ok === undefined) return;
  if (store.roomID === room.id) setRoom("");
  refreshRooms();
}
onMounted(refresh);
</script>

<template>
  <div class="page">
    <template v-if="!isCloud">
      <div class="page-head"><div><h1>机房连接</h1><p>当前不是云端模式。</p></div></div>
      <el-card shadow="never">
        <el-descriptions :column="1" border>
          <el-descriptions-item label="机房">{{ store.deployment.room_name || "当前局域网" }}</el-descriptions-item>
          <el-descriptions-item label="模式">{{ store.deployment.mode === "relay" ? "机房中转" : "单机" }}</el-descriptions-item>
          <el-descriptions-item label="云端连接">{{ store.connection || "未连接" }}</el-descriptions-item>
        </el-descriptions>
        <div class="row" style="margin-top: 16px">
          <el-button type="primary" @click="go('settings')">配置部署模式</el-button>
          <el-button @click="go('devices')">管理本地设备</el-button>
        </div>
      </el-card>
    </template>
    <template v-else>
      <div class="page-head">
        <div><h1>机房总览</h1><p>进入机房后，设备、命令、分发等操作都作用于该机房。</p></div>
        <div class="actions"><el-button :icon="Refresh" :loading="loading" @click="refresh">刷新</el-button></div>
      </div>
      <div class="grid">
        <el-card v-for="room in store.rooms" :key="room.id" shadow="hover">
          <div class="row">
            <span class="dot" :class="{ on: room.online }" /><b class="name">{{ room.name }}</b>
            <span class="spacer" />
            <el-tag size="small" :type="room.online ? 'success' : 'info'">{{ room.online ? "中转在线" : "中转离线" }}</el-tag>
          </div>
          <div class="num">{{ room.devices.filter((d) => d.connected).length }}<span class="muted"> / {{ room.devices.length }} 台在线</span></div>
          <div class="muted small">已签到 {{ room.devices.filter((d) => d.checkin_status === 1).length }} · 最后上报 {{ timeAgo(room.last_seen) }}<span v-if="room.transfer_phase"> · {{ room.transfer_phase }}</span></div>
          <div class="row foot">
            <el-button type="primary" :icon="Right" @click="setRoom(room.id, 'dashboard')">进入机房</el-button>
            <span class="spacer" />
            <el-button text type="danger" :icon="Delete" @click="remove(room)">删除</el-button>
          </div>
        </el-card>
      </div>
      <el-empty v-if="!store.rooms.length" description="还没有机房连接。请在机房中转的“设置 → 部署”里填写云端地址和相同的连接密钥。" />
    </template>
  </div>
</template>

<style scoped>
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 14px; }
.name { font-size: 16px; }
.num { font-size: 30px; font-weight: 600; margin: 12px 0 4px; font-variant-numeric: tabular-nums; }
.num .muted { font-size: 14px; font-weight: normal; }
.foot { margin-top: 14px; }
</style>
