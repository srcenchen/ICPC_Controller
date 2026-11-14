<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import {
  Plus, Delete, CopyDocument, ArrowUp, ArrowDown, View, VideoPlay, VideoPause, RefreshRight, Upload, Check,
} from "@element-plus/icons-vue";
import { api } from "@/lib/api";
import { store, isCloud, go, run, toast, confirmAction } from "@/lib/store";
import type { BroadcastConfig, BroadcastFont, BroadcastItem, BroadcastPage, Room } from "@/lib/types";

type ModeId = "before" | "contesting" | "after";
const MODES: { label: string; value: ModeId }[] = [
  { label: "赛前", value: "before" },
  { label: "赛中", value: "contesting" },
  { label: "赛后", value: "after" },
];
const ITEM_TYPES = [
  { key: "text", label: "文字" },
  { key: "image", label: "图片" },
  { key: "clock", label: "时钟" },
  { key: "countdown", label: "倒计时" },
  { key: "scrolling_notice", label: "滚动通知" },
] as const;
const ITEM_LABELS: Record<string, string> = Object.fromEntries(ITEM_TYPES.map((x) => [x.key, x.label]));
function itemLabel(t: string) {
  return ITEM_LABELS[t] || t;
}
function openPreview() {
  window.open("/broadcast/" + mode.value, "_blank");
}

const mode = ref<ModeId>("before");
const pages = ref<BroadcastPage[]>([]);
const pageID = ref<number | null>(null);
const itemID = ref<number | null>(null);
const fonts = ref<BroadcastFont[]>([]);
const config = ref<BroadcastConfig | null>(null);
const targetScope = ref<"all" | "custom">(store.roomID ? "custom" : "all");
const pickedRooms = ref<string[]>(store.roomID ? [store.roomID] : []);
const dirty = ref(false);

const pickedSet = computed(() => new Set(pickedRooms.value));
const validPickedRooms = computed(() => {
  const valid = new Set(store.rooms.map((r) => r.id));
  return pickedRooms.value.filter((id) => valid.has(id));
});
const effectiveTargets = computed(() => {
  if (!store.rooms.length) return [];
  if (store.rooms.length === 1 || targetScope.value === "all") {
    return store.rooms.map((r) => r.id);
  }
  return validPickedRooms.value;
});
const offlineRooms = computed(() => store.rooms.filter((r) => !r.online));
const onlineRoomCount = computed(() => store.rooms.length - offlineRooms.value.length);
const offlineSummary = computed(() => {
  const names = offlineRooms.value.map((r) => r.name);
  if (!names.length) return "";
  return names.length <= 2 ? `${names.join("、")} 离线` : `${names.slice(0, 2).join("、")} 等 ${names.length} 个离线`;
});
const scopeOptions = computed(() => [
  { label: `全部机房 · ${store.rooms.length}`, value: "all" },
  { label: validPickedRooms.value.length ? `指定机房 · ${validPickedRooms.value.length}` : "指定机房", value: "custom" },
]);

function roomOnlineDevices(r: Room) {
  return (r.devices || []).filter((d) => d.connected).length;
}
function toggleRoom(id: string) {
  targetScope.value = "custom";
  pickedRooms.value = pickedSet.value.has(id) ? pickedRooms.value.filter((x) => x !== id) : [...pickedRooms.value, id];
}
function selectOnlineRooms() {
  targetScope.value = "custom";
  pickedRooms.value = store.rooms.filter((r) => r.online).map((r) => r.id);
}
function invertRooms() {
  targetScope.value = "custom";
  const cur = new Set(validPickedRooms.value);
  pickedRooms.value = store.rooms.map((r) => r.id).filter((id) => !cur.has(id));
}
const stageRef = ref<HTMLElement | null>(null);
const boxes = ref<Record<number, { pos_x: number; pos_y: number; width: number; height: number }>>({});

const pageForm = ref({ title: "", durationSec: 10, bg_color: "#000000", transition: "fade" });
const draft = ref<BroadcastItem | null>(null);
const countdownDate = ref<Date | null>(null);
const baseURL = ref("");

const currentPage = computed(() => pages.value.find((p) => p.id === pageID.value) || null);
const currentItem = computed(() => currentPage.value?.items?.find((it) => it.id === itemID.value) || null);

