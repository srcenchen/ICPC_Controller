import { useState } from "react";
import { api, apiRoomPath } from "@/lib/api";
import { checkinLabel, formatBytes, parseIP, timeAgo } from "@/lib/utils";
import type { Device } from "@/lib/types";
import { Badge, Button, Dialog, Empty, PageHeader } from "@/components/ui";
import { FleetTable } from "@/components/fleet";
import { TerminalDialog } from "@/components/terminal-dialog";
import { useApp, useDevices } from "@/state";

export function Devices() {
  const app = useApp();
  const { devices, reload } = useDevices();
  const [detail, setDetail] = useState<Device | null>(null);
  const [events, setEvents] = useState<{ id: number; event: string; detail: string; at: string }[]>([]);
  const [term, setTerm] = useState<number | null>(null);
  const [query, setQuery] = useState("");

  async function open(id: number) {
    const device = await api<Device>("/api/devices/" + id);
    setDetail(device);
    setEvents(await api("/api/devices/" + id + "/events"));
  }

  if (app.cloudAll) {
    return (
      <div>
        <PageHeader title="全部设备" description="先勾选设备，再到命令、分发或电源页操作。编号只在同一机房内唯一。">
          <Button variant="outline" onClick={() => exportFleet(app.rooms)}>导出 CSV</Button>
        </PageHeader>
        <FleetTable onManage={(room) => { app.setRoom(room); app.go("devices"); }} />
      </div>
    );
  }

  const shown = devices.filter((device) => {
    const q = query.trim().toLowerCase();
    return !q || [device.hostname, device.student_name, String(device.assigned_id), parseIP(device.local_ip)].join(" ").toLowerCase().includes(q);
  });

  return (
    <div>
      <PageHeader title="设备" description="点击一行查看硬件、签到和上下线记录。">
        <input className="h-9 rounded-md border border-border bg-card px-3 text-sm" placeholder="搜索" value={query} onChange={(event) => setQuery(event.target.value)} />
        <a className="inline-flex h-9 items-center rounded-md border border-border px-3 text-sm" href={apiRoomPath("/api/devices/export")}>导出 Excel</a>
        <Button variant="destructive" onClick={async () => {
          if (!(await app.confirm("将清空全部设备记录和编号。已连接的机器会重新注册。继续？"))) return;
          await api("/api/devices/reset", { method: "POST" });
          app.toast("已重置", "ok");
          reload();
        }}>重置编号</Button>
      </PageHeader>
      <div className="table-wrap">
        <table className="data">
          <thead><tr><th>编号</th><th>主机</th><th>地址</th><th>选手</th><th>健康</th><th>状态</th></tr></thead>
          <tbody>
            {shown.map((device) => (
              <tr key={device.assigned_id} className="cursor-pointer" onClick={() => open(device.assigned_id).catch((err) => app.toast(err.message, "bad"))}>
                <td className="font-mono font-semibold">#{device.assigned_id}</td>
                <td>{device.hostname}<div className="text-xs text-muted-foreground">{device.os_name} {device.client_version}</div></td>
                <td className="font-mono text-xs">{parseIP(device.local_ip) || "—"}</td>
                <td>{device.student_name || "—"}<div className="text-xs text-muted-foreground">{checkinLabel(device.checkin_status)}</div></td>
                <td className="text-xs text-muted-foreground">CPU {pct(device.cpu_pct)} · 内存 {pct(device.mem_pct)} · {device.temp_c && device.temp_c > 0 ? device.temp_c.toFixed(0) + "°C" : "—"}</td>
                <td><Badge tone={device.connected ? "ok" : "muted"}>{device.connected ? "在线" : "离线"}</Badge></td>
              </tr>
            ))}
            {!shown.length && <tr><td colSpan={6}><Empty>没有设备</Empty></td></tr>}
          </tbody>
        </table>
      </div>
      <Dialog open={!!detail} onOpenChange={(open) => { if (!open) setDetail(null); }} title={detail ? `#${detail.assigned_id} ${detail.hostname}` : "设备"} wide>
        {detail && (
          <div className="space-y-3 text-sm">
            <div className="flex flex-wrap gap-2">
              <Button size="sm" onClick={() => setTerm(detail.assigned_id)}>打开终端</Button>
              <Button size="sm" variant="outline" onClick={() => { app.go("commands"); }}>去发命令</Button>
              <Button size="sm" variant="destructive" onClick={async () => {
                if (!(await app.confirm(`删除 #${detail.assigned_id} 的记录？`))) return;
                await api("/api/devices/" + detail.assigned_id, { method: "DELETE" });
                setDetail(null);
                reload();
              }}>删除记录</Button>
            </div>
            <dl className="grid grid-cols-2 gap-x-4 gap-y-1 md:grid-cols-3">
              <Item k="系统" v={detail.os_pretty_name || detail.os_name} />
              <Item k="内核" v={detail.kernel_release} />
              <Item k="CPU" v={detail.cpu_model} />
              <Item k="内存" v={formatBytes(detail.memory_total)} />
              <Item k="GPU" v={detail.gpu_info} />
              <Item k="MAC" v={detail.mac_address} />
              <Item k="选手" v={[detail.student_name, detail.student_num].filter(Boolean).join(" ")} />
              <Item k="最近在线" v={timeAgo(detail.last_seen)} />
              <Item k="磁盘" v={detail.disk_info} />
            </dl>
            <div className="table-wrap max-h-48">
              <table className="data">
                <thead><tr><th>时间</th><th>事件</th><th>详情</th></tr></thead>
                <tbody>
                  {events.map((event) => <tr key={event.id}><td className="text-xs">{event.at}</td><td>{event.event}</td><td className="text-xs">{event.detail}</td></tr>)}
                  {!events.length && <tr><td colSpan={3}><Empty>没有事件</Empty></td></tr>}
                </tbody>
              </table>
            </div>
          </div>
        )}
      </Dialog>
      <TerminalDialog deviceID={term} onClose={() => setTerm(null)} />
    </div>
  );
}

function Item({ k, v }: { k: string; v?: string }) {
  return <div><dt className="text-xs text-muted-foreground">{k}</dt><dd className="truncate">{v || "—"}</dd></div>;
}
function pct(value?: number) { return value == null || value < 0 ? "—" : value.toFixed(0) + "%"; }

function exportFleet(rooms: { name: string; id: string; devices: Device[] }[]) {
  const rows = [["机房", "身份", "编号", "主机名", "选手", "学号", "签到", "在线"]];
  rooms.forEach((room) => room.devices.forEach((device) => rows.push([
    room.name, room.id, String(device.assigned_id), device.hostname, device.student_name || "", device.student_num || "", checkinLabel(device.checkin_status), device.connected ? "在线" : "离线",
  ])));
  const csv = "\uFEFF" + rows.map((row) => row.map((value) => `"${String(value).replace(/"/g, '""')}"`).join(",")).join("\r\n");
  const link = document.createElement("a");
  link.href = URL.createObjectURL(new Blob([csv], { type: "text/csv;charset=utf-8" }));
  link.download = "icpc-rooms.csv";
  link.click();
}
