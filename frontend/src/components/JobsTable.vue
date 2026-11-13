<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from "vue";
import { Refresh } from "@element-plus/icons-vue";
import { api } from "@/lib/api";
import { store, run } from "@/lib/store";
import { formatReceipt, formatTime, statusLabel, statusType } from "@/lib/utils";
import type { Job } from "@/lib/types";

/** Cloud job queue: one row per room delivery. */
const jobs = ref<Job[]>([]);
const loading = ref(false);
async function load() {
  loading.value = true;
  try {
    jobs.value = (await api<Job[]>("/api/cluster/jobs")) || [];
  } catch {
    /* ignore */
  } finally {
    loading.value = false;
  }
}
function roomLabel(id: string) {
  return store.rooms.find((r) => r.id === id)?.name || id.slice(0, 8);
}
let timer = 0;
onMounted(() => {
  load();
  timer = window.setInterval(() => document.visibilityState === "visible" && load(), 5000);
});
onBeforeUnmount(() => clearInterval(timer));
defineExpose({ load });
</script>

<template>
  <el-card shadow="never">
    <template #header>
      <div class="card-title">跨机房任务回执
        <span class="muted small">“已接收”只表示中转收到，执行结果请进入机房查看</span>
        <div class="actions"><el-button size="small" :icon="Refresh" :loading="loading" @click="load">刷新</el-button></div>
      </div>
    </template>
    <el-table :data="jobs.slice(0, 50)" size="small" max-height="320" empty-text="还没有跨机房任务">
      <el-table-column label="时间" width="170"><template #default="{ row }">{{ formatTime(row.created_at) }}</template></el-table-column>
      <el-table-column label="机房" width="140"><template #default="{ row }">{{ roomLabel(row.room_id) }}</template></el-table-column>
      <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag size="small" :type="statusType(row.status)">{{ statusLabel(row.status) }}</el-tag></template></el-table-column>
      <el-table-column label="回执" min-width="240"><template #default="{ row }">{{ formatReceipt(row.response) }}</template></el-table-column>
      <el-table-column width="80" align="right">
        <template #default="{ row }">
          <el-button v-if="row.status === 'queued'" size="small" link type="danger" @click="run(() => api('/api/cluster/jobs/' + row.id, { method: 'DELETE' }), '已取消').then(load)">取消</el-button>
        </template>
      </el-table-column>
    </el-table>
  </el-card>
</template>
