<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref, type Component } from "vue";
import {
  Odometer, OfficeBuilding, Monitor, VideoCamera, Operation, FolderOpened, Connection, SwitchButton,
  Tickets, Notification, Setting, Search, Moon, Sunny, Expand, Fold, Back,
} from "@element-plus/icons-vue";
import { api } from "@/lib/api";
import {
  store, PAGE_TITLE, type PageId, go, setRoom, isCloud, isCloudAll, roomName, headerStats, fleetDevices,
  initStore, toggleDark, confirmAction,
} from "@/lib/store";
import { matchDevice, parseIP, checkinLabel } from "@/lib/utils";
import DeviceDrawer from "@/components/DeviceDrawer.vue";
import TerminalDialog from "@/components/TerminalDialog.vue";
import ScreenDialog from "@/components/ScreenDialog.vue";

const pages: Record<PageId, Component> = {
  rooms: defineAsyncComponent(() => import("@/pages/Rooms.vue")),
  dashboard: defineAsyncComponent(() => import("@/pages/Dashboard.vue")),
  devices: defineAsyncComponent(() => import("@/pages/Devices.vue")),
  screen: defineAsyncComponent(() => import("@/pages/Screen.vue")),
  commands: defineAsyncComponent(() => import("@/pages/Commands.vue")),
  distribute: defineAsyncComponent(() => import("@/pages/Distribute.vue")),
  network: defineAsyncComponent(() => import("@/pages/Network.vue")),
  power: defineAsyncComponent(() => import("@/pages/Power.vue")),
  checkin: defineAsyncComponent(() => import("@/pages/Checkin.vue")),
  broadcast: defineAsyncComponent(() => import("@/pages/Broadcast.vue")),
  settings: defineAsyncComponent(() => import("@/pages/Settings.vue")),
};

const NAV: { group: string; items: { id: PageId; icon: Component; cloudOnly?: boolean }[] }[] = [
  { group: "监控", items: [
    { id: "rooms", icon: OfficeBuilding, cloudOnly: true },
    { id: "dashboard", icon: Odometer },
    { id: "devices", icon: Monitor },
    { id: "screen", icon: VideoCamera },
  ] },
  { group: "运维", items: [
    { id: "commands", icon: Operation },
    { id: "distribute", icon: FolderOpened },
    { id: "network", icon: Connection },
    { id: "power", icon: SwitchButton },
  ] },
  { group: "比赛", items: [
    { id: "checkin", icon: Tickets },
    { id: "broadcast", icon: Notification },
  ] },
  { group: "系统", items: [{ id: "settings", icon: Setting }] },
];

const collapsed = ref(localStorage.getItem("icpc-nav") === "collapsed");
function toggleNav() {
  collapsed.value = !collapsed.value;
  localStorage.setItem("icpc-nav", collapsed.value ? "collapsed" : "open");
}

const modeLabel = computed(() => (store.deployment.mode === "cloud" ? "云端" : store.deployment.mode === "relay" ? "机房中转" : "单机"));
const scopeLabel = computed(() =>
  isCloudAll.value ? "全部机房" : store.roomID ? roomName.value : store.deployment.room_name || "本机房",
);

// ---- global search -------------------------------------------------------
type Suggestion = { value: string; id: number; room?: string; roomLabel?: string; name?: string; num?: string; ip?: string; online: boolean; checkin: number };
const searchText = ref("");
const searchRef = ref<{ focus: () => void; blur: () => void } | null>(null);

function querySearch(query: string, cb: (items: Suggestion[]) => void) {
  if (!query.trim()) return cb([]);
  const source = isCloudAll.value
    ? fleetDevices.value.map((d) => ({ d, room: d.room_id, roomLabel: d.room_name }))
    : store.devices.map((d) => ({ d, room: undefined, roomLabel: undefined }));
  cb(
    source
      .filter(({ d }) => matchDevice(d, query))
      .slice(0, 12)
      .map(({ d, room, roomLabel }) => ({
        value: `#${d.assigned_id}`,
        id: d.assigned_id,
        room,
        roomLabel,
        name: d.student_name,
        num: d.student_num,
        ip: parseIP(d.local_ip),
        online: d.connected,
        checkin: d.checkin_status,
      })),
  );
}