function syncBoxes(p: BroadcastPage | null) {
  const next: Record<number, { pos_x: number; pos_y: number; width: number; height: number }> = {};
  (p?.items || []).forEach((it) => {
    next[it.id] = { pos_x: it.pos_x, pos_y: it.pos_y, width: it.width, height: it.height };
  });
  boxes.value = next;
}

watch(currentPage, (p) => {
  syncBoxes(p);
  if (!p) return;
  pageForm.value = {
    title: p.title || "",
    durationSec: Math.max(1, Math.round((p.duration_ms || 10000) / 1000)),
    bg_color: p.bg_color || "#000000",
    transition: p.transition || "fade",
  };
});

watch(currentItem, (it) => {
  draft.value = it ? { ...it } : null;
  dirty.value = false;
});

async function load(keepPage = pageID.value, keepItem = itemID.value) {
  const [pageData, fontList, cfg] = await Promise.all([
    api<{ pages: BroadcastPage[] }>("/api/broadcast/pages?mode=" + mode.value),
    api<BroadcastFont[]>("/api/broadcast/fonts").catch(() => []),
    api<BroadcastConfig>("/api/broadcast/config"),
  ]);
  const list = pageData.pages || [];
  pages.value = list;
  const nextPageID = list.some((p) => p.id === keepPage) ? keepPage : list[0]?.id ?? null;
  pageID.value = nextPageID;
  const nextPage = list.find((p) => p.id === nextPageID) || null;
  itemID.value = nextPage?.items?.some((i) => i.id === keepItem) ? keepItem : null;
  syncBoxes(nextPage);
  fonts.value = fontList || [];
  config.value = cfg;
  countdownDate.value = cfg.countdown_target ? new Date(cfg.countdown_target) : null;
  baseURL.value = cfg.base_url || "";
  dirty.value = false;
}

onMounted(() => {
  load(null, null).catch((e) => toast(e.message, "error"));
});

async function switchMode(next: ModeId) {
  if (next === mode.value) return;
  if (dirty.value && !(await confirmAction("当前元素有未保存的修改，切换阶段将丢弃修改。继续？"))) return;
  mode.value = next;
  itemID.value = null;
  await run(() => load(null, null));
}

async function createPage() {
  const created = await run(() => api<BroadcastPage>("/api/broadcast/pages", {
    json: { mode: mode.value, title: `页面 ${pages.value.length + 1}`, sort_order: pages.value.length, duration_ms: 10000, bg_color: "#000000", transition: "fade" },
  }), "已新建页面");
  if (created) load(created.id, null);
}

async function savePage() {
  if (!currentPage.value) return;
  const p = currentPage.value;
  const f = pageForm.value;
  await run(() => api("/api/broadcast/pages/" + p.id, {
    method: "PUT",
    json: { ...p, title: f.title.trim() || "未命名", duration_ms: Math.max(1, f.durationSec) * 1000, bg_color: f.bg_color || "#000000", transition: f.transition },
  }), "页面属性已保存");
  load(p.id, itemID.value);
}

async function duplicatePage(id: number) {
  const res = await run(() => api<{ id: number }>(`/api/broadcast/pages/${id}/duplicate`, { method: "POST" }), "已复制页面");
  if (res) load(res.id, null);
}

async function deletePage(id: number) {
  if (!(await confirmAction("删除这个页面及其所有图层？", { danger: true, confirmText: "删除" }))) return;
  await run(() => api("/api/broadcast/pages/" + id, { method: "DELETE" }), "已删除页面");
  load(null, null);
}

async function movePage(index: number, delta: number) {
  const next = index + delta;
  if (next < 0 || next >= pages.value.length) return;
  const copy = pages.value.slice();
  const [moved] = copy.splice(index, 1);
  copy.splice(next, 0, moved);
  await run(() => api("/api/broadcast/pages/reorder", {
    method: "PUT",
    json: copy.map((entry, idx) => ({ id: entry.id, sort_order: idx })),
  }));
  load(pageID.value, itemID.value);
}

async function pickAndUploadImage(): Promise<string> {
  const file = await new Promise<File | null>((resolve) => {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = "image/*";
    input.onchange = () => resolve(input.files?.[0] || null);
    input.click();
  });
  if (!file) return "";
  const body = new FormData();
  body.append("file", file);
  const res = await run(() => api<{ url: string }>("/api/broadcast/images/upload", { method: "POST", body }), "图片已上传");
  return res?.url || "";
}

