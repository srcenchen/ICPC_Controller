import { useMemo, useState } from "react";
import type { Device } from "@/lib/types";
import { checkinLabel, parseIP } from "@/lib/utils";
import { Badge, Input } from "@/components/ui";

export function TargetPicker({
  devices,
  mode,
  onMode,
  selected,
  onToggle,
  onSet,
}: {
  devices: Device[];
  mode: "online" | "picked";
  onMode: (mode: "online" | "picked") => void;
  selected: number[];
  onToggle: (id: number) => void;
  onSet: (ids: number[]) => void;
}) {
  const [query, setQuery] = useState("");
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return devices.filter((device) => !q || [device.hostname, device.student_name, device.student_num, String(device.assigned_id), parseIP(device.local_ip)].join(" ").toLowerCase().includes(q));
  }, [devices, query]);
  const online = shown.filter((device) => device.connected);
  return (
    <div className="flex h-full min-h-0 flex-col gap-2">
      <div className="grid grid-cols-2 rounded-md bg-muted p-1 text-xs">
        <button className={"rounded px-2 py-1.5 " + (mode === "online" ? "bg-card shadow-sm" : "")} onClick={() => onMode("online")}>全部在线 · {devices.filter((d) => d.connected).length}</button>
        <button className={"rounded px-2 py-1.5 " + (mode === "picked" ? "bg-card shadow-sm" : "")} onClick={() => onMode("picked")}>指定设备 · {selected.length}</button>
      </div>
      <Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索编号、主机名、选手" />
      {mode === "picked" && (
        <div className="flex gap-2 text-xs">
          <button className="text-primary" onClick={() => onSet(online.map((device) => device.assigned_id))}>选中筛选内在线</button>
          <button className="text-primary" onClick={() => onSet(shown.map((device) => device.assigned_id))}>选中筛选结果</button>
          <button className="text-muted-foreground" onClick={() => onSet([])}>清空</button>
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-auto rounded-md border border-border">
        {shown.map((device) => (
          <label key={device.assigned_id} className="flex cursor-pointer items-center gap-2 border-b border-border px-2 py-1.5 text-sm last:border-0 hover:bg-muted/60">
            {mode === "picked" && <input type="checkbox" checked={selected.includes(device.assigned_id)} onChange={() => onToggle(device.assigned_id)} />}
            <span className="w-10 font-mono font-semibold">#{device.assigned_id}</span>
            <span className="min-w-0 flex-1 truncate">{device.hostname || "未命名"}</span>
            <span className="hidden truncate text-xs text-muted-foreground sm:inline">{device.student_name || checkinLabel(device.checkin_status)}</span>
            <Badge tone={device.connected ? "ok" : "muted"}>{device.connected ? "在线" : "离线"}</Badge>
          </label>
        ))}
        {!shown.length && <div className="p-6 text-center text-sm text-muted-foreground">没有匹配的设备</div>}
      </div>
    </div>
  );
}

export function Metric({ label, value, onClick }: { label: string; value: number | string; onClick?: () => void }) {
  const Tag = onClick ? "button" : "div";
  return (
    <Tag onClick={onClick} className="rounded-xl border border-border bg-card p-4 text-left shadow-sm">
      <div className="text-2xl font-semibold tabular-nums tracking-tight">{value}</div>
      <div className="mt-1 text-xs text-muted-foreground">{label}</div>
    </Tag>
  );
}
