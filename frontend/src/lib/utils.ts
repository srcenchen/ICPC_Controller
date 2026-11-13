import type { Device } from "./types";

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
  fired: "已执行",
};

export function statusLabel(status?: string) {
  return STATUS_LABEL[status || ""] || status || "—";
}

const ACTION_LABEL: Record<string, string> = {
  stop: "关闭广播",
  start: "推送广播",
  sync: "同步素材",
  reset: "复位轮播",
  shutdown: "关机",
  reboot: "重启",
  wol: "唤醒",
};

const FIELD_LABEL: Record<string, string> = {
  action: "操作",
  applied: "结果",
  message: "说明",
  revision: "版本",
  error: "错误",
  status: "状态",
  id: "编号",
  sent: "已发送",
  total: "总数",
  affected_count: "影响台数",
  job_ids: "任务数",
  command: "命令",
  target_type: "目标",
  target_id: "设备",
};

function parseMaybeJson(raw: string): unknown {
  const text = raw.trim();
  if (!text || (text[0] !== "{" && text[0] !== "[" && text[0] !== '"')) return text;
  try {
    const value = JSON.parse(text) as unknown;
    return typeof value === "string" ? parseMaybeJson(value) : value;
  } catch {
    return text;
  }
}

function fieldText(key: string, value: unknown): string | null {
  if (value == null || value === "") return null;
  if (key === "applied") return value ? "已应用到机房" : "未应用";
  if (key === "action") return ACTION_LABEL[String(value)] || String(value);
  if (key === "status") return statusLabel(String(value));
  if (key === "revision") return "版本 " + value;
  if (key === "target_type") return value === "broadcast" ? "全部在线" : value === "list" ? "指定设备" : String(value);
  if (key === "job_ids" && Array.isArray(value)) return "任务 " + value.length + " 个";
  if (typeof value === "boolean") return (FIELD_LABEL[key] || key) + (value ? "：是" : "：否");
  if (typeof value === "number" || typeof value === "string") {
    const label = FIELD_LABEL[key];
    return label ? label + " " + value : String(value);
  }
  return null;
}

// formatReceipt turns a raw API body into a sentence. Unknown JSON objects are
// still expanded field by field instead of being shown as source text.
export function formatReceipt(raw?: string): string {
  if (!raw || !raw.trim()) return "—";
  const value = parseMaybeJson(raw);
  if (typeof value === "string") return value;
  if (Array.isArray(value)) {
    return value.map((item) => (item && typeof item === "object" ? formatReceipt(JSON.stringify(item)) : String(item))).filter(Boolean).join("；") || "—";
  }
  if (!value || typeof value !== "object") return String(value);
  const record = value as Record<string, unknown>;
  const preferred = ["message", "error", "action", "applied", "revision", "status", "sent", "total", "affected_count", "target_type", "target_id", "id"];
  const seen = new Set<string>();
  const parts: string[] = [];
  for (const key of preferred) {
    if (!(key in record)) continue;
    seen.add(key);
    const text = fieldText(key, record[key]);
    if (text) parts.push(text);
  }
  for (const [key, item] of Object.entries(record)) {
    if (seen.has(key)) continue;
    const text = fieldText(key, item);
    if (text) parts.push(text);
  }
  return parts.join(" · ") || "—";
}

export function formatGPU(raw?: string) {
  const value = raw ? parseMaybeJson(raw) : null;
  if (!Array.isArray(value) || !value.length) return raw && !raw.startsWith("[") ? raw : "";
  return value.map((item) => {
    const gpu = item as { name?: string; vendor?: string; driver?: string; type?: string };
    const name = gpu.name || "未知显卡";
    const vendor = gpu.vendor ? "（" + gpu.vendor + "）" : "";
    const driver = gpu.driver ? "，驱动 " + gpu.driver : "";
    const kind = gpu.type && gpu.type !== "Unknown" ? "，" + gpu.type : "";
    return name + vendor + driver + kind;
  }).join("；");
}

export function formatDisk(raw?: string) {
  const value = raw ? parseMaybeJson(raw) : null;
  if (!Array.isArray(value) || !value.length) return raw && !raw.startsWith("[") ? raw : "";
  return value.map((item) => {
    const disk = item as { filesystem?: string; mountpoint?: string; name?: string; bytes?: { used?: number; total?: number } };
    const where = disk.mountpoint || disk.name || "磁盘";
    const fs = disk.filesystem ? " " + disk.filesystem : "";
    const used = formatBytes(disk.bytes?.used);
    const total = formatBytes(disk.bytes?.total);
    return `${where}${fs} ${used} / ${total}`;
  }).join("；");
}

export function formatTime(iso?: string) {
  if (!iso) return "—";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

export function checkinLabel(status: number) {
  return ["未签到", "已签到", "已签退"][status] || "未知";
}

export function parseIP(raw?: string) {
  if (!raw) return "";
  const clean = (value: string) => value.split("/")[0];
  try {
    const data = JSON.parse(raw);
    if (Array.isArray(data)) {
      const hit = data.find((item) => item && (item.ipv4 || item.ip || item.addr || item.address));
      return hit ? clean(String(hit.ipv4 || hit.ip || hit.addr || hit.address)) : "";
    }
    return typeof data === "string" ? clean(data) : "";
  } catch {
    return clean(raw);
  }
}

export function deviceKey(room: string, id: number) {
  return room + ":" + id;
}

export type TagType = "success" | "warning" | "danger" | "info" | "primary";

export function statusType(status?: string): TagType {
  switch (status) {
    case "completed": case "fired": case "sent": return "success";
    case "failed": case "timeout": case "stalled": return "danger";
    case "cancelled": case "stopped": case "unknown": return "warning";
    case "running": case "downloading": case "verifying": case "executing": case "dispatched": return "primary";
    default: return "info";
  }
}

export function checkinType(status: number): TagType {
  return status === 1 ? "success" : status === 2 ? "warning" : "info";
}

export function pct(value?: number) {
  return value == null || value < 0 ? "—" : value.toFixed(0) + "%";
}

export function targetLabel(command: { target_type: string; target_id?: number }) {
  if (command.target_type === "broadcast") return "全部在线";
  if (command.target_type === "list") return "指定设备";
  return "#" + (command.target_id ?? "");
}

/**
 * matchDevice is the shared search used by every list: number (with or
 * without "#"), student name, student number, hostname, IP and MAC.
 * Space-separated terms must all match.
 */
export function matchDevice(device: Device, query: string) {
  const terms = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (!terms.length) return true;
  const hay = [
    "#" + device.assigned_id,
    device.hostname,
    device.student_name,
    device.student_num,
    parseIP(device.local_ip),
    device.mac_address,
  ].filter(Boolean).join(" ").toLowerCase();
  return terms.every((term) => {
    if (/^#?\d+$/.test(term)) {
      const n = Number(term.replace("#", ""));
      if (device.assigned_id === n) return true;
      if (term.startsWith("#")) return false;
    }
    return hay.includes(term);
  });
}

export function downloadCSV(name: string, rows: (string | number)[][]) {
  const csv = "﻿" + rows.map((row) => row.map((value) => `"${String(value ?? "").replace(/"/g, '""')}"`).join(",")).join("\r\n");
  const link = document.createElement("a");
  link.href = URL.createObjectURL(new Blob([csv], { type: "text/csv;charset=utf-8" }));
  link.download = name;
  link.click();
  setTimeout(() => URL.revokeObjectURL(link.href), 1000);
}

export function toLocalInput(iso?: string) {
  if (!iso) return "";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

export function sleep(ms: number) {
  return new Promise<void>((resolve) => setTimeout(resolve, ms));
}
