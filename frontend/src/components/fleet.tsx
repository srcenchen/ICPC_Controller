import { useEffect, useMemo, useState } from "react";
import { api } from "@/lib/api";
import { checkinLabel, deviceKey } from "@/lib/utils";
import type { Device } from "@/lib/types";
import { Badge, Button, Empty, Input } from "@/components/ui";
import { useApp } from "@/state";

export type FleetDevice = Device & { room_id: string; room_name: string; key: string };

export function useFleetDevices() {
  const app = useApp();
  const devices = useMemo<FleetDevice[]>(() => app.rooms.flatMap((room) => (room.devices || []).map((device) => ({
    ...device,
    room_id: room.id,
    room_name: room.name,
    key: deviceKey(room.id, device.assigned_id),
  }))), [app.rooms]);
  return { ...app, devices };
}

export function FleetTable({ onManage }: { onManage?: (room: string, device: number) => void }) {
  const { devices, fleet, toggleFleet, selectFleet, clearFleet, rooms } = useFleetDevices();
  const [query, setQuery] = useState("");
  const [room, setRoom] = useState("");
  const shown = devices.filter((device) => {
    if (room && device.room_id !== room) return false;
    const q = query.trim().toLowerCase();
    if (!q) return true;
    return [device.room_name, device.hostname, device.student_name, device.student_num, String(device.assigned_id)].join(" ").toLowerCase().includes(q);
  });
  return (
    <div className="mb-4">
      <div className="mb-2 flex flex-wrap gap-2">
        <select className="h-9 rounded-md border border-border bg-card px-2 text-sm" value={room} onChange={(event) => setRoom(event.target.value)} aria-label="机房筛选">
          <option value="">全部机房</option>
          {rooms.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
        </select>
        <Input className="max-w-xs" placeholder="搜索机房、编号、选手" value={query} onChange={(event) => setQuery(event.target.value)} />
        <Button variant="outline" size="sm" onClick={() => selectFleet(shown.filter((device) => device.connected).map((device) => device.key))}>选中筛选内在线</Button>
        <Button variant="outline" size="sm" onClick={() => selectFleet(shown.map((device) => device.key))}>选中筛选结果</Button>
        <Button variant="ghost" size="sm" onClick={clearFleet}>清空</Button>
        <span className="self-center text-xs text-muted-foreground">已选 {fleet.length} 台</span>
      </div>
      <div className="table-wrap max-h-[420px]">
        <table className="data">
          <thead><tr><th></th><th>机房 / 编号</th><th>主机</th><th>选手</th><th>状态</th>{onManage && <th></th>}</tr></thead>
          <tbody>
            {shown.map((device) => (
              <tr key={device.key}>
                <td><input type="checkbox" aria-label={`选择 ${device.room_name} #${device.assigned_id}`} checked={fleet.includes(device.key)} onChange={() => toggleFleet(device.key)} /></td>
                <td>{device.room_name} <span className="font-mono font-semibold">#{device.assigned_id}</span></td>
                <td>{device.hostname}<div className="text-xs text-muted-foreground">{device.os_name}</div></td>
                <td>{device.student_name || "—"}<div className="text-xs text-muted-foreground">{checkinLabel(device.checkin_status)}</div></td>
                <td><Badge tone={device.connected ? "ok" : "muted"}>{device.connected ? "在线" : "离线"}</Badge></td>
                {onManage && <td><Button size="sm" variant="outline" onClick={() => onManage(device.room_id, device.assigned_id)}>进入机房</Button></td>}
              </tr>
            ))}
            {!shown.length && <tr><td colSpan={6}><Empty>没有匹配的设备</Empty></td></tr>}
          </tbody>
        </table>
      </div>
    </div>
  );
}

export async function queueFleet(selection: string[], operation: string, extra: Record<string, unknown> = {}) {
  const targets: Record<string, number[]> = {};
  selection.forEach((key) => {
    const split = key.indexOf(":");
    const room = key.slice(0, split);
    const id = Number(key.slice(split + 1));
    (targets[room] ||= []).push(id);
  });
  return api<{ job_ids: string[] }>("/api/cluster/jobs", {
    method: "POST",
    body: JSON.stringify({ operation, room_ids: Object.keys(targets), targets, ...extra }),
  });
}

export function Jobs() {
  const [jobs, setJobs] = useState<{ id: string; room_id: string; status: string; response: string; created_at: string }[]>([]);
  async function load() {
    setJobs(await api("/api/cluster/jobs"));
  }
  useEffect(() => { load().catch(() => {}); }, []);
  return (
    <div className="table-wrap mt-3 max-h-64">
      <table className="data">
        <thead><tr><th>时间</th><th>机房</th><th>状态</th><th>回执</th><th></th></tr></thead>
        <tbody>
          {jobs.slice(0, 30).map((job) => (
            <tr key={job.id}>
              <td className="whitespace-nowrap text-xs">{job.created_at}</td>
              <td className="font-mono text-xs">{job.room_id.slice(0, 8)}</td>
              <td>{job.status}</td>
              <td className="max-w-xs truncate text-xs">{job.response}</td>
              <td>{job.status === "queued" && <Button size="sm" variant="ghost" onClick={() => api("/api/cluster/jobs/" + job.id, { method: "DELETE" }).then(load)}>取消</Button>}</td>
            </tr>
          ))}
          {!jobs.length && <tr><td colSpan={5}><Empty>还没有跨机房任务</Empty></td></tr>}
        </tbody>
      </table>
      <div className="p-2"><Button size="sm" variant="outline" onClick={() => load()}>刷新回执</Button></div>
    </div>
  );
}