async function addItem(type: string) {
  if (!currentPage.value) return toast("请先新建或选择一个页面", "warning");
  let content = type === "text" ? "双击或右侧编辑文字" : type === "scrolling_notice" ? "比赛通知内容" : "";
  if (type === "image") {
    content = await pickAndUploadImage();
    if (!content) return;
  }
  const created = await run(() => api<BroadcastItem>("/api/broadcast/items", {
    json: {
      page_id: currentPage.value!.id,
      item_type: type,
      content,
      pos_x: 15,
      pos_y: 15,
      width: type === "scrolling_notice" ? 70 : 36,
      height: type === "image" ? 36 : 16,
      font_size: "4",
      font_color: "#ffffff",
      font_weight: "600",
      text_align: "center",
      bg_color: "transparent",
      border_radius: "0",
      animation: "",
      z_index: (currentPage.value!.items?.length || 0) + 10,
      extra_json: "{}",
    },
  }));
  if (created) load(currentPage.value.id, created.id);
}

async function replaceImage() {
  if (!draft.value) return;
  const url = await pickAndUploadImage();
  if (!url) return;
  draft.value.content = url;
  dirty.value = true;
}

async function saveItem() {
  if (!draft.value) return;
  const d = draft.value;
  await run(() => api("/api/broadcast/items/" + d.id, { method: "PUT", json: d }), "元素已保存");
  dirty.value = false;
  load(pageID.value, d.id);
}

async function deleteItem() {
  if (!draft.value || !(await confirmAction("删除当前选中元素？", { danger: true, confirmText: "删除" }))) return;
  const id = draft.value.id;
  await run(() => api("/api/broadcast/items/" + id, { method: "DELETE" }), "已删除元素");
  load(pageID.value, null);
}

function clamp(v: number, max = 96) {
  return Math.max(0, Math.min(max, Math.round(v * 100) / 100));
}

function startDrag(event: PointerEvent, item: BroadcastItem, resize: boolean) {
  event.stopPropagation();
  event.preventDefault();
  if (itemID.value !== item.id) itemID.value = item.id;
  const bounds = stageRef.value?.getBoundingClientRect();
  if (!bounds) return;
  const initial = boxes.value[item.id] || { pos_x: item.pos_x, pos_y: item.pos_y, width: item.width, height: item.height };
  const startX = event.clientX;
  const startY = event.clientY;
  let latest = { ...initial };
  let moved = false;

  const onMove = (ev: PointerEvent) => {
    const dx = ((ev.clientX - startX) / bounds.width) * 100;
    const dy = ((ev.clientY - startY) / bounds.height) * 100;
    if (Math.abs(dx) > 0.1 || Math.abs(dy) > 0.1) moved = true;
    latest = resize
      ? { pos_x: initial.pos_x, pos_y: initial.pos_y, width: Math.max(4, Math.min(100 - initial.pos_x, Math.round((initial.width + dx) * 100) / 100)), height: Math.max(4, Math.min(100 - initial.pos_y, Math.round((initial.height + dy) * 100) / 100)) }
      : { pos_x: clamp(initial.pos_x + dx, 100 - initial.width), pos_y: clamp(initial.pos_y + dy, 100 - initial.height), width: initial.width, height: initial.height };
    boxes.value[item.id] = latest;
    if (draft.value?.id === item.id) Object.assign(draft.value, latest);
  };
  const onUp = async () => {
    window.removeEventListener("pointermove", onMove);
    window.removeEventListener("pointerup", onUp);
    if (!moved) return;
    await run(() => api(`/api/broadcast/items/${item.id}/position`, { method: "PATCH", json: latest }));
    if (currentPage.value?.items) {
      const target = currentPage.value.items.find((x) => x.id === item.id);
      if (target) Object.assign(target, latest);
    }
  };
  window.addEventListener("pointermove", onMove);
  window.addEventListener("pointerup", onUp);
}

async function uploadFont() {
  const file = await new Promise<File | null>((resolve) => {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = ".ttf,.woff,.woff2";
    input.onchange = () => resolve(input.files?.[0] || null);
    input.click();
  });
  if (!file) return;
  const body = new FormData();
  body.append("file", file);
  await run(() => api("/api/broadcast/fonts", { method: "POST", body }), "字体已上传");
  load();
}