function pickSuggestion(item: Record<string, unknown>) {
  const s = item as unknown as Suggestion;
  searchText.value = "";
  searchRef.value?.blur();
  if (s.room) setRoom(s.room, "devices");
  store.detailID = s.id;
}

function onKey(event: KeyboardEvent) {
  const target = event.target as HTMLElement | null;
  const typing = target && (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.isContentEditable || target.closest(".xterm"));
  if ((event.key === "k" && (event.metaKey || event.ctrlKey)) || (event.key === "/" && !typing)) {
    event.preventDefault();
    searchRef.value?.focus();
  }
}

async function logout() {
  if (!(await confirmAction("确定退出登录吗？"))) return;
  await api("/api/auth/logout", { method: "POST" }).catch(() => {});
  location.href = "/login.html";
}

onMounted(() => {
  initStore();
  window.addEventListener("keydown", onKey);
});
onBeforeUnmount(() => window.removeEventListener("keydown", onKey));
</script>

<template>
  <el-container class="shell">
    <el-aside :width="collapsed ? '64px' : '216px'" class="aside">
      <div class="brand" @click="go('dashboard')">
        <span class="logo">IC</span>
        <div v-if="!collapsed" class="brand-text">
          <div class="brand-title">ICPC 集控</div>
          <div class="brand-sub">{{ modeLabel }}</div>
        </div>
      </div>
      <el-scrollbar class="nav-scroll">
        <el-menu :default-active="store.page" :collapse="collapsed" :collapse-transition="false" class="nav" @select="(id: string) => go(id as PageId)">
          <template v-for="group in NAV" :key="group.group">
            <el-menu-item-group :title="collapsed ? '' : group.group">
              <template v-for="item in group.items" :key="item.id">
                <el-menu-item v-if="!item.cloudOnly || isCloud" :index="item.id">
                  <el-icon><component :is="item.icon" /></el-icon>
                  <template #title>{{ PAGE_TITLE[item.id] }}</template>
                </el-menu-item>
              </template>
            </el-menu-item-group>
          </template>
        </el-menu>
      </el-scrollbar>
      <div class="aside-foot">
        <el-tooltip :content="collapsed ? '展开菜单' : '收起菜单'" placement="right">
          <el-button text :icon="collapsed ? Expand : Fold" @click="toggleNav" />
        </el-tooltip>
        <el-tooltip content="退出登录" placement="right">
          <el-button text :icon="Back" @click="logout" />
        </el-tooltip>
      </div>
    </el-aside>

    <el-container class="main-col">
      <el-header class="header" height="56px">
        <div class="scope">
          <el-select
            v-if="isCloud"
            :model-value="store.roomID"
            class="room-select"
            placeholder="全部机房"
            @change="(v: string) => setRoom(v)"
          >
            <el-option label="全部机房" value="" />
            <el-option v-for="room in store.rooms" :key="room.id" :label="room.name" :value="room.id">
              <span class="row nowrap"><span class="dot" :class="{ on: room.online }" />{{ room.name }}
                <span class="muted small">{{ room.devices.filter((d) => d.connected).length }}/{{ room.devices.length }}</span></span>
            </el-option>
          </el-select>
          <div v-else class="scope-label">
            <div class="scope-title">{{ PAGE_TITLE[store.page] }}</div>
            <div class="muted small">{{ scopeLabel }}</div>
          </div>
        </div>

        <el-autocomplete
          ref="searchRef"
          v-model="searchText"
          class="global-search"
          :fetch-suggestions="querySearch"
          :trigger-on-focus="false"
          placeholder="搜索编号 / 姓名 / 学号 / IP"
          clearable
          highlight-first-item
          popper-class="search-popper"
          @select="pickSuggestion"
        >
          <template #prefix><el-icon><Search /></el-icon></template>
          <template #suffix><kbd v-if="!searchText">/</kbd></template>
          <template #default="{ item }">
            <div class="sugg">
              <span class="dot" :class="{ on: item.online }" />
              <b class="mono">#{{ item.id }}</b>
              <span class="grow ellipsis">
                {{ item.name || "未签到" }}
                <span class="muted small">{{ item.num }}</span>
              </span>
              <span class="muted small">{{ item.roomLabel || item.ip }}</span>
              <el-tag size="small" :type="item.checkin === 1 ? 'success' : 'info'" disable-transitions>{{ checkinLabel(item.checkin) }}</el-tag>
            </div>
          </template>
        </el-autocomplete>

        <div class="header-right">
          <el-tooltip :content="store.wsConnected ? '实时连接正常' : '实时连接断开，正在重连'">
            <span class="live"><span class="dot" :class="store.wsConnected ? 'on' : 'warn'" />{{ store.wsConnected ? "实时" : "重连中" }}</span>
          </el-tooltip>
          <span class="online-count">
            在线 <b>{{ headerStats.online }}</b><span class="muted"> / {{ headerStats.total }}</span>
          </span>
          <el-tooltip content="切换深浅色">
            <el-button circle text :icon="store.dark ? Sunny : Moon" @click="toggleDark" />
          </el-tooltip>
        </div>
      </el-header>

      <el-main class="main">
        <Suspense>
          <component :is="pages[store.page]" :key="store.page + '|' + store.roomID" />
          <template #fallback>
            <div class="empty-hint">加载中…</div>
          </template>
        </Suspense>
      </el-main>
    </el-container>

    <DeviceDrawer />
    <TerminalDialog />
    <ScreenDialog />
  </el-container>
