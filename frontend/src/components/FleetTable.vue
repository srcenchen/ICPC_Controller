<script setup lang="ts">
import { computed, ref } from "vue";
import { Search } from "@element-plus/icons-vue";
import { store, fleetDevices, setRoom, type FleetDevice, type PageId } from "@/lib/store";
import { checkinLabel, checkinType, matchDevice } from "@/lib/utils";

/** Cloud "all rooms" device table. Selection lives in store.fleet. */
const props = withDefaults(defineProps<{ selectable?: boolean; managePage?: PageId; height?: string }>(), { selectable: true, height: "420px" });

const query = ref("");
const room = ref("");
const shown = computed(() =>
  fleetDevices.value.filter((d) => (!room.value || d.room_id === room.value) && (matchDevice(d, query.value) || d.room_name.toLowerCase().includes(query.value.trim().toLowerCase()))),
);
const selected = computed(() => new Set(store.fleet));
const allShownSelected = computed(() => shown.value.length > 0 && shown.value.every((d) => selected.value.has(d.key)));

function toggle(key: string) {
  store.fleet = selected.value.has(key) ? store.fleet.filter((k) => k !== key) : [...store.fleet, key];
}
function selectShown(onlineOnly: boolean) {
  store.fleet = Array.from(new Set([...store.fleet, ...shown.value.filter((d) => !onlineOnly || d.connected).map((d) => d.key)]));
}
function toggleAll() {
  if (allShownSelected.value) {
    const keys = new Set(shown.value.map((d) => d.key));
    store.fleet = store.fleet.filter((k) => !keys.has(k));
  } else selectShown(false);
}
function manage(d: FleetDevice) {
  setRoom(d.room_id, props.managePage || "devices");
  store.detailID = d.assigned_id;
}
</script>

<template>
  <el-card shadow="never">
    <div class="row toolbar">
      <el-select v-model="room" placeholder="全部机房" clearable style="width: 180px">
        <el-option v-for="r in store.rooms" :key="r.id" :label="r.name" :value="r.id" />
      </el-select>
      <el-input v-model="query" :prefix-icon="Search" placeholder="搜索机房 / 编号 / 姓名 / 学号" clearable style="width: 280px" />
      <template v-if="selectable">
        <el-button @click="selectShown(true)">选中筛选内在线</el-button>
        <el-button @click="selectShown(false)">选中筛选结果</el-button>
        <el-button text :disabled="!store.fleet.length" @click="store.fleet = []">清空</el-button>
        <el-tag type="primary" effect="plain">已选 {{ store.fleet.length }} 台</el-tag>
      </template>
    </div>
    <el-table :data="shown" :max-height="height" row-key="key" size="small" empty-text="没有匹配的设备" @row-click="(row: FleetDevice) => selectable && toggle(row.key)">
      <el-table-column v-if="selectable" width="44">
        <template #header><el-checkbox :model-value="allShownSelected" :indeterminate="!allShownSelected && shown.some((d) => selected.has(d.key))" @change="toggleAll" /></template>
        <template #default="{ row }"><el-checkbox :model-value="selected.has(row.key)" @click.stop @change="toggle(row.key)" /></template>
      </el-table-column>
      <el-table-column label="机房" prop="room_name" min-width="110" sortable />
      <el-table-column label="编号" width="80" sortable :sort-by="(row: FleetDevice) => row.assigned_id">
        <template #default="{ row }"><b class="mono">#{{ row.assigned_id }}</b></template>
      </el-table-column>
      <el-table-column label="选手" min-width="150">
        <template #default="{ row }">{{ row.student_name || "—" }} <span class="muted small">{{ row.student_num }}</span></template>
      </el-table-column>
      <el-table-column label="签到" width="90">
        <template #default="{ row }"><el-tag size="small" :type="checkinType(row.checkin_status)">{{ checkinLabel(row.checkin_status) }}</el-tag></template>
      </el-table-column>
      <el-table-column label="状态" width="80">
        <template #default="{ row }"><span class="row nowrap"><span class="dot" :class="{ on: row.connected }" />{{ row.connected ? "在线" : "离线" }}</span></template>
      </el-table-column>
      <el-table-column width="96" align="right">
        <template #default="{ row }"><el-button size="small" link type="primary" @click.stop="manage(row)">进入机房</el-button></template>
      </el-table-column>
    </el-table>
  </el-card>
</template>

<style scoped>
.toolbar { margin-bottom: 12px; }
</style>