async function removeFont(font: BroadcastFont) {
  if (!(await confirmAction(`删除字体「${font.name}」？`, { danger: true }))) return;
  await run(() => api("/api/broadcast/fonts/" + font.id, { method: "DELETE" }), "已删除字体");
  load();
}

async function saveGlobalConfig(patch: Record<string, unknown>, msg = "配置已保存") {
  await run(() => api("/api/broadcast/config", { method: "PUT", json: patch }), msg);
  load();
}

async function localPush(start: boolean) {
  if (dirty.value) return toast("请先保存右侧元素的修改", "warning");
  if (start) {
    await run(async () => {
      await api("/api/broadcast/config", { method: "PUT", json: { sync_reset: mode.value, pushed_state: mode.value } });
      const base = (baseURL.value.trim() || config.value?.base_url || "http://icpc-server.local:8080").replace(/\/$/, "");
      await api("/api/commands", { json: { target_type: "broadcast", command: `full-firefox kill; full-firefox '${base}/broadcast/${mode.value}'` } });
    }, "已向全部在线选手机推送广播");
  } else {
    if (!(await confirmAction("关闭本机房所有在线选手机上的广播全屏窗口？"))) return;
    await run(async () => {
      await api("/api/broadcast/config", { method: "PUT", json: { pushed_state: "" } });
      await api("/api/commands", { json: { target_type: "broadcast", command: "full-firefox kill" } });
    }, "已关闭选手机广播");
  }
  load();
}

async function cloudPublish(action: "sync" | "start" | "stop" | "reset") {
  if (dirty.value) return toast("请先保存右侧元素的修改", "warning");
  if (!store.rooms.length) return toast("当前暂无已接入机房", "warning");
  const ids = effectiveTargets.value;
  if (!ids.length) return toast("请先选择目标机房，或切换为「全部机房」", "warning");
  const labels = {
    sync: `同步「${modeName(mode.value)}」内容与素材`,
    start: `同步素材并推送「${modeName(mode.value)}」广播`,
    stop: "关闭选手机广播",
    reset: `复位「${modeName(mode.value)}」轮播顺序`,
  };
  const names = ids.map((id) => store.rooms.find((r) => r.id === id)?.name || id);
  const scopeText =
    store.rooms.length > 1 && targetScope.value === "all"
      ? `全部 ${ids.length} 个机房`
      : ids.length === 1
        ? `机房「${names[0]}」`
        : `已选 ${ids.length} 个机房（${names.length <= 3 ? names.join("、") : names.slice(0, 3).join("、") + " 等"}）`;
  const extra = action === "sync" || action === "start" ? "未变化的素材会自动跳过重复下载。" : "";
  if (!(await confirmAction(`对${scopeText}执行「${labels[action]}」？${extra}`, { danger: action === "stop", confirmText: action === "stop" ? "关闭广播" : "确定" }))) return;
  const res = await run(() => api<{ revision: number }>("/api/cluster/broadcast", { json: { room_ids: ids, action, mode: mode.value } }));
  if (res) toast(`广播版本 #${res.revision} 已加入投递队列`, "success");
}

function itemText(it: BroadcastItem) {
  if (it.item_type === "clock") return "12:00:00";
  if (it.item_type === "countdown") return "04:59:59";
  return it.content || ITEM_LABELS[it.item_type] || it.item_type;
}
function modeName(m?: string) {
  return MODES.find((x) => x.value === m)?.label || m || "";
}
</script>

