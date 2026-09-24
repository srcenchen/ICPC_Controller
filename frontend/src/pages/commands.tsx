import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "@/lib/api";
import { statusLabel } from "@/lib/utils";
import type { CommandLog, Preset } from "@/lib/types";
import { Badge, Button, Card, Empty, PageHeader, statusTone, Textarea } from "@/components/ui";
import { TargetPicker } from "@/components/targets";
import { FleetTable, Jobs, queueFleet, useFleetDevices } from "@/components/fleet";
import { useApp, useDevices } from "@/state";

type Row = { deviceID: number; commandID: number; status: string; output: string; duration: number };

export function Commands() {
  const app = useApp();
  if (app.cloudAll) return <CloudCommands />;
  return <RoomCommands />;
}

function RoomCommands() {
  const app = useApp();
  const { devices } = useDevices();
  const [presets, setPresets] = useState<Preset[]>([]);
  const [command, setCommand] = useState("hostname && uptime");
  const [mode, setMode] = useState<"online" | "picked">("online");
  const [picked, setPicked] = useState<number[]>([]);
  const [rows, setRows] = useState<Record<number, Row>>({});
  const known = useRef<Record<number, number>>({});
  const [active, setActive] = useState<number | null>(null);
  const [history, setHistory] = useState<CommandLog[]>([]);
  const [detail, setDetail] = useState<CommandLog | null>(null);
  const [busy, setBusy] = useState(false);

  function loadHistory() {
    api<CommandLog[]>("/api/commands?limit=30").then(setHistory).catch(() => {});
  }
  useEffect(() => { api<Preset[]>("/api/presets").then(setPresets).catch(() => {}); loadHistory(); }, [app.roomID]);

  useEffect(() => app.onAdmin((event) => {
    const data = event.data || {};
    const commandID = Number(data.command_id || 0);
    const deviceID = Number(data.device_id || 0);
    if (!commandID || known.current[commandID] == null) return;
    if (event.event === "command_output") {
      setRows((current) => {
        const row = current[deviceID] || { deviceID, commandID, status: "running", output: "", duration: 0 };
        return { ...current, [deviceID]: { ...row, output: row.output + String(data.line || "") + "\n", status: row.status === "dispatched" ? "running" : row.status } };
      });
    }
    if (event.event === "command_result") {
      setRows((current) => {
        const row = current[deviceID] || { deviceID, commandID, status: "running", output: "", duration: 0 };
        const extra = data.error_output ? String(data.error_output) + "\n" : "";
        return { ...current, [deviceID]: { ...row, status: String(data.status || "completed"), output: row.output + extra, duration: Number(data.duration_ms || 0) } };
      });
      loadHistory();
    }
  }), [app]);

  const list = useMemo(() => Object.values(rows).sort((a, b) => a.deviceID - b.deviceID), [rows]);
  const done = list.filter((row) => ["completed", "failed", "timeout"].includes(row.status)).length;

  async function run(ids?: number[]) {
    const text = command.trim();
    if (!text) return app.toast("请输入命令", "bad");
    const targets = ids || (mode === "online" ? devices.filter((device) => device.connected).map((device) => device.assigned_id) : picked);
    if (!targets.length) return app.toast(mode === "online" ? "没有在线设备" : "请先指定设备", "bad");
    setBusy(true);
    try {
      const body = mode === "online" && !ids
        ? { target_type: "broadcast", command: text }
        : { target_type: "list", target_ids: targets, command: text };
      const created = await api<CommandLog>("/api/commands", { method: "POST", body: JSON.stringify(body) });
      const nextRows: Record<number, Row> = {};
      const nextKnown: Record<number, number> = {};
      (created.children || []).forEach((child) => {
        if (child.target_id == null) return;
        nextRows[child.target_id] = { deviceID: child.target_id, commandID: child.id, status: child.status, output: child.error_output || "", duration: 0 };
        nextKnown[child.id] = child.target_id;
      });
      setRows(nextRows);
      known.current = nextKnown;
      setActive(null);
      app.toast(`已一次派发到 ${created.children?.length || targets.length} 台`, "ok");
      loadHistory();
    } catch (err) {
      app.toast(err instanceof Error ? err.message : "执行失败", "bad");
    } finally {
      setBusy(false);
    }
  }

  const failed = list.filter((row) => row.status === "failed" || row.status === "timeout").map((row) => row.deviceID);

  return (
    <div>
      <PageHeader title="命令" description="全部在线走一条广播；指定多台也只发一次请求，服务端一次性写入并投递。" />
      <div className="grid gap-4 lg:grid-cols-[340px_minmax(0,1fr)]">
        <Card className="flex min-h-[520px] flex-col">
          <TargetPicker devices={devices} mode={mode} onMode={setMode} selected={picked} onToggle={(id) => setPicked((list) => list.includes(id) ? list.filter((item) => item !== id) : [...list, id])} onSet={setPicked} />
        </Card>
        <div className="space-y-4">
          <Card>
            <div className="mb-2 flex flex-wrap gap-2">
              {presets.map((preset) => <Button key={preset.name} size="sm" variant="outline" title={preset.desc} onClick={() => setCommand(preset.command)}>{preset.name}</Button>)}
            </div>
            <Textarea rows={7} value={command} onChange={(event) => setCommand(event.target.value)} spellCheck={false} />
            <div className="mt-3 flex flex-wrap gap-2">
              <Button disabled={busy} onClick={() => run()}>{busy ? "正在派发…" : "执行"}</Button>
              <Button variant="outline" disabled={!failed.length} onClick={() => run(failed)}>重跑失败的 {failed.length || ""}</Button>
            </div>
          </Card>
          <Card>
            <div className="mb-2 flex items-center justify-between text-sm">
              <span className="font-semibold">本次结果</span>
              <span className="text-muted-foreground">{list.length ? `${done} / ${list.length} 结束` : "等待执行"}</span>
            </div>
            <div className="table-wrap max-h-72">
              <table className="data">
                <thead><tr><th>设备</th><th>状态</th><th>摘要</th><th>耗时</th></tr></thead>
                <tbody>
                  {list.map((row) => (
                    <tr key={row.deviceID} className={active === row.deviceID ? "active cursor-pointer" : "cursor-pointer"} onClick={() => setActive(active === row.deviceID ? null : row.deviceID)}>
                      <td className="font-mono">#{row.deviceID}</td>
                      <td><Badge tone={statusTone(row.status)}>{statusLabel(row.status)}</Badge></td>
                      <td className="max-w-xs truncate font-mono text-xs">{row.output.trim().split("\n").pop() || "—"}</td>
                      <td>{row.duration ? row.duration + " ms" : "—"}</td>
                    </tr>
                  ))}
                  {!list.length && <tr><td colSpan={4}><Empty>执行后这里按设备列出输出</Empty></td></tr>}
                </tbody>
              </table>
            </div>
            {active != null && rows[active] && (
              <div className="mt-3">
                <div className="mb-1 flex items-center justify-between text-xs text-muted-foreground">
                  <span>#{active} 完整输出</span>
                  {rows[active].status === "running" || rows[active].status === "dispatched" ? <Button size="sm" variant="destructive" onClick={() => api("/api/commands/" + rows[active].commandID + "/cancel", { method: "POST" })}>终止</Button> : null}
                </div>
                <pre className="max-h-48 overflow-auto rounded-md bg-muted p-3 font-mono text-xs whitespace-pre-wrap">{rows[active].output || "等待输出"}</pre>
              </div>
            )}
          </Card>
        </div>
      </div>
      <Card className="mt-4">
        <div className="mb-2 flex items-center justify-between">
          <h2 className="font-semibold">历史</h2>
          <Button size="sm" variant="ghost" onClick={async () => { if (await app.confirm("清空全部命令历史？")) { await api("/api/commands/clear", { method: "POST" }); loadHistory(); } }}>清空</Button>
        </div>
        <History history={history} onOpen={async (id) => setDetail(await api("/api/commands/" + id))} />
        {detail && <CommandDetail command={detail} onClose={() => setDetail(null)} />}
      </Card>
    </div>
  );
}

