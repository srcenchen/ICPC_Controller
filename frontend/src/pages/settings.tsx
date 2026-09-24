import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import type { Deployment, Preset, Settings } from "@/lib/types";
import { Button, Card, Input, PageHeader, Select, Textarea } from "@/components/ui";
import { useApp } from "@/state";

export function SettingsPage() {
  const app = useApp();
  const [settings, setSettings] = useState<Settings | null>(null);
  useEffect(() => { api<Settings>("/api/settings").then(setSettings).catch((err) => app.toast(err.message, "bad")); }, [app.roomID]);
  if (!settings) return <p className="text-sm text-muted-foreground">正在加载设置…</p>;
  return (
    <div className="mx-auto max-w-3xl space-y-4">
      <PageHeader title="设置" description="部署、主机名和密码属于当前这台服务器。选中某个机房时，签到文案和预设命令会写到那个机房。" />
      <DeploymentForm initial={settings.deployment || app.deployment} />
      <Card>
        <h2 className="font-semibold">数据</h2>
        <p className="mb-3 text-sm text-muted-foreground">备份含管理员凭据和机房密钥。恢复时先停服务，再执行 <span className="font-mono">./server --db icpc.db --restore backup.zip</span>。</p>
        <div className="flex flex-wrap gap-2">
          <a className="inline-flex h-9 items-center rounded-md border border-border px-3 text-sm" href="/api/data/export">业务 JSON</a>
          <a className="inline-flex h-9 items-center rounded-md border border-border px-3 text-sm" href="/api/devices/export">设备 Excel</a>
          <a className="inline-flex h-9 items-center rounded-md border border-border px-3 text-sm" href="/api/checkin/export">签到 Excel</a>
          <a className="inline-flex h-9 items-center rounded-md bg-primary px-3 text-sm text-primary-foreground" href="/api/data/backup">下载备份</a>
          <a className="inline-flex h-9 items-center rounded-md border border-border px-3 text-sm" href="/api/data/backup?uploads=true">含分发文件</a>
        </div>
      </Card>
      <Prefix prefix={settings.hostname_prefix} />
      <CheckinForm config={settings.checkin_config} />
      <Presets initial={settings.presets || []} />
      <Maintenance initial={settings.maintenance} />
      <Card>
        <h2 className="font-semibold">客户端更新</h2>
        <p className="mb-3 text-sm text-muted-foreground">先在分发页上传名为 icpc-client 的新二进制，再推送给在线选手机。</p>
        <Button variant="warning" onClick={async () => {
          if (!(await app.confirm("向所有在线选手机推送自更新？"))) return;
          const result = await api<{ sent: number }>("/api/client/update", { method: "POST" });
          app.toast(`已推送到 ${result.sent} 台`, "ok");
        }}>推送更新</Button>
      </Card>
      <Password />
    </div>
  );
}

function DeploymentForm({ initial }: { initial: Deployment }) {
  const app = useApp();
  const [form, setForm] = useState({ ...initial, token: "" });
  return (
    <Card>
      <h2 className="font-semibold">部署</h2>
      <p className="mb-3 text-sm text-muted-foreground">同一程序可以是单机、机房中转或云端。中转主动连云端。身份 {initial.node_id || "—"}。</p>
      <div className="grid gap-3">
        <Select value={form.mode} onChange={(event) => setForm({ ...form, mode: event.target.value })}>
          <option value="standalone">单机 · 只管理当前局域网</option>
          <option value="relay">并机 · 机房中转</option>
          <option value="cloud">云端 · 管理全部机房</option>
        </Select>
        <Input placeholder="机房名称" value={form.room_name} onChange={(event) => setForm({ ...form, room_name: event.target.value })} />
        <Input placeholder="云端地址 https://…" value={form.cloud_url} onChange={(event) => setForm({ ...form, cloud_url: event.target.value })} />
        <Input type="password" placeholder={initial.token_set ? "密钥已配置，留空则保持" : "连接密钥，至少 32 字符"} value={form.token} onChange={(event) => setForm({ ...form, token: event.target.value })} />
        <div className="grid grid-cols-2 gap-3">
          <Input type="number" min={1} value={form.device_id_start || 1} onChange={(event) => setForm({ ...form, device_id_start: Number(event.target.value) })} />
          <Input placeholder="中转局域网 IP，可留空" value={form.advertise_ip} onChange={(event) => setForm({ ...form, advertise_ip: event.target.value })} />
        </div>
        <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={form.allow_insecure} onChange={(event) => setForm({ ...form, allow_insecure: event.target.checked })} />仅可信内网允许 HTTP</label>
      </div>
      <div className="mt-3 flex gap-2">
        <Button onClick={async () => {
          if (!(await app.confirm("切换部署可能断开当前连接。继续？"))) return;
          await api("/api/settings", { method: "POST", body: JSON.stringify({ deployment: form }) });
          app.toast("已保存，正在刷新", "ok");
          location.href = "/";
        }}>保存并切换</Button>
        <Button variant="outline" onClick={() => {
          const bytes = new Uint8Array(32);
          crypto.getRandomValues(bytes);
          setForm({ ...form, token: Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("") });
        }}>生成密钥</Button>
      </div>
    </Card>
  );
}

