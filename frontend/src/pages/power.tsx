import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { Button, Card, Input, PageHeader, Select } from "@/components/ui";
import { TargetPicker } from "@/components/targets";
import { FleetTable, Jobs, queueFleet, useFleetDevices } from "@/components/fleet";
import { useApp, useDevices } from "@/state";

type Schedule = { id: number; action: string; run_at: string; target_type: string; target_ids?: number[]; note: string; status: string };

export function Power() {
  const app = useApp();
  const fleet = useFleetDevices();
  const { devices } = useDevices();
  const [mode, setMode] = useState<"online" | "picked">("online");
  const [picked, setPicked] = useState<number[]>([]);
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [action, setAction] = useState("shutdown");
  const [when, setWhen] = useState("");
  const [note, setNote] = useState("");

  function load() { if (!app.cloudAll) api<Schedule[]>("/api/power/schedules").then(setSchedules).catch(() => {}); }
  useEffect(() => { load(); }, [app.roomID, app.cloudAll]);

  function ids() {
    return mode === "picked" ? picked : devices.map((device) => device.assigned_id);
  }

  return (
    <div className="space-y-4">
      <PageHeader title="电源" description="唤醒使用网卡魔术包。关机和重启是发给选手机的命令。定时计划保存在当前机房。" />
      {app.cloudAll ? <FleetTable /> : (
        <Card className="h-[300px]"><TargetPicker devices={devices} mode={mode} onMode={setMode} selected={picked} onToggle={(id) => setPicked((list) => list.includes(id) ? list.filter((item) => item !== id) : [...list, id])} onSet={setPicked} /></Card>
      )}
      <Card>
        <h2 className="mb-2 font-semibold">立即操作</h2>
        <div className="flex flex-wrap gap-2">
          <Button onClick={() => wake().catch((err) => app.toast(err.message, "bad"))}>唤醒</Button>
          <Button variant="destructive" onClick={() => powerCommand("shutdown -h now").catch((err) => app.toast(err.message, "bad"))}>关机</Button>
          <Button variant="outline" onClick={() => powerCommand("reboot").catch((err) => app.toast(err.message, "bad"))}>重启</Button>
        </div>
      </Card>
      <Card>
        <h2 className="mb-2 font-semibold">定时</h2>
        <div className="grid gap-3 md:grid-cols-3">
          <Select value={action} onChange={(event) => setAction(event.target.value)}>
            <option value="shutdown">关机</option>
            <option value="reboot">重启</option>
            <option value="wol">唤醒</option>
          </Select>
          <Input type="datetime-local" value={when} onChange={(event) => setWhen(event.target.value)} />
          <Input placeholder="备注" value={note} onChange={(event) => setNote(event.target.value)} />
        </div>
        <Button className="mt-3" onClick={async () => {
          if (!when) return app.toast("请选择时间", "bad");
          if (app.cloudAll) {
            if (!fleet.fleet.length) return app.toast("请选择设备", "bad");
            await queueFleet(fleet.fleet, "schedule", { action, run_at: new Date(when).toISOString(), note });
            app.toast("计划已入队", "ok");
            return;
          }
          const targetIDs = ids();
          if (mode === "picked" && !targetIDs.length) return app.toast("请指定设备", "bad");
          await api("/api/power/schedules", { method: "POST", body: JSON.stringify({ action, run_at: when, note, target_type: mode === "online" ? "all" : "list", device_ids: mode === "online" ? [] : targetIDs }) });
          app.toast("已添加计划", "ok");
          load();
        }}>创建计划</Button>
        {!app.cloudAll && (
          <div className="table-wrap mt-3">
            <table className="data">
              <thead><tr><th>时间</th><th>动作</th><th>目标</th><th>状态</th><th></th></tr></thead>
              <tbody>
                {schedules.map((item) => (
                  <tr key={item.id}>
                    <td>{item.run_at}</td>
                    <td>{item.action}</td>
                    <td>{item.target_type === "all" ? "全部" : (item.target_ids || []).map((id) => "#" + id).join(" ")}</td>
                    <td>{item.status}{item.note ? " · " + item.note : ""}</td>
                    <td><Button size="sm" variant="ghost" onClick={() => api("/api/power/schedules/" + item.id, { method: "DELETE" }).then(load)}>删除</Button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      {app.cloudAll && <Jobs />}
    </div>
  );

  async function wake() {
    if (app.cloudAll) {
      if (!fleet.fleet.length) return app.toast("请选择设备", "bad");
      await queueFleet(fleet.fleet, "wol");
      app.toast("唤醒已入队", "ok");
      return;
    }
    const body = mode === "online" ? { target_type: "all" } : { target_type: "list", device_ids: picked };
    if (mode === "picked" && !picked.length) return app.toast("请指定设备", "bad");
    const result = await api<{ sent: number; total: number }>("/api/power/wol", { method: "POST", body: JSON.stringify(body) });
    app.toast(`已发送 ${result.sent}/${result.total} 个唤醒包`, "ok");
  }

  async function powerCommand(command: string) {
    if (app.cloudAll) {
      if (!fleet.fleet.length) return app.toast("请选择设备", "bad");
      if (!(await app.confirm("向已选设备执行 " + command + " ？"))) return;
      await queueFleet(fleet.fleet, "command", { command });
      app.toast("已入队", "ok");
      return;
    }
    if (!(await app.confirm("对" + (mode === "online" ? "全部在线设备" : picked.length + " 台设备") + "执行 " + command + " ？"))) return;
    const body = mode === "online" ? { target_type: "broadcast", command } : { target_type: "list", target_ids: picked, command };
    await api("/api/commands", { method: "POST", body: JSON.stringify(body) });
    app.toast("已派发", "ok");
  }
}
