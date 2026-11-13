<script setup lang="ts">
import { computed, ref } from "vue";
import { Search } from "@element-plus/icons-vue";
import type { Device } from "@/lib/types";
import { checkinLabel, matchDevice } from "@/lib/utils";

/**
 * TargetPicker chooses which devices an action applies to.
 * mode "online" = every online device at the moment of sending;
 * mode "picked" = exactly the checked devices.
 */
const props = withDefaults(defineProps<{
  devices: Device[];
  error?: string;
  allowOffline?: boolean;
  height?: string;
}>(), { error: "", allowOffline: true, height: "460px" });

const mode = defineModel<"online" | "picked">("mode", { required: true });
const selected = defineModel<number[]>("selected", { required: true });

const query = ref("");
const filter = ref<"all" | "online" | "checked">("all");
const range = ref("");

const onlineCount = computed(() => props.devices.filter((d) => d.connected).length);
const shown = computed(() =>
  props.devices.filter((d) => {
    if (filter.value === "online" && !d.connected) return false;
    if (filter.value === "checked" && d.checkin_status !== 1) return false;
    return matchDevice(d, query.value);
  }),
);
const selectedSet = computed(() => new Set(selected.value));

function toggle(id: number) {
  mode.value = "picked";
  selected.value = selectedSet.value.has(id) ? selected.value.filter((x) => x !== id) : [...selected.value, id];
}
function setSelection(ids: number[]) {
  mode.value = "picked";
  selected.value = Array.from(new Set(ids)).sort((a, b) => a - b);
}
function addShown(onlineOnly: boolean) {
  setSelection([...selected.value, ...shown.value.filter((d) => !onlineOnly || d.connected).map((d) => d.assigned_id)]);
}
function invert() {
  const ids = shown.value.map((d) => d.assigned_id);
  setSelection([...selected.value.filter((id) => !ids.includes(id)), ...ids.filter((id) => !selectedSet.value.has(id))]);
}

/** "1-20, 25, 30-32" → those device numbers (only ones that exist). */
function applyRange() {
  const wanted = new Set<number>();
  for (const part of range.value.split(/[,，\s]+/).filter(Boolean)) {
    const m = part.match(/^#?(\d+)(?:\s*[-~]\s*#?(\d+))?$/);
    if (!m) continue;
    const a = Number(m[1]);
    const b = m[2] ? Number(m[2]) : a;
    for (let i = Math.min(a, b); i <= Math.max(a, b) && i - Math.min(a, b) < 5000; i++) wanted.add(i);
  }
  const ids = props.devices.map((d) => d.assigned_id).filter((id) => wanted.has(id));
  setSelection([...selected.value, ...ids]);
  range.value = "";
}
</script>

<template>
  <div class="picker">
    <el-segmented
      v-model="mode"
      block
      :options="[
        { label: `全部在线 · ${onlineCount}`, value: 'online' },
        { label: `指定设备 · ${selected.length}`, value: 'picked' },
      ]"
    />
    <p v-if="mode === 'online'" class="hint">发送时所有在线设备都会执行。勾选下方设备可改为只对指定设备执行。</p>

    <el-input v-model="query" :prefix-icon="Search" placeholder="搜索编号 / 姓名 / 学号 / IP" clearable />
    <div class="row filters">
      <el-radio-group v-model="filter" size="small">
        <el-radio-button value="all">全部</el-radio-button>
        <el-radio-button value="online">在线</el-radio-button>
        <el-radio-button value="checked">已签到</el-radio-button>
      </el-radio-group>
      <span class="spacer" />
      <span class="muted small">{{ shown.length }} 台</span>
    </div>
    <div class="row quick">
      <el-button size="small" link type="primary" @click="addShown(true)">选中列表内在线</el-button>
      <el-button size="small" link type="primary" @click="addShown(false)">选中列表全部</el-button>
      <el-button size="small" link type="primary" @click="invert">反选</el-button>
      <el-button size="small" link :disabled="!selected.length" @click="setSelection([])">清空</el-button>
    </div>
    <el-input v-model="range" size="small" placeholder="按编号添加，如 1-20, 25" @keyup.enter="applyRange">
      <template #append><el-button size="small" @click="applyRange">添加</el-button></template>
    </el-input>

    <el-scrollbar :height="height" class="list">
      <div
        v-for="d in shown"
        :key="d.assigned_id"
        class="item"
        :class="{ checked: selectedSet.has(d.assigned_id), offline: !d.connected }"
        @click="(allowOffline || d.connected) && toggle(d.assigned_id)"
      >
        <el-checkbox class="passive" :model-value="selectedSet.has(d.assigned_id)" :disabled="!allowOffline && !d.connected" />
        <span class="dot" :class="{ on: d.connected }" />
        <b class="mono id">#{{ d.assigned_id }}</b>
        <span class="grow ellipsis">
          {{ d.student_name || d.hostname || "未命名" }}
          <span class="muted small">{{ d.student_name ? d.student_num : checkinLabel(d.checkin_status) }}</span>
        </span>
      </div>
      <div v-if="!shown.length" class="empty-hint">{{ error || (devices.length ? "没有匹配的设备" : "还没有设备注册") }}</div>
    </el-scrollbar>
  </div>
</template>

<style scoped>
.picker { display: flex; flex-direction: column; gap: 10px; }
.hint { margin: 0; font-size: 12px; color: var(--el-text-color-secondary); }
.quick { gap: 4px 12px; }
.quick .el-button + .el-button { margin-left: 0; }
.list { border: 1px solid var(--app-border); border-radius: 8px; }
.item { display: flex; align-items: center; gap: 8px; padding: 4px 10px; border-bottom: 1px solid var(--app-border); cursor: pointer; font-size: 13px; }
.item:last-child { border-bottom: none; }
.item:hover { background: var(--el-fill-color-light); }
.item.checked { background: var(--el-color-primary-light-9); }
.item.offline .id { color: var(--el-text-color-placeholder); }
.id { width: 44px; }
.passive { pointer-events: none; height: 24px; }
</style>
