<script setup lang="ts">
import { onMounted, ref } from "vue";
import { Plus, Delete, Lock, Unlock } from "@element-plus/icons-vue";
import { api } from "@/lib/api";
import { store, isCloudAll, run, toast, confirmAction, takePendingTargets, queueFleet, onlineDevices, toastError } from "@/lib/store";
import type { NetRule } from "@/lib/types";
import TargetPicker from "@/components/TargetPicker.vue";
import FleetTable from "@/components/FleetTable.vue";
import JobsTable from "@/components/JobsTable.vue";

const TYPES = [
  { value: "DOMAIN-SUFFIX", label: "域名后缀", hint: "acm.example.com 及其所有子域名" },
  { value: "DOMAIN-KEYWORD", label: "包含关键字", hint: "域名中包含该词即放行" },
  { value: "DOMAIN", label: "完整域名", hint: "只放行这一个域名" },
];

const rules = ref<NetRule[]>([]);
const saved = ref("");
const mode = ref<"online" | "picked">("online");
const picked = ref<number[]>([]);
const bulk = ref("");
const showBulk = ref(false);
const jobs = ref<{ load: () => void } | null>(null);

function snapshot(list: NetRule[]) {
  return JSON.stringify(list.map((r) => ({ type: r.type, value: r.value.trim() })).filter((r) => r.value));
}

async function load() {
  rules.value = (await api<NetRule[]>("/api/network/rules").catch((err) => (toastError(err), []))) || [];
  saved.value = snapshot(rules.value);
}

async function save(silent = false) {
  const clean = rules.value.map((r) => ({ type: r.type, value: r.value.trim() })).filter((r) => r.value);
  const result = await run(() => api<NetRule[]>("/api/network/rules", { method: "PUT", json: clean }), silent ? undefined : "规则已保存");
  if (result) {
    rules.value = result;
    saved.value = snapshot(result);
  }
  return result !== undefined;
}

function addBulk() {
  const lines = bulk.value.split(/[\n,，\s]+/).map((s) => s.trim().replace(/^https?:\/\//, "").replace(/\/.*$/, "")).filter(Boolean);
  const existing = new Set(rules.value.map((r) => r.value));
  lines.forEach((value) => !existing.has(value) && rules.value.push({ type: "DOMAIN-SUFFIX", value }));
  bulk.value = "";
  showBulk.value = false;
}

async function act(kind: "apply" | "remove") {
  if (kind === "apply" && snapshot(rules.value) !== saved.value && !(await save(true))) return;
  if (kind === "apply" && !rules.value.length && !(await confirmAction("当前没有任何规则，应用后选手机几乎无法访问任何网站。确定继续？", { danger: true }))) return;
  if (isCloudAll.value) {
    if (!store.fleet.length) return toast("请先勾选设备", "warning");
    if (!(await confirmAction(kind === "apply" ? `把白名单应用到 ${store.fleet.length} 台设备？` : `解除 ${store.fleet.length} 台设备的网络限制？`))) return;
    await run(() => queueFleet(store.fleet, kind === "apply" ? "network_apply" : "network_remove", kind === "apply" ? { rules: rules.value } : {}), "已加入队列");
    return jobs.value?.load();
  }
  const ids = mode.value === "online" ? onlineDevices.value.map((d) => d.assigned_id) : picked.value;
  if (!ids.length) return toast(mode.value === "online" ? "当前没有在线设备" : "请先勾选设备", "warning");
  const target = mode.value === "online" ? `全部在线设备（${ids.length} 台）` : `${ids.length} 台设备`;
  if (!(await confirmAction(kind === "apply" ? `对 ${target} 启用网络白名单？` : `解除 ${target} 的网络限制？`, { danger: kind === "apply" }))) return;
  const body = mode.value === "online" ? { target_type: "broadcast" } : { target_type: "list", target_ids: ids };
  await run(() => api(`/api/network/${kind}`, { json: body }), kind === "apply" ? `已对 ${target} 应用白名单` : `已解除 ${target} 的限制`);
}

onMounted(() => {
  const ids = takePendingTargets();
  if (ids.length) {
    mode.value = "picked";
    picked.value = ids;
  }
  load();
});
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div><h1>网络管控</h1><p>白名单模式：只有匹配规则的域名可以访问，其余全部拦截。修改规则后点击“应用白名单”才会生效到选手机。</p></div>
    </div>
    <FleetTable v-if="isCloudAll" height="300px" />
    <div :class="isCloudAll ? 'stack' : 'workspace'">
      <el-card v-if="!isCloudAll" shadow="never">
        <template #header><div class="card-title">目标设备</div></template>
        <TargetPicker v-model:mode="mode" v-model:selected="picked" :devices="store.devices" height="calc(100vh - 470px)" />
      </el-card>
      <div class="stack">
        <el-card shadow="never">
          <template #header>
            <div class="card-title">白名单规则 <el-tag size="small" type="info">{{ rules.length }} 条</el-tag>
              <el-tag v-if="snapshot(rules) !== saved" size="small" type="warning">未保存</el-tag>
              <div class="actions">
                <el-button size="small" @click="showBulk = !showBulk">批量添加</el-button>
                <el-button size="small" type="primary" :disabled="snapshot(rules) === saved" @click="save()">保存规则</el-button>
              </div>
            </div>
          </template>
          <div v-if="showBulk" class="bulk">
            <el-input v-model="bulk" type="textarea" :rows="4" placeholder="每行一个域名，例如：&#10;acm.example.com&#10;docs.oracle.com" />
            <el-button type="primary" size="small" :disabled="!bulk.trim()" @click="addBulk">添加为“域名后缀”</el-button>
          </div>
          <div v-for="(rule, index) in rules" :key="index" class="rule">
            <el-select v-model="rule.type" style="width: 130px">
              <el-option v-for="t in TYPES" :key="t.value" :label="t.label" :value="t.value"><span>{{ t.label }}</span> <span class="muted small">{{ t.hint }}</span></el-option>
            </el-select>
            <el-input v-model="rule.value" class="mono" placeholder="例如 acm.example.com" />
            <el-button :icon="Delete" circle text type="danger" @click="rules.splice(index, 1)" />
          </div>
          <el-empty v-if="!rules.length" description="还没有规则" :image-size="60" />
          <el-button :icon="Plus" class="add" @click="rules.push({ type: 'DOMAIN-SUFFIX', value: '' })">添加规则</el-button>
        </el-card>
        <el-card shadow="never">
          <div class="row">
            <el-button type="danger" size="large" :icon="Lock" @click="act('apply')">应用白名单</el-button>
            <el-button size="large" :icon="Unlock" @click="act('remove')">解除限制</el-button>
            <span class="muted small">应用前会自动保存规则</span>
          </div>
        </el-card>
        <JobsTable v-if="isCloudAll" ref="jobs" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.rule { display: flex; gap: 8px; margin-bottom: 8px; align-items: center; }
.bulk { display: flex; flex-direction: column; gap: 8px; align-items: flex-start; margin-bottom: 12px; }
.add { width: 100%; border-style: dashed; }
</style>