function CloudCommands() {
  const app = useFleetDevices();
  const [presets, setPresets] = useState<Preset[]>([]);
  const [command, setCommand] = useState("");
  const [history, setHistory] = useState<(CommandLog & { room?: string })[]>([]);
  useEffect(() => { api<Preset[]>("/api/presets").then(setPresets).catch(() => {}); }, []);
  useEffect(() => {
    setHistory(app.rooms.flatMap((room) => (room.recent_commands || []).map((command) => ({ ...command, room: room.name }))).sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at)).slice(0, 40));
  }, [app.rooms]);
  return (
    <div>
      <PageHeader title="跨机房命令" description="每个机房只收到一条任务，里面包含该机房全部选中设备。离线中转恢复后自动投递。" />
      <FleetTable />
      <Card>
        <div className="mb-2 flex flex-wrap gap-2">{presets.map((preset) => <Button key={preset.name} size="sm" variant="outline" onClick={() => setCommand(preset.command)}>{preset.name}</Button>)}</div>
        <Textarea rows={6} value={command} onChange={(event) => setCommand(event.target.value)} placeholder="hostname && uptime" />
        <Button className="mt-3" onClick={async () => {
          if (!command.trim()) return app.toast("请输入命令", "bad");
          if (!app.fleet.length) return app.toast("请先选择设备", "bad");
          if (!(await app.confirm(`向 ${app.fleet.length} 台设备执行？每个机房只投递一次。`))) return;
          await queueFleet(app.fleet, "command", { command });
          app.toast("已入队", "ok");
        }}>向已选设备执行</Button>
        <Jobs />
      </Card>
      <Card className="mt-4">
        <h2 className="mb-2 font-semibold">各机房最近命令</h2>
        <History history={history} onOpen={() => {}} />
      </Card>
    </div>
  );
}

