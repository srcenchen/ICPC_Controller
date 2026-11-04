import { useEffect, useState } from "react";
import { api, apiRoomPath } from "@/lib/api";
import { checkinLabel } from "@/lib/utils";
import type { Device } from "@/lib/types";
import { Badge, Button, Dialog, Empty, Input, PageHeader } from "@/components/ui";
import { Metric } from "@/components/targets";
import { FleetTable } from "@/components/fleet";
import { useApp, useDevices } from "@/state";

type JudgeAttendance = { event_id: string; contest_id: number; student_number: string; student_name: string; access_kind: string; node_id: string; device_id: number; logged_in_at: number };

export function Checkin() {
  const app = useApp();
  const { devices, reload } = useDevices();
  const [stats, setStats] = useState({ total: 0, checked_in: 0, checked_out: 0, not_checked: 0 });
  const [attendance, setAttendance] = useState<JudgeAttendance[]>([]);
  const [attendanceError, setAttendanceError] = useState("");
  const loadAttendance = () => api<JudgeAttendance[]>("/api/judge/attendance").then((rows) => { setAttendance(rows); setAttendanceError(""); }).catch(() => setAttendanceError("无法读取比赛签到记录，请确认此集控已升级并启用 OJ 集成。"));
  useEffect(() => {
    if (app.cloudAll) return;
    loadAttendance();
    const timer = window.setInterval(loadAttendance, 15000);
    return () => window.clearInterval(timer);
  }, [app.cloudAll, app.roomID]);
  const [form, setForm] = useState<number | null>(null);
  const [name, setName] = useState("");
  const [num, setNum] = useState("");
  const [swapFrom, setSwapFrom] = useState<number | null>(null);
  const [swapTo, setSwapTo] = useState("");

  useEffect(() => {
    if (app.cloudAll) {
      const total = app.rooms.reduce((sum, room) => sum + room.devices.length, 0);
      const checked = app.rooms.reduce((sum, room) => sum + room.devices.filter((device) => device.checkin_status === 1).length, 0);
      const out = app.rooms.reduce((sum, room) => sum + room.devices.filter((device) => device.checkin_status === 2).length, 0);
      setStats({ total, checked_in: checked, checked_out: out, not_checked: total - checked - out });
      return;
    }
    api<typeof stats>("/api/checkin/stats").then(setStats).catch(() => {});
  }, [app.cloudAll, app.rooms, devices]);

  if (app.cloudAll) {
    return (
      <div>
        <PageHeader title="签到总览" description="签到操作在具体机房内完成。这里只汇总。" >
          <Button variant="outline" onClick={() => exportCheckin(app.rooms)}>导出 CSV</Button>
        </PageHeader>
        <Stats stats={stats} />
        <FleetTable onManage={(room) => { app.setRoom(room); app.go("checkin"); }} />
      </div>
    );
  }

  return (
    <div>
      <PageHeader title="签到" description="比赛网站登录后自动签到。这里保留管理员补签、签退和换机操作。">
        <a className="inline-flex h-9 items-center rounded-md border border-border px-3 text-sm" href={apiRoomPath("/api/checkin/export")}>导出 Excel</a>
        <Button variant="outline" onClick={async () => {
          if (!(await app.confirm("清空全部签到记录？"))) return;
          await api("/api/checkin/reset-all", { method: "POST" });
          reload();
        }}>全部重置</Button>
      </PageHeader>
      <Stats stats={stats} />
      <div className="table-wrap">
        <table className="data">
          <thead><tr><th>编号</th><th>主机</th><th>选手</th><th>学号</th><th>状态</th><th></th></tr></thead>
          <tbody>
            {devices.map((device) => (
              <tr key={device.assigned_id}>
                <td className="font-mono">#{device.assigned_id}</td>
                <td>{device.hostname}</td>
                <td>{device.student_name || "—"}</td>
                <td>{device.student_num || "—"}</td>
                <td><Badge tone={device.checkin_status === 1 ? "ok" : device.checkin_status === 2 ? "warn" : "muted"}>{checkinLabel(device.checkin_status)}</Badge></td>
                <td className="space-x-1 whitespace-nowrap">
                  {device.checkin_status !== 1 && <Button size="sm" onClick={() => { setForm(device.assigned_id); setName(device.student_name || ""); setNum(device.student_num || ""); }}>签到</Button>}
                  {device.checkin_status === 1 && <Button size="sm" variant="outline" onClick={async () => { if (await app.confirm(`签退 #${device.assigned_id}？`)) { await api(`/api/checkin/${device.assigned_id}/checkout`, { method: "POST" }); reload(); } }}>签退</Button>}
                  {device.checkin_status === 2 && <Button size="sm" variant="outline" onClick={() => api(`/api/checkin/${device.assigned_id}/restore`, { method: "POST" }).then(reload)}>恢复</Button>}
                  <Button size="sm" variant="ghost" onClick={() => { setSwapFrom(device.assigned_id); setSwapTo(""); }}>换机</Button>
                  {device.checkin_status !== 0 && <Button size="sm" variant="ghost" onClick={() => api(`/api/checkin/${device.assigned_id}/reset`, { method: "POST" }).then(reload)}>重置</Button>}
                </td>
              </tr>
            ))}
            {!devices.length && <tr><td colSpan={6}><Empty>没有设备</Empty></td></tr>}
          </tbody>
        </table>
      </div>
      <section className="mt-6">
        <div className="mb-3 flex items-center justify-between"><div><h2 className="text-lg font-semibold">比赛登录签到</h2><p className="text-sm text-muted-foreground">最近 500 条，包含本机验证、远程参赛和赦免登录。</p></div><Button variant="outline" onClick={loadAttendance}>刷新记录</Button></div>
        {attendanceError ? <p role="alert" className="text-sm text-muted-foreground">{attendanceError}</p> : <div className="table-wrap"><table className="data"><thead><tr><th>比赛</th><th>选手</th><th>学号</th><th>进入方式</th><th>设备</th><th>登录时间</th></tr></thead><tbody>
          {attendance.map((event) => <tr key={event.event_id}><td>#{event.contest_id}</td><td>{event.student_name}</td><td>{event.student_number}</td><td>{event.access_kind === "local" ? "本机验证" : event.access_kind === "exempt" ? "远程赦免" : "允许远程"}</td><td>{event.device_id ? `#${event.device_id}` : "—"}</td><td>{new Date(event.logged_in_at * 1000).toLocaleString("zh-CN")}</td></tr>)}
          {!attendance.length && <tr><td colSpan={6}><Empty>暂无比赛登录签到记录</Empty></td></tr>}
        </tbody></table></div>}
      </section>
      <Dialog open={form != null} onOpenChange={(open) => { if (!open) setForm(null); }} title={`签到 #${form ?? ""}`}>
        <div className="space-y-3">
          <Input placeholder="姓名" value={name} onChange={(event) => setName(event.target.value)} />
          <Input placeholder="学号" value={num} onChange={(event) => setNum(event.target.value)} />
          <Button onClick={async () => {
            if (!name.trim() || !num.trim()) return app.toast("请填写姓名和学号", "bad");
            await api(`/api/checkin/${form}/checkin`, { method: "POST", body: JSON.stringify({ student_name: name.trim(), student_num: num.trim() }) });
            setForm(null);
            reload();
          }}>确认签到</Button>
        </div>
      </Dialog>
      <Dialog open={swapFrom != null} onOpenChange={(open) => { if (!open) setSwapFrom(null); }} title={`把 #${swapFrom ?? ""} 的签到换到`}>
        <div className="space-y-3">
          <Input placeholder="目标设备编号" value={swapTo} onChange={(event) => setSwapTo(event.target.value)} />
          <Button onClick={async () => {
            const to = Number(swapTo);
            if (!to) return app.toast("请填写目标编号", "bad");
            await api("/api/checkin/swap", { method: "POST", body: JSON.stringify({ from_assigned_id: swapFrom, to_assigned_id: to }) });
            setSwapFrom(null);
            reload();
          }}>交换</Button>
        </div>
      </Dialog>
    </div>
  );
}

function Stats({ stats }: { stats: { total: number; checked_in: number; checked_out: number; not_checked: number } }) {
  return (
    <div className="mb-4 grid grid-cols-2 gap-3 lg:grid-cols-4">
      <Metric label="设备" value={stats.total} />
      <Metric label="已签到" value={stats.checked_in} />
      <Metric label="未签到" value={stats.not_checked} />
      <Metric label="已签退" value={stats.checked_out} />
    </div>
  );
}

function exportCheckin(rooms: { name: string; devices: Device[] }[]) {
  const rows = [["机房", "编号", "选手", "学号", "状态"]];
  rooms.forEach((room) => room.devices.forEach((device) => rows.push([room.name, String(device.assigned_id), device.student_name || "", device.student_num || "", checkinLabel(device.checkin_status)])));
  const link = document.createElement("a");
  link.href = URL.createObjectURL(new Blob(["\uFEFF" + rows.map((row) => row.map((value) => `"${value}"`).join(",")).join("\r\n")], { type: "text/csv" }));
  link.download = "checkin.csv";
  link.click();
}
