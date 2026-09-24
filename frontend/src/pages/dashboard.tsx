import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { statusLabel, timeAgo } from "@/lib/utils";
import type { CommandLog } from "@/lib/types";
import { Badge, Card, Empty, statusTone } from "@/components/ui";
import { Metric } from "@/components/targets";
import { Snapshots } from "@/components/snapshots";
import { useApp } from "@/state";

export function Dashboard() {
  const app = useApp();
  const [stats, setStats] = useState({ total_devices: 0, online_devices: 0, offline_devices: 0, checked_in: 0, total_commands: 0, recent_commands: [] as CommandLog[] });
  useEffect(() => {
    if (app.cloudAll) return;
    api<typeof stats>("/api/stats").then(setStats).catch((err) => app.toast(err.message, "bad"));
  }, [app.cloudAll, app.roomID]);

  if (app.cloudAll) {
    const total = app.rooms.reduce((sum, room) => sum + room.devices.length, 0);
    const online = app.rooms.reduce((sum, room) => sum + room.devices.filter((device) => device.connected).length, 0);
    const checked = app.rooms.reduce((sum, room) => sum + room.devices.filter((device) => device.checkin_status === 1).length, 0);
    const commands = app.rooms.reduce((sum, room) => sum + (room.total_commands || 0), 0);
    const recent = app.rooms.flatMap((room) => (room.recent_commands || []).map((command) => ({ ...command, room: room.name }))).sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at)).slice(0, 12);
    return (
      <div className="space-y-4">
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <Metric label="设备" value={total} onClick={() => app.go("devices")} />
          <Metric label="在线" value={online} onClick={() => app.go("devices")} />
          <Metric label="已签到" value={checked} onClick={() => app.go("checkin")} />
          <Metric label="命令" value={commands} onClick={() => app.go("commands")} />
        </div>
        <Card>
          <h2 className="mb-2 font-semibold">各机房最近命令</h2>
          <div className="table-wrap max-h-80">
            <table className="data">
              <thead><tr><th>机房</th><th>命令</th><th>状态</th><th>时间</th></tr></thead>
              <tbody>
                {recent.map((command) => (
                  <tr key={command.room + command.id}>
                    <td>{command.room}</td>
                    <td className="max-w-md truncate font-mono text-xs">{command.command}</td>
                    <td><Badge tone={statusTone(command.status)}>{statusLabel(command.status)}</Badge></td>
                    <td className="text-xs text-muted-foreground">{timeAgo(command.created_at)}</td>
                  </tr>
                ))}
                {!recent.length && <tr><td colSpan={4}><Empty>暂无命令</Empty></td></tr>}
              </tbody>
            </table>
          </div>
        </Card>
        <Snapshots />
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Metric label="设备" value={stats.total_devices} onClick={() => app.go("devices")} />
        <Metric label="在线" value={stats.online_devices} onClick={() => app.go("devices")} />
        <Metric label="离线" value={stats.offline_devices} />
        <Metric label="已签到" value={stats.checked_in} onClick={() => app.go("checkin")} />
      </div>
      <Card>
        <div className="mb-2 flex items-center justify-between">
          <h2 className="font-semibold">最近命令</h2>
          <button className="text-sm text-primary" onClick={() => app.go("commands")}>去执行</button>
        </div>
        <div className="table-wrap">
          <table className="data">
            <thead><tr><th>时间</th><th>目标</th><th>命令</th><th>状态</th></tr></thead>
            <tbody>
              {(stats.recent_commands || []).map((command) => (
                <tr key={command.id}>
                  <td className="text-xs text-muted-foreground">{timeAgo(command.created_at)}</td>
                  <td>{command.target_type === "broadcast" ? "全部在线" : command.target_type === "list" ? "指定设备" : "#" + command.target_id}</td>
                  <td className="max-w-md truncate font-mono text-xs">{command.command}</td>
                  <td><Badge tone={statusTone(command.status)}>{statusLabel(command.status)}</Badge></td>
                </tr>
              ))}
              {!stats.recent_commands?.length && <tr><td colSpan={4}><Empty>还没有命令记录</Empty></td></tr>}
            </tbody>
          </table>
        </div>
      </Card>
      <Snapshots />
    </div>
  );
}