<template>
  <div class="page broadcast-page">
    <div class="row topbar">
      <el-segmented :model-value="mode" :options="MODES" @change="(v: string) => switchMode(v as ModeId)" />
      <el-button :icon="View" @click="openPreview">预览当前阶段</el-button>
      <el-tag v-if="config?.pushed_state" type="success">当前推送：{{ modeName(config.pushed_state) }}</el-tag>
      <el-tag v-if="dirty" type="warning">元素有未保存修改</el-tag>
      <span class="spacer" />
      <template v-if="isCloud">
        <el-button :disabled="!store.rooms.length" @click="cloudPublish('sync')">仅同步素材</el-button>
        <el-button type="primary" :icon="VideoPlay" :disabled="!store.rooms.length" @click="cloudPublish('start')">同步并推送</el-button>
        <el-button :icon="RefreshRight" :disabled="!store.rooms.length" @click="cloudPublish('reset')">复位轮播</el-button>
        <el-button type="danger" plain :icon="VideoPause" :disabled="!store.rooms.length" @click="cloudPublish('stop')">关闭广播</el-button>
      </template>
      <template v-else>
        <el-button type="primary" :icon="VideoPlay" @click="localPush(true)">推送到选手机</el-button>
        <el-button :icon="RefreshRight" @click="saveGlobalConfig({ sync_reset: mode }, '轮播已复位')">复位轮播</el-button>
        <el-button type="danger" plain :icon="VideoPause" @click="localPush(false)">关闭广播</el-button>
      </template>
    </div>

    <el-card v-if="isCloud" shadow="never" body-style="padding: 10px 14px">
      <div v-if="!store.rooms.length" class="row">
        <span class="dot" />
        <span class="muted small">暂无已接入机房 — 可先在云端编排广播内容，待机房中转连接后再同步推送</span>
        <span class="spacer" />
        <el-button size="small" link type="primary" @click="go('rooms')">前往机房总览</el-button>
      </div>

      <div v-else-if="store.rooms.length === 1" class="row">
        <span class="muted small">发布目标机房：</span>
        <span class="room-pill static">
          <span class="dot" :class="{ on: store.rooms[0].online }" />
          <span class="room-pill-name">{{ store.rooms[0].name }}</span>
          <span class="room-pill-meta">
            {{ store.rooms[0].online ? `${roomOnlineDevices(store.rooms[0])}/${store.rooms[0].devices.length} 台在线` : "中转离线（上线后自动投递）" }}
          </span>
        </span>
        <span class="spacer" />
        <span class="muted small">不会覆盖机房本地配置的局域网推送地址</span>
      </div>

      <div v-else class="row target-row">
        <span class="muted small">发布范围：</span>
        <el-segmented v-model="targetScope" size="small" :options="scopeOptions" />
        <template v-if="targetScope === 'all'">
          <span class="muted small status-summary">
            <span class="dot" :class="offlineRooms.length ? 'warn' : 'on'" />
            <template v-if="!offlineRooms.length">{{ store.rooms.length }} 个机房中转均在线</template>
            <template v-else>{{ onlineRoomCount }}/{{ store.rooms.length }} 个中转在线（{{ offlineSummary }}，恢复后自动投递）</template>
          </span>
        </template>
        <template v-else>
          <div class="room-pills">
            <button
              v-for="r in store.rooms"
              :key="r.id"
              type="button"
              class="room-pill"
              :class="{ active: pickedSet.has(r.id) }"
              @click="toggleRoom(r.id)"
            >
              <el-icon v-if="pickedSet.has(r.id)" class="pill-check"><Check /></el-icon>
              <span class="dot" :class="{ on: r.online }" />
              <span class="room-pill-name">{{ r.name }}</span>
              <span class="room-pill-meta">{{ r.online ? `${roomOnlineDevices(r)}/${r.devices.length}` : "离线" }}</span>
            </button>
          </div>
          <el-button v-if="onlineRoomCount > 0 && onlineRoomCount < store.rooms.length" size="small" link type="primary" @click="selectOnlineRooms">仅选在线</el-button>
          <el-button v-if="store.rooms.length >= 3" size="small" link type="primary" @click="invertRooms">反选</el-button>
          <el-button v-if="validPickedRooms.length" size="small" link @click="pickedRooms = []">清空</el-button>
        </template>
        <span class="spacer" />
        <span class="muted small">不会覆盖各机房本地配置的局域网推送地址</span>
      </div>
    </el-card>

    <div class="studio">
      <!-- Left: Pages, Layers, Global Settings -->
      <div class="stack side">
        <el-card shadow="never" body-style="padding: 12px">
          <div class="card-title" style="margin-bottom: 8px">
            轮播页面 <el-tag size="small" type="info">{{ pages.length }}</el-tag>
            <div class="actions"><el-button size="small" type="primary" :icon="Plus" @click="createPage">新建</el-button></div>
          </div>
          <div v-for="(p, idx) in pages" :key="p.id" class="list-row" :class="{ active: p.id === pageID }" @click="(pageID = p.id), (itemID = null)">
            <span class="idx mono">{{ idx + 1 }}</span>
            <span class="grow ellipsis">{{ p.title || "未命名" }}</span>
            <span class="muted small">{{ Math.round((p.duration_ms || 10000) / 1000) }}s</span>
            <el-button size="small" text circle :icon="ArrowUp" :disabled="idx === 0" @click.stop="movePage(idx, -1)" />
            <el-button size="small" text circle :icon="ArrowDown" :disabled="idx === pages.length - 1" @click.stop="movePage(idx, 1)" />
          </div>
          <div v-if="!pages.length" class="empty-hint">点击“新建”创建第一页</div>
        </el-card>

        <el-card v-if="currentPage" shadow="never" body-style="padding: 12px">
          <div class="card-title" style="margin-bottom: 8px">当前页图层</div>
          <div
            v-for="it in currentPage.items || []"
            :key="it.id"
            class="list-row"
            :class="{ active: it.id === itemID }"
            @click="itemID = it.id"
          >
            <el-tag size="small" type="info" effect="plain">{{ itemLabel(it.item_type) }}</el-tag>
            <span class="grow ellipsis small">{{ it.item_type === "image" ? it.content.split("/").pop() : itemText(it) }}</span>
          </div>
          <div v-if="!currentPage.items?.length" class="empty-hint" style="padding: 14px 0">画布上方添加元素</div>
        </el-card>

        <el-card shadow="never" body-style="padding: 8px 12px">
          <el-collapse>
            <el-collapse-item title="全局设置（倒计时 / 字体 / 地址）" name="cfg">
              <el-form label-position="top" size="small">
                <el-form-item label="倒计时目标时间">
                  <el-date-picker
                    v-model="countdownDate"
                    type="datetime"
                    placeholder="选择比赛开始/结束时间"
                    style="width: 100%"
                    @change="(d: Date | null) => saveGlobalConfig({ countdown_target: d ? d.toISOString() : '' }, '倒计时已更新')"
                  />
                </el-form-item>
                <el-form-item v-if="!isCloud" label="本机房推送地址（选手机访问的局域网 URL）">
                  <div class="row nowrap" style="width: 100%">
                    <el-input v-model="baseURL" placeholder="http://192.168.1.10:8080" />
                    <el-button @click="saveGlobalConfig({ base_url: baseURL.trim() })">保存</el-button>
                  </div>
                </el-form-item>
                <el-form-item label="全局字体">
                  <el-select
                    :model-value="config?.active_font || ''"
                    style="width: 100%"
                    @change="(v: string) => saveGlobalConfig({ active_font: v }, '字体已切换')"
                  >
                    <el-option value="" label="系统默认无衬线" />
                    <el-option v-for="f in fonts" :key="f.id" :value="f.filename" :label="f.name" />
                  </el-select>
                  <div class="row" style="margin-top: 6px">
                    <el-button :icon="Upload" @click="uploadFont">上传字体 (.ttf/.woff2)</el-button>
                  </div>
                  <div v-for="f in fonts" :key="f.id" class="row small" style="margin-top: 4px">
                    <span class="grow ellipsis">{{ f.name }}</span>
                    <el-button link type="danger" size="small" @click="removeFont(f)">删除</el-button>
                  </div>
                </el-form-item>
              </el-form>
            </el-collapse-item>
          </el-collapse>
        </el-card>
      </div>

      <!-- Center: Toolbar + 16:9 Canvas -->
      <div class="canvas-col">
        <div class="row add-bar">
          <span class="muted small">添加元素：</span>
          <el-button v-for="t in ITEM_TYPES" :key="t.key" size="small" :icon="Plus" :disabled="!currentPage" @click="addItem(t.key)">{{ t.label }}</el-button>
          <span class="spacer" />
          <span class="muted small">拖动画布元素可自动保存位置</span>
        </div>
        <div class="stage-wrap">
          <div
            v-if="currentPage"
            ref="stageRef"
            class="stage"
            :style="{ background: pageForm.bg_color || currentPage.bg_color || '#000' }"
            @pointerdown="itemID = null"
          >
            <div
              v-for="it in currentPage.items || []"
              :key="it.id"
              class="canvas-item"
              :class="{ selected: it.id === itemID }"
              :style="{
                left: (boxes[it.id]?.pos_x ?? it.pos_x) + '%',
                top: (boxes[it.id]?.pos_y ?? it.pos_y) + '%',
                width: (boxes[it.id]?.width ?? it.width) + '%',
                height: (boxes[it.id]?.height ?? it.height) + '%',
                color: (draft?.id === it.id ? draft.font_color : it.font_color) || '#fff',
                background: (draft?.id === it.id ? draft.bg_color : it.bg_color) === 'transparent' ? undefined : (draft?.id === it.id ? draft.bg_color : it.bg_color),
                fontSize: (Number(draft?.id === it.id ? draft.font_size : it.font_size) || 4) + 'cqh',
                fontWeight: (draft?.id === it.id ? draft.font_weight : it.font_weight) || '600',
                borderRadius: ((draft?.id === it.id ? draft.border_radius : it.border_radius) || '0') + 'px',
                zIndex: draft?.id === it.id ? draft.z_index : it.z_index,
              }"
              @pointerdown="startDrag($event, it, false)"
            >
              <img v-if="it.item_type === 'image' && (draft?.id === it.id ? draft.content : it.content)" :src="draft?.id === it.id ? draft.content : it.content" alt="" draggable="false" />
              <div
                v-else
                class="item-text"
                :style="{ textAlign: ((draft?.id === it.id ? draft.text_align : it.text_align) || 'center') as 'left' | 'center' | 'right' }"
              >{{ draft?.id === it.id ? itemText(draft) : itemText(it) }}</div>
              <span v-if="it.id === itemID" class="handle" @pointerdown="startDrag($event, it, true)" />
            </div>
          </div>
          <el-empty v-else description="请先在左侧新建一个页面" />
        </div>
      </div>

      <!-- Right: Inspector -->
      <div class="stack side">
        <el-card v-if="currentPage" shadow="never" body-style="padding: 12px">
          <div class="card-title" style="margin-bottom: 10px">
            页面属性
            <div class="actions">
              <el-tooltip content="复制页面"><el-button size="small" circle :icon="CopyDocument" @click="duplicatePage(currentPage.id)" /></el-tooltip>
              <el-tooltip content="删除页面"><el-button size="small" circle type="danger" plain :icon="Delete" @click="deletePage(currentPage.id)" /></el-tooltip>
            </div>
          </div>
          <el-form label-width="68px" size="small">
            <el-form-item label="标题"><el-input v-model="pageForm.title" /></el-form-item>
            <el-form-item label="停留秒数"><el-input-number v-model="pageForm.durationSec" :min="1" :max="3600" style="width: 100%" /></el-form-item>
            <el-form-item label="背景颜色">
              <div class="row nowrap"><el-color-picker v-model="pageForm.bg_color" /><el-input v-model="pageForm.bg_color" class="mono" /></div>
            </el-form-item>
            <el-form-item label="切换动画">
              <el-select v-model="pageForm.transition" style="width: 100%">
                <el-option value="fade" label="淡入淡出" />
                <el-option value="slide" label="滑动" />
                <el-option value="none" label="无动画" />
              </el-select>
            </el-form-item>
            <el-button type="primary" style="width: 100%" @click="savePage">保存页面属性</el-button>
          </el-form>
        </el-card>

        <el-card v-if="draft" shadow="never" body-style="padding: 12px">
          <div class="card-title" style="margin-bottom: 10px">
            元素 · {{ itemLabel(draft.item_type) }}
            <div class="actions"><el-button size="small" type="danger" text :icon="Delete" @click="deleteItem">删除</el-button></div>
          </div>
          <el-form label-width="68px" size="small" @input="dirty = true" @change="dirty = true">
            <el-form-item v-if="draft.item_type === 'image'" label="图片">
              <el-button :icon="Upload" @click="replaceImage">更换图片</el-button>
            </el-form-item>
            <el-form-item v-else-if="draft.item_type !== 'clock'" label="内容">
              <el-input v-model="draft.content" type="textarea" :rows="3" @input="dirty = true" />
            </el-form-item>
            <el-form-item label="字号(cqh)">
              <el-input v-model="draft.font_size" placeholder="例如 4" @input="dirty = true" />
            </el-form-item>
            <el-form-item label="文字颜色">
              <div class="row nowrap">
                <el-color-picker v-model="draft.font_color" @change="dirty = true" />
                <el-input v-model="draft.font_color" class="mono" @input="dirty = true" />
              </div>
            </el-form-item>
            <el-form-item label="背景色">
              <div class="row nowrap">
                <el-color-picker :model-value="draft.bg_color === 'transparent' ? '' : draft.bg_color" show-alpha @change="(v: string | null) => { if (draft) { draft.bg_color = v || 'transparent'; dirty = true; } }" />
                <el-button size="small" @click="(draft.bg_color = 'transparent'), (dirty = true)">透明</el-button>
              </div>
            </el-form-item>
            <el-form-item label="对齐/字重">
              <div class="row nowrap" style="width: 100%">
                <el-select v-model="draft.text_align" @change="dirty = true">
                  <el-option value="left" label="居左" />
                  <el-option value="center" label="居中" />
                  <el-option value="right" label="居右" />
                </el-select>
                <el-select v-model="draft.font_weight" @change="dirty = true">
                  <el-option value="400" label="常规" />
                  <el-option value="600" label="半粗" />
                  <el-option value="700" label="粗体" />
                </el-select>
              </div>
            </el-form-item>
            <el-form-item label="层级">
              <el-input-number v-model="draft.z_index" :min="0" :max="999" style="width: 100%" @change="dirty = true" />
            </el-form-item>
            <el-button type="primary" :disabled="!dirty" style="width: 100%" @click="saveItem">保存元素修改</el-button>
          </el-form>
        </el-card>
      </div>
    </div>
  </div>