function Prefix({ prefix }: { prefix: string }) {
  const app = useApp();
  const [value, setValue] = useState(prefix);
  return (
    <Card>
      <h2 className="font-semibold">主机名前缀</h2>
      <p className="mb-2 text-sm text-muted-foreground">新注册的选手机会变成 {value || "前缀"}-1。已有机器不会改名。</p>
      <div className="flex gap-2">
        <Input value={value} onChange={(event) => setValue(event.target.value)} />
        <Button onClick={async () => { await api("/api/settings", { method: "POST", body: JSON.stringify({ hostname_prefix: value.trim() }) }); app.toast("已保存", "ok"); }}>保存</Button>
      </div>
    </Card>
  );
}

function CheckinForm({ config }: { config: Settings["checkin_config"] }) {
  const app = useApp();
  const [form, setForm] = useState(config);
  const set = (key: keyof typeof form, value: string) => setForm({ ...form, [key]: value });
  return (
    <Card>
      <h2 className="font-semibold">选手签到页</h2>
      <div className="mt-3 space-y-2">
        <Textarea rows={2} value={form.welcome_text} onChange={(event) => set("welcome_text", event.target.value)} placeholder="欢迎语" />
        <Textarea rows={2} value={form.warning_text} onChange={(event) => set("warning_text", event.target.value)} placeholder="警告" />
        <Input value={form.post_checkin_msg} onChange={(event) => set("post_checkin_msg", event.target.value)} placeholder="签到成功提示" />
        <Input className="font-mono" value={form.post_checkout_cmd} onChange={(event) => set("post_checkout_cmd", event.target.value)} placeholder="签退后执行的命令" />
        <Textarea rows={2} value={form.post_checkout_msg} onChange={(event) => set("post_checkout_msg", event.target.value)} placeholder="签退提示" />
      </div>
      <Button className="mt-3" onClick={async () => { await api("/api/settings/checkin", { method: "PUT", body: JSON.stringify(form) }); app.toast("签到文案已保存", "ok"); }}>保存签到文案</Button>
    </Card>
  );
}

function Presets({ initial }: { initial: Preset[] }) {
  const app = useApp();
  const [rows, setRows] = useState<Preset[]>(initial);
  return (
    <Card>
      <h2 className="font-semibold">预设命令</h2>
      <div className="mt-3 space-y-2">
        {rows.map((row, index) => (
          <div key={index} className="grid gap-2 md:grid-cols-[140px_1fr_auto]">
            <Input value={row.name} placeholder="名称" onChange={(event) => setRows(rows.map((item, i) => i === index ? { ...item, name: event.target.value } : item))} />
            <Input className="font-mono" value={row.command} placeholder="命令" onChange={(event) => setRows(rows.map((item, i) => i === index ? { ...item, command: event.target.value } : item))} />
            <Button variant="ghost" onClick={() => setRows(rows.filter((_, i) => i !== index))}>删除</Button>
          </div>
        ))}
      </div>
      <div className="mt-3 flex gap-2">
        <Button variant="outline" onClick={() => setRows([...rows, { name: "", desc: "", command: "", color: "primary" }])}>添加</Button>
        <Button onClick={async () => {
          if (rows.some((row) => !row.name.trim() || !row.command.trim())) return app.toast("名称和命令都不能空", "bad");
          setRows(await api("/api/settings/presets", { method: "PUT", body: JSON.stringify(rows) }));
          app.toast("预设已保存", "ok");
        }}>保存预设</Button>
      </div>
    </Card>
  );
}

function Maintenance({ initial }: { initial: Settings["maintenance"] }) {
  const app = useApp();
  const [form, setForm] = useState(initial);
  const field = (key: keyof typeof form, label: string) => (
    <label className="text-xs text-muted-foreground">{label}<Input type="number" value={form[key]} onChange={(event) => setForm({ ...form, [key]: Number(event.target.value) })} /></label>
  );
  return (
    <Card>
      <h2 className="font-semibold">保留与告警</h2>
      <div className="mt-3 grid grid-cols-2 gap-3 md:grid-cols-3">
        {field("cmd_retention_days", "命令保留天数")}
        {field("cmd_retention_max", "命令条数上限")}
        {field("event_retention_day", "事件保留天数")}
        {field("disk_alert_pct", "磁盘告警 %")}
        {field("temp_alert_c", "温度告警 °C")}
        {field("mem_alert_pct", "内存告警 %")}
      </div>
      <Button className="mt-3" onClick={async () => { await api("/api/settings", { method: "POST", body: JSON.stringify({ maintenance: form }) }); app.toast("已保存", "ok"); }}>保存</Button>
    </Card>
  );
}

function Password() {
  const app = useApp();
  const [oldPassword, setOld] = useState("");
  const [next, setNext] = useState("");
  return (
    <Card>
      <h2 className="font-semibold">管理员密码</h2>
      <div className="mt-3 grid gap-2 md:grid-cols-2">
        <Input type="password" placeholder="旧密码" value={oldPassword} onChange={(event) => setOld(event.target.value)} />
        <Input type="password" placeholder="新密码" value={next} onChange={(event) => setNext(event.target.value)} />
      </div>
      <Button className="mt-3" onClick={async () => {
        await api("/api/auth/password", { method: "POST", body: JSON.stringify({ old_password: oldPassword, new_password: next }) });
        setOld(""); setNext("");
        app.toast("密码已修改", "ok");
      }}>修改密码</Button>
    </Card>
  );
}
