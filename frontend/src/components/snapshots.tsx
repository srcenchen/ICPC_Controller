import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { Button, Card, Empty, Input } from "@/components/ui";
import { useApp } from "@/state";

type Snap = { id: number; name: string; created_at: string; op_count: number };
type Op = { id: number; kind: string; summary: string; created_at: string; delivered: number };

const KINDS: Record<string, string> = { command: "命令", distribute: "分发", wol: "唤醒", schedule: "电源计划", broadcast: "广播" };

export function Snapshots() {
  const app = useApp();
  const [active, setActive] = useState<Snap | null>(null);
  const [ops, setOps] = useState<Op[]>([]);
  const [name, setName] = useState("");
  const [scope, setScope] = useState("");

  async function load() {
    const data = await api<{ active: Snap | null; scope: string }>("/api/snapshots");
    setActive(data.active);
    setScope(data.scope);
    if (data.active) setOps(await api<Op[]>(`/api/snapshots/${data.active.id}/ops`));
    else setOps([]);
  }
  useEffect(() => { load().catch(() => {}); }, [app.roomID, app.page]);
  useEffect(() => app.onAdmin((event) => { if (event.event === "snapshot_updated") load().catch(() => {}); }), [app]);

  return (
    <Card>
      <div className="mb-2 flex items-center justify-between gap-2">
        <h2 className="font-semibold">操作快照</h2>
        <span className="text-xs text-muted-foreground">{scope === "cloud" ? "云端队列" : "本机房队列"} · 只记录对全体目标的操作，之后上线的设备会补执行</span>
      </div>
      {!active ? (
        <div className="flex flex-wrap gap-2">
          <Input className="max-w-xs" placeholder="例如：2026 校赛" value={name} onChange={(event) => setName(event.target.value)} />
          <Button onClick={async () => {
            if (!name.trim()) return app.toast("请填写快照名称", "bad");
            if (!(await app.confirm("启用后，之后的全员操作会进入队列，并补发给后上线的设备。继续？"))) return;
            await api("/api/snapshots", { method: "POST", body: JSON.stringify({ name: name.trim() }) });
            setName("");
            app.toast("快照已启用", "ok");
            load();
          }}>启用</Button>
        </div>
      ) : (
        <>
          <div className="mb-2 flex flex-wrap items-center gap-2 text-sm">
            <strong>{active.name}</strong>
            <span className="text-muted-foreground">{active.created_at} · {active.op_count} 条</span>
            <Button size="sm" variant="outline" onClick={() => load()}>刷新</Button>
            <Button size="sm" variant="destructive" onClick={async () => {
              if (!(await app.confirm("结束后，新上线设备不再补执行这些操作。"))) return;
              await api(`/api/snapshots/${active.id}/end`, { method: "POST" });
              load();
            }}>结束</Button>
          </div>
          <div className="table-wrap max-h-64">
            <table className="data">
              <thead><tr><th>类型</th><th>内容</th><th>已送达</th><th></th></tr></thead>
              <tbody>
                {ops.map((op) => (
                  <tr key={op.id}>
                    <td>{KINDS[op.kind] || op.kind}</td>
                    <td className="max-w-md truncate">{op.summary}</td>
                    <td>{op.delivered}</td>
                    <td><Button size="sm" variant="ghost" onClick={async () => {
                      if (!(await app.confirm("从队列移除后，之后上线的设备不再执行它。"))) return;
                      await api(`/api/snapshots/${active.id}/ops/${op.id}`, { method: "DELETE" });
                      load();
                    }}>移除</Button></td>
                  </tr>
                ))}
                {!ops.length && <tr><td colSpan={4}><Empty>队列还是空的</Empty></td></tr>}
              </tbody>
            </table>
          </div>
        </>
      )}
    </Card>
  );
}