</template>

<style scoped>
.topbar .el-button + .el-button { margin-left: 0; }
.target-row .el-button + .el-button { margin-left: 0; }
.status-summary { display: inline-flex; align-items: center; gap: 6px; }
.room-pills { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
.room-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 11px;
  border-radius: 999px;
  border: 1px solid var(--el-border-color);
  background: var(--el-fill-color-blank);
  color: var(--el-text-color-regular);
  font: inherit;
  font-size: 12px;
  line-height: 1.4;
  cursor: pointer;
  transition: border-color .15s, background-color .15s, color .15s;
}
.room-pill:hover { border-color: var(--el-color-primary-light-5); color: var(--el-color-primary); }
.room-pill.active {
  border-color: var(--el-color-primary);
  background: var(--el-color-primary-light-9);
  color: var(--el-color-primary);
  font-weight: 600;
}
.room-pill.static {
  cursor: default;
  border-color: var(--el-color-primary-light-5);
  background: var(--el-color-primary-light-9);
  color: var(--el-text-color-primary);
}
.room-pill.static:hover { color: var(--el-text-color-primary); }
.room-pill-name { font-weight: 500; }
.room-pill.active .room-pill-name { font-weight: 600; }
.room-pill-meta { color: var(--el-text-color-secondary); font-weight: normal; font-variant-numeric: tabular-nums; }
.room-pill.active .room-pill-meta { color: var(--el-color-primary); opacity: 0.85; }
.pill-check { font-size: 12px; }
.studio { display: grid; grid-template-columns: 250px minmax(0, 1fr) 290px; gap: 14px; align-items: start; }
@media (max-width: 1280px) { .studio { grid-template-columns: 1fr; } }
.side { min-width: 0; }
.list-row { display: flex; align-items: center; gap: 6px; padding: 6px 8px; border-radius: 6px; cursor: pointer; font-size: 13px; }
.list-row:hover { background: var(--el-fill-color-light); }
.list-row.active { background: var(--el-color-primary-light-9); color: var(--el-color-primary); font-weight: 600; }
.list-row .el-button + .el-button { margin-left: 0; }
.idx { width: 18px; color: var(--el-text-color-secondary); }
.canvas-col { display: flex; flex-direction: column; gap: 10px; min-width: 0; }
.add-bar .el-button + .el-button { margin-left: 0; }
.stage-wrap { background: var(--el-fill-color); border: 1px solid var(--app-border); border-radius: 10px; padding: 16px; display: grid; place-items: center; min-height: 440px; }
.stage { position: relative; width: 100%; max-width: 960px; aspect-ratio: 16 / 9; overflow: hidden; box-shadow: 0 12px 32px rgba(0,0,0,.28); container-type: size; user-select: none; }
.canvas-item { position: absolute; overflow: hidden; cursor: move; touch-action: none; }
.canvas-item.selected { outline: 2px solid var(--el-color-primary); }
.canvas-item img { width: 100%; height: 100%; object-fit: contain; display: block; pointer-events: none; }
.item-text { width: 100%; height: 100%; display: flex; align-items: center; justify-content: center; white-space: pre-wrap; padding: 0 4px; }
.handle { position: absolute; right: 0; bottom: 0; width: 12px; height: 12px; background: var(--el-color-primary); cursor: nwse-resize; border-top-left-radius: 3px; }
</style>