function History({ history, onOpen }: { history: (CommandLog & { room?: string })[]; onOpen: (id: number) => void }) {
  return (
    <div className="table-wrap max-h-80">
      <table className="data">
        <thead><tr><th>时间</th>{history.some((item) => item.room) && <th>机房</th>}<th>目标</th><th>命令</th><th>状态</th><th>耗时</th></tr></thead>
        <tbody>
          {history.map((command) => (
            <tr key={(command.room || "") + command.id} className="cursor-pointer" onClick={() => onOpen(command.id)}>
              <td className="whitespace-nowrap text-xs">{command.created_at}</td>
              {history.some((item) => item.room) && <td>{command.room}</td>}
              <td>{command.target_type === "broadcast" ? "全部在线" : command.target_type === "list" ? "指定多台" : "#" + (command.target_id ?? "")}</td>
              <td className="max-w-sm truncate font-mono text-xs">{command.command}</td>
              <td><Badge tone={statusTone(command.status)}>{statusLabel(command.status)}</Badge></td>
              <td>{command.duration_ms ? command.duration_ms + " ms" : "—"}</td>
            </tr>
          ))}
          {!history.length && <tr><td colSpan={6}><Empty>暂无记录</Empty></td></tr>}
        </tbody>
      </table>
    </div>
  );
}

function CommandDetail({ command, onClose }: { command: CommandLog; onClose: () => void }) {
  return (
    <div className="mt-3 rounded-md border border-border p-3">
      <div className="mb-2 flex items-center justify-between">
        <strong>#{command.id}</strong>
        <Button size="sm" variant="ghost" onClick={onClose}>收起</Button>
      </div>
      <pre className="max-h-48 overflow-auto whitespace-pre-wrap font-mono text-xs">{command.output || command.error_output || "没有汇总输出"}</pre>
      {!!command.children?.length && (
        <div className="table-wrap mt-2 max-h-64">
          <table className="data">
            <thead><tr><th>设备</th><th>状态</th><th>输出</th></tr></thead>
            <tbody>
              {command.children.map((child) => (
                <tr key={child.id}>
                  <td>#{child.target_id}</td>
                  <td><Badge tone={statusTone(child.status)}>{statusLabel(child.status)}</Badge></td>
                  <td className="max-w-lg whitespace-pre-wrap font-mono text-xs">{child.output || child.error_output}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
