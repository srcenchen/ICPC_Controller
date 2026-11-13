<script setup lang="ts">
import { computed } from "vue";
import type { DistributeTask } from "@/lib/types";

export type { DistributeTask };
const props = defineProps<{ task: DistributeTask }>();

const devices = computed(() => Object.values(props.task.progresses || {}));
const completed = computed(() => devices.value.filter((d) => d.status === "completed").length);
const issues = computed(() => devices.value.filter((d) => ["failed", "stalled", "cancelled"].includes(d.status)).length);
const average = computed(() =>
  devices.value.length ? devices.value.reduce((sum, d) => sum + Math.max(0, Math.min(100, d.percentage || 0)), 0) / devices.value.length : 0,
);
</script>

<template>
  <div class="dp">
    <div class="row">
      <b class="ellipsis grow">{{ task.active_file || "准备中" }}</b>
      <span v-if="task.files?.length" class="muted small">第 {{ (task.active_idx ?? 0) + 1 }} / {{ task.files.length }} 个文件</span>
    </div>
    <el-progress :percentage="Number(average.toFixed(1))" :stroke-width="10" :status="issues ? 'warning' : completed === devices.length && devices.length ? 'success' : undefined" />
    <div class="muted small">完成 {{ completed }} / {{ devices.length }} 台<span v-if="issues" class="warn"> · 待处理 {{ issues }} 台</span></div>
  </div>
</template>

<style scoped>
.dp { display: flex; flex-direction: column; gap: 6px; }
.warn { color: var(--el-color-warning); }
</style>