</template>

<style scoped>
.shell { height: 100vh; }
.aside {
  background: var(--app-sidebar);
  border-right: 1px solid var(--app-border);
  display: flex;
  flex-direction: column;
  transition: width .2s;
  overflow: hidden;
}
.brand { display: flex; align-items: center; gap: 10px; padding: 14px 14px 10px; cursor: pointer; }
.logo {
  width: 34px; height: 34px; border-radius: 9px; flex: none;
  display: grid; place-items: center;
  background: linear-gradient(135deg, var(--el-color-primary), var(--el-color-primary-light-3));
  color: #fff; font-weight: 700; font-size: 13px;
}
.brand-title { font-weight: 600; font-size: 15px; }
.brand-sub { font-size: 11px; color: var(--el-text-color-secondary); }
.nav-scroll { flex: 1; }
.nav { border-right: none; background: transparent; }
.nav :deep(.el-menu-item) { height: 40px; line-height: 40px; margin: 2px 8px; border-radius: 8px; }
.nav :deep(.el-menu-item.is-active) { background: var(--el-color-primary-light-9); font-weight: 600; }
.nav :deep(.el-menu-item-group__title) { padding-top: 10px; padding-bottom: 4px; font-size: 11px; }
.nav.el-menu--collapse :deep(.el-menu-item) { margin: 2px 6px; }
.aside-foot { display: flex; justify-content: space-around; padding: 8px; border-top: 1px solid var(--app-border); }
.main-col { min-width: 0; }
.header {
  display: flex; align-items: center; gap: 16px;
  background: var(--el-bg-color);
  border-bottom: 1px solid var(--app-border);
}
.scope { min-width: 160px; }
.room-select { width: 220px; }
.scope-title { font-weight: 600; font-size: 15px; line-height: 1.2; }
.global-search { width: min(420px, 40vw); margin: 0 auto; }
.header-right { display: flex; align-items: center; gap: 14px; font-size: 13px; }
.live { display: inline-flex; align-items: center; gap: 6px; color: var(--el-text-color-secondary); }
.online-count b { font-variant-numeric: tabular-nums; }
.main { padding: 20px; overflow: auto; }
.sugg { display: flex; align-items: center; gap: 8px; line-height: 1.3; padding: 4px 0; }
@media (max-width: 900px) {
  .online-count, .live { display: none; }
  .global-search { width: 100%; }
}
</style>
