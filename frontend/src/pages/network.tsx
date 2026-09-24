import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import type { NetRule } from "@/lib/types";
import { Button, Card, Input, PageHeader, Select } from "@/components/ui";
import { TargetPicker } from "@/components/targets";
import { FleetTable, Jobs, queueFleet, useFleetDevices } from "@/components/fleet";
import { useApp, useDevices } from "@/state";

const TYPES = [
  { value: "DOMAIN-SUFFIX", label: "域名后缀" },
  { value: "DOMAIN-KEYWORD", label: "关键字" },
  { value: "DOMAIN", label: "完整域名" },
];

export function Network() {
  const app = useApp();
  const { devices } = useDevices();
  const fleet = useFleetDevices();
  const [rules, setRules] = useState<NetRule[]>([]);
  const [mode, setMode] = useState<"online" | "picked">("online");
  const [picked, setPicked] = useState<number[]>([]);
  const [log, setLog] = useState("规则保存在本机。下发时才会作用到选手机。");

  useEffect(() => { api<NetRule[]>("/api/network/rules").then((list) => setRules(list || [])).catch((err) => app.toast(err.message, "bad")); }, [app.roomID]);

  async function save(next = rules) {
    const clean = next.map((rule) => ({ type: rule.type, value: rule.value.trim() })).filter((rule) => rule.value);
    setRules(await api("/api/network/rules", { method: "PUT", body: JSON.stringify(clean) }));
  }

  async function act(kind: "apply" | "remove") {
    if (app.cloudAll) {
      if (!fleet.fleet.length) return app.toast("请先选择设备", "bad");
      if (kind === "apply") await save();
      if (!(await app.confirm(kind === "apply" ? "把当前白名单应用到已选设备？" : "解除已选设备的网络限制？"))) return;
      await queueFleet(fleet.fleet, kind === "apply" ? "network_apply" : "network_remove", kind === "apply" ? { rules } : {});
      app.toast("已入队", "ok");
      return;
    }
    const ids = mode === "online" ? devices.filter((device) => device.connected).map((device) => device.assigned_id) : picked;
    if (!ids.length) return app.toast("没有目标设备", "bad");
    if (kind === "apply") await save();
    const body = mode === "online" ? { target_type: "broadcast" } : { target_type: "list", target_ids: ids };
    const result = await api<{ id: number; children?: { length: number } }>(`/api/network/${kind}`, { method: "POST", body: JSON.stringify(body) });
    setLog(`#${result.id} 已一次派发${mode === "online" ? "到全部在线设备" : `到 ${ids.length} 台`}`);
    app.toast("已下发", "ok");
  }

  return (
    <div className="space-y-4">
      <PageHeader title="网络白名单" description="只有匹配规则的域名可以访问外网，其余拦截。先保存规则，再选择目标下发。" />
      <Card>
        <div className="space-y-2">
          {rules.map((rule, index) => (
            <div key={index} className="grid grid-cols-[160px_minmax(0,1fr)_auto] gap-2">
              <Select value={rule.type} onChange={(event) => setRules(rules.map((item, i) => i === index ? { ...item, type: event.target.value } : item))}>
                {TYPES.map((type) => <option key={type.value} value={type.value}>{type.label}</option>)}
              </Select>
              <Input value={rule.value} placeholder="例如 baidu.com" onChange={(event) => setRules(rules.map((item, i) => i === index ? { ...item, value: event.target.value } : item))} onBlur={() => save().catch((err) => app.toast(err.message, "bad"))} />
              <Button variant="ghost" onClick={() => save(rules.filter((_, i) => i !== index)).catch((err) => app.toast(err.message, "bad"))}>删除</Button>
            </div>
          ))}
          {!rules.length && <p className="text-sm text-muted-foreground">还没有规则。没有规则时下发，等于几乎全部拦截。</p>}
        </div>
        <Button className="mt-3" variant="outline" onClick={() => setRules([...rules, { type: "DOMAIN-SUFFIX", value: "" }])}>添加规则</Button>
      </Card>
      {app.cloudAll ? <FleetTable /> : (
        <Card className="h-[360px]">
          <TargetPicker devices={devices} mode={mode} onMode={setMode} selected={picked} onToggle={(id) => setPicked((list) => list.includes(id) ? list.filter((item) => item !== id) : [...list, id])} onSet={setPicked} />
        </Card>
      )}
      <div className="flex flex-wrap gap-2">
        <Button variant="destructive" onClick={() => act("apply").catch((err) => app.toast(err.message, "bad"))}>应用限制</Button>
        <Button variant="outline" onClick={() => act("remove").catch((err) => app.toast(err.message, "bad"))}>解除限制</Button>
      </div>
      <p className="text-sm text-muted-foreground">{log}</p>
      {app.cloudAll && <Jobs />}
    </div>
  );
}
