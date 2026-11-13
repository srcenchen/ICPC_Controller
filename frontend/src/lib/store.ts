import { computed, reactive } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { api, errorText, setApiRoom, wsURL } from "./api";
import { deviceKey } from "./utils";
import type { AdminEvent, Deployment, Device, Room } from "./types";

export const PAGE_IDS = ["rooms", "dashboard", "devices", "screen", "commands", "distribute", "network", "power", "checkin", "broadcast", "settings"] as const;
export type PageId = (typeof PAGE_IDS)[number];

export const PAGE_TITLE: Record<PageId, string> = {
  rooms: "机房总览",
  dashboard: "总览",
  devices: "设备",
  screen: "屏幕墙",
  commands: "批量命令",
  distribute: "文件分发",
  network: "网络管控",
  power: "电源",
  checkin: "签到",
  broadcast: "广播",
  settings: "设置",
};

const emptyDeploy: Deployment = {
  mode: "standalone",
  node_id: "",
  room_name: "",
  cloud_url: "",
  token_set: false,
  allow_insecure: false,
  device_id_start: 1,
  advertise_ip: "",
};

function parsePage(hash: string): PageId {
  const page = hash.replace(/^#\/?/, "") as PageId;
  return PAGE_IDS.includes(page) ? page : "dashboard";
}

function roomFromPath(pathname = location.pathname) {
  const match = pathname.match(/^\/room\/([^/]+)\/?$/);
  return match ? decodeURIComponent(match[1]) : "";
}

export type FleetDevice = Device & { room_id: string; room_name: string; key: string };

export const store = reactive({
  page: parsePage(location.hash) as PageId,
  roomID: roomFromPath(),
  deployment: { ...emptyDeploy } as Deployment,
  connection: "",
  rooms: [] as Room[],
  devices: [] as Device[],
  devicesLoading: false,
  devicesError: "",
  wsConnected: false,
  /** Cloud "all rooms" selection, keys are "room:id". */
  fleet: [] as string[],
  dark: document.documentElement.classList.contains("dark"),
  // Global overlays, so any page can open them for a device.
  detailID: null as number | null,
  terminalID: null as number | null,
  screenID: null as number | null,
  /** Devices handed to the next action page (e.g. "send command" from a drawer). */
  pendingTargets: [] as number[],
  /** Command history entry to open when the commands page mounts. */
  pendingCommandID: null as number | null,
});
setApiRoom(store.roomID);

export const isCloud = computed(() => store.deployment.mode === "cloud");
export const isCloudAll = computed(() => isCloud.value && !store.roomID);
export const roomName = computed(() => store.rooms.find((room) => room.id === store.roomID)?.name || store.roomID);
export const onlineDevices = computed(() => store.devices.filter((device) => device.connected));

export const fleetDevices = computed<FleetDevice[]>(() =>
  store.rooms.flatMap((room) =>
    (room.devices || []).map((device) => ({ ...device, room_id: room.id, room_name: room.name, key: deviceKey(room.id, device.assigned_id) })),
  ),
);

export const headerStats = computed(() => {
  const list = isCloudAll.value ? fleetDevices.value : store.devices;
  return { online: list.filter((device) => device.connected).length, total: list.length };
});

// ---- navigation ----------------------------------------------------------

function urlFor(room: string, page: PageId) {
  return (room ? "/room/" + encodeURIComponent(room) : "/") + "#" + page;
}

export function go(page: PageId) {
  const url = urlFor(store.roomID, page);
  if (location.pathname + location.hash !== url) history.pushState(null, "", url);
  store.page = page;
}

export function setRoom(id: string, page?: PageId) {
  store.roomID = id;
  setApiRoom(id);
  if (page) store.page = page;
  history.pushState(null, "", urlFor(id, store.page));
  store.devices = [];
  loadStatus();
  loadDevices();
}

// ---- feedback ------------------------------------------------------------

export function toast(text: string, type: "success" | "error" | "info" | "warning" = "info") {
  ElMessage({ message: text, type, grouping: true, showClose: type === "error", duration: type === "error" ? 4000 : 2400 });
}

export function toastError(err: unknown) {
  toast(errorText(err), "error");
}

export async function confirmAction(text: string, opts: { title?: string; danger?: boolean; confirmText?: string } = {}) {
  try {
    await ElMessageBox.confirm(text, opts.title || "请确认", {
      type: opts.danger ? "warning" : "info",
      confirmButtonText: opts.confirmText || "确定",
      cancelButtonText: "取消",
      confirmButtonClass: opts.danger ? "el-button--danger" : undefined,
      autofocus: false,
    });
    return true;
  } catch {
    return false;
  }
}

/** run wraps an async action with error toasts; returns undefined on failure. */
export async function run<T>(fn: () => Promise<T>, success?: string): Promise<T | undefined> {
  try {
    const result = await fn();
    if (success) toast(success, "success");
    return result;
  } catch (err) {
    toastError(err);
    return undefined;
  }
}

// ---- data ----------------------------------------------------------------

export async function loadStatus() {
  try {
    const result = await api<{ deployment: Deployment; connection: string }>("/api/cluster/status");
    store.deployment = result.deployment || { ...emptyDeploy };
    store.connection = result.connection || "";
    if (store.deployment.mode === "cloud") await refreshRooms();
  } catch {
    /* auth redirect handled in api() */
  }
}

export async function refreshRooms() {
  if (store.deployment.mode !== "cloud") return;
  const list = await api<Room[]>("/api/cluster/rooms");
  store.rooms = list || [];
}

let deviceTimer = 0;
let deviceSeq = 0;
export async function loadDevices() {
  if (isCloudAll.value) {
    store.devices = [];
    return;
  }
  const seq = ++deviceSeq;
  store.devicesLoading = !store.devices.length;
  try {
    const list = await api<Device[] | null>("/api/devices");
    if (seq !== deviceSeq) return;
    store.devices = (Array.isArray(list) ? list : []).sort((a, b) => a.assigned_id - b.assigned_id);
    store.devicesError = "";
  } catch (err) {
    if (seq === deviceSeq) store.devicesError = errorText(err);
  } finally {
    if (seq === deviceSeq) store.devicesLoading = false;
  }
}

/** Coalesce bursts of device events (health reports arrive constantly). */
export function scheduleDeviceReload(delay = 800) {
  if (deviceTimer) return;
  deviceTimer = window.setTimeout(() => {
    deviceTimer = 0;
    loadDevices();
  }, delay);
}

/** Open an action page with the given devices preselected. */
export function actOn(page: PageId, ids: number[]) {
  store.pendingTargets = ids;
  store.detailID = null;
  go(page);
}

export function takePendingTargets() {
  const ids = store.pendingTargets;
  store.pendingTargets = [];
  return ids;
}

export function deviceByID(id: number | null) {
  return id == null ? undefined : store.devices.find((device) => device.assigned_id === id);
}

// ---- admin websocket -----------------------------------------------------

type Listener = (event: AdminEvent) => void;
const listeners = new Set<Listener>();

export function onAdmin(fn: Listener) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

const DEVICE_EVENTS = new Set(["device_connected", "device_disconnected", "device_updated", "checkin_updated", "device_health"]);

function openAdminSocket() {
  let retry = 1000;
  const open = () => {
    const socket = new WebSocket(wsURL("/ws/admin"));
    socket.onopen = () => {
      store.wsConnected = true;
      retry = 1000;
      loadDevices();
    };
    socket.onmessage = (message) => {
      let event: AdminEvent;
      try {
        event = JSON.parse(message.data);
      } catch {
        return;
      }
      if (DEVICE_EVENTS.has(event.event)) scheduleDeviceReload(event.event === "device_health" ? 2000 : 400);
      listeners.forEach((fn) => fn(event));
    };
    socket.onclose = () => {
      store.wsConnected = false;
      setTimeout(open, retry);
      retry = Math.min(retry * 2, 15000);
    };
  };
  open();
}

// ---- boot ----------------------------------------------------------------

export function initStore() {
  window.addEventListener("popstate", () => {
    store.page = parsePage(location.hash);
    const room = roomFromPath();
    if (room !== store.roomID) {
      store.roomID = room;
      setApiRoom(room);
      loadDevices();
    }
  });
  window.addEventListener("hashchange", () => (store.page = parsePage(location.hash)));
  loadStatus().then(loadDevices);
  openAdminSocket();
  // Cloud: room summaries are pulled, not pushed.
  setInterval(() => {
    if (isCloud.value && document.visibilityState === "visible") refreshRooms().catch(() => {});
  }, 10000);
  // Safety net in case a device event was missed while the socket reconnected.
  setInterval(() => {
    if (document.visibilityState === "visible") loadDevices();
  }, 30000);
}

export function toggleDark() {
  store.dark = !store.dark;
  document.documentElement.classList.toggle("dark", store.dark);
  localStorage.setItem("icpc-theme", store.dark ? "dark" : "light");
}

export async function queueFleet(selection: string[], operation: string, extra: Record<string, unknown> = {}) {
  const targets: Record<string, number[]> = {};
  selection.forEach((key) => {
    const split = key.lastIndexOf(":");
    (targets[key.slice(0, split)] ||= []).push(Number(key.slice(split + 1)));
  });
  return api<{ job_ids: string[] }>("/api/cluster/jobs", { json: { operation, room_ids: Object.keys(targets), targets, ...extra } });
}
