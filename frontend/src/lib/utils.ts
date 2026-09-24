import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function formatBytes(bytes?: number) {
  if (bytes == null || bytes < 0) return "—";
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  return (bytes / 1024 ** i).toFixed(i === 0 ? 0 : 1) + " " + units[i];
}

export function timeAgo(iso?: string) {
  if (!iso) return "—";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  let s = Math.floor((Date.now() - t) / 1000);
  if (s < 0) s = 0;
  if (s < 60) return s + " 秒前";
  if (s < 3600) return Math.floor(s / 60) + " 分钟前";
  if (s < 86400) return Math.floor(s / 3600) + " 小时前";
  return Math.floor(s / 86400) + " 天前";
}

export const STATUS_LABEL: Record<string, string> = {
  idle: "未运行",
  downloading: "下载中",
  pending: "等待中",
  dispatched: "已派发",
  running: "运行中",
  completed: "已完成",
  failed: "失败",
  timeout: "超时",
  stalled: "卡住",
  cancelled: "已取消",
  verifying: "校验中",
  executing: "后置命令",
  stopped: "已停止",
  queued: "等待中转",
  sent: "已投递",
  unknown: "待核实",
};

export function statusLabel(status?: string) {
  return STATUS_LABEL[status || ""] || status || "—";
}

export function checkinLabel(status: number) {
  return ["未签到", "已签到", "已签退"][status] || "未知";
}

export function parseIP(raw?: string) {
  if (!raw) return "";
  try {
    const data = JSON.parse(raw);
    if (Array.isArray(data)) {
      const hit = data.find((item) => item && (item.addr || item.ip || item.ipv4));
      return hit?.addr || hit?.ip || hit?.ipv4 || "";
    }
  } catch {
    return raw;
  }
  return raw;
}

export function deviceKey(room: string, id: number) {
  return room + ":" + id;
}

export const PAGES = [
  "rooms",
  "dashboard",
  "devices",
  "screen",
  "commands",
  "network",
  "distribute",
  "power",
  "checkin",
  "broadcast",
  "settings",
] as const;

export type PageId = (typeof PAGES)[number];

export const PAGE_TITLE: Record<PageId, string> = {
  rooms: "机房",
  dashboard: "总览",
  devices: "设备",
  screen: "屏幕",
  commands: "命令",
  network: "网络",
  distribute: "分发",
  power: "电源",
  checkin: "签到",
  broadcast: "广播",
  settings: "设置",
};

export function parsePage(hash: string): PageId {
  const page = hash.replace(/^#/, "") as PageId;
  return PAGES.includes(page) ? page : "dashboard";
}

export function roomFromPath(pathname = location.pathname) {
  const match = pathname.match(/^\/room\/([^/]+)\/?$/);
  return match ? decodeURIComponent(match[1]) : "";
}
