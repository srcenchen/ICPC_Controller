import { useEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type RefObject } from "react";
import { api } from "@/lib/api";
import type { BroadcastConfig, BroadcastFont, BroadcastItem, BroadcastPage, Room } from "@/lib/types";
import { Button, Input, Select } from "@/components/ui";
import { useApp } from "@/state";

const MODES = [
  { id: "before", label: "赛前" },
  { id: "contesting", label: "赛中" },
  { id: "after", label: "赛后" },
] as const;

export function Broadcast() {
  const app = useApp();
  const [mode, setMode] = useState<(typeof MODES)[number]["id"]>("before");
  const [pages, setPages] = useState<BroadcastPage[]>([]);
  const [pageID, setPageID] = useState<number | null>(null);
  const [itemID, setItemID] = useState<number | null>(null);
  const [fonts, setFonts] = useState<BroadcastFont[]>([]);
  const [config, setConfig] = useState<BroadcastConfig | null>(null);
  const [targets, setTargets] = useState<string[]>(app.roomID ? [app.roomID] : []);
  const [dirty, setDirty] = useState(false);

  async function load(keep = pageID) {
    const data = await api<{ pages: BroadcastPage[] }>("/api/broadcast/pages?mode=" + mode);
    const list = data.pages || [];
    setPages(list);
    setPageID(list.some((page) => page.id === keep) ? keep : list[0]?.id ?? null);
    setFonts(await api<BroadcastFont[]>("/api/broadcast/fonts"));
    setConfig(await api<BroadcastConfig>("/api/broadcast/config"));
    setDirty(false);
  }
  useEffect(() => { load(null).catch((err) => app.toast(err.message, "bad")); }, [mode]);

  const page = pages.find((item) => item.id === pageID) || null;
  const item = page?.items?.find((entry) => entry.id === itemID) || null;

  async function publish(action: "sync" | "start" | "stop" | "reset") {
    if (dirty) return app.toast("先保存当前修改", "bad");
    if (!targets.length) return app.toast("请选择要发布到的机房", "bad");
    const labels = { sync: "同步内容和素材", start: "同步并推送", stop: "关闭广播", reset: "复位轮播" };
    if (!(await app.confirm(`对 ${targets.length} 个机房${labels[action]}？素材若未变化，机房不会重新下载。`))) return;
    const result = await api<{ revision: number }>("/api/cluster/broadcast", { method: "POST", body: JSON.stringify({ room_ids: targets, action, mode }) });
    app.toast(`版本 ${result.revision} 已入队`, "ok");
  }

  async function localPush(start: boolean) {
    if (start) {
      await api("/api/broadcast/config", { method: "PUT", body: JSON.stringify({ sync_reset: mode, pushed_state: mode }) });
      const base = (config?.base_url || "http://icpc-server.local:8082").replace(/\/$/, "");
      await api("/api/commands", { method: "POST", body: JSON.stringify({ target_type: "broadcast", command: `full-firefox kill; full-firefox '${base}/broadcast/${mode}'` }) });
      app.toast("已让在线机器打开广播", "ok");
    } else {
      await api("/api/broadcast/config", { method: "PUT", body: JSON.stringify({ pushed_state: "" }) });
      await api("/api/commands", { method: "POST", body: JSON.stringify({ target_type: "broadcast", command: "full-firefox kill" }) });
      app.toast("已关闭", "ok");
    }
    load();
  }

  return (
    <div className="flex h-[calc(100vh-6.5rem)] min-h-[640px] flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex rounded-md bg-muted p-1">
          {MODES.map((entry) => (
            <button key={entry.id} className={"rounded px-3 py-1 text-sm " + (mode === entry.id ? "bg-card shadow-sm" : "")} onClick={() => { if (!dirty || confirm("放弃未保存的修改？")) { setDirty(false); setItemID(null); setMode(entry.id); } }}>{entry.label}</button>
          ))}
        </div>
        <Button size="sm" variant="outline" onClick={() => window.open("/broadcast/" + mode, "_blank")}>预览</Button>
        {app.cloud ? (
          <>
            <Button size="sm" onClick={() => publish("sync")}>同步素材</Button>
            <Button size="sm" onClick={() => publish("start")}>推送</Button>
            <Button size="sm" variant="outline" onClick={() => publish("reset")}>复位</Button>
            <Button size="sm" variant="destructive" onClick={() => publish("stop")}>关闭</Button>
          </>
        ) : (
          <>
            <Button size="sm" onClick={() => localPush(true)}>推送到本机房</Button>
            <Button size="sm" variant="outline" onClick={async () => { await api("/api/broadcast/config", { method: "PUT", body: JSON.stringify({ sync_reset: mode }) }); app.toast("轮播已复位", "ok"); }}>复位</Button>
            <Button size="sm" variant="destructive" onClick={() => localPush(false)}>关闭</Button>
          </>
        )}
        <span className="text-xs text-muted-foreground">{dirty ? "有未保存的修改" : "已保存"}{config?.pushed_state ? ` · 当前推送 ${config.pushed_state}` : ""}</span>
      </div>
      {app.cloud && (
        <div className="rounded-lg border border-border bg-card px-3 py-2 text-sm">
          <p className="mb-1 text-xs text-muted-foreground">这里编辑的是云端统一内容。同步只替换机房里的页面和素材，不改机房自己的推送地址。内容没变时，机房会跳过已经校验过的文件。</p>
          <div className="flex flex-wrap gap-3">
            {app.rooms.map((room) => (
              <label key={room.id} className="flex items-center gap-1"><input type="checkbox" checked={targets.includes(room.id)} onChange={() => setTargets((list) => list.includes(room.id) ? list.filter((id) => id !== room.id) : [...list, room.id])} />{room.name}</label>
            ))}
            <button className="text-primary" onClick={() => setTargets(app.rooms.map((room: Room) => room.id))}>全选</button>
          </div>
        </div>
      )}
      <div className="grid min-h-0 flex-1 grid-cols-1 gap-3 xl:grid-cols-[240px_minmax(0,1fr)_280px]">
        <aside className="space-y-3 overflow-auto">
          <section className="rounded-xl border border-border bg-card p-3">
            <div className="mb-2 flex items-center justify-between"><h2 className="text-sm font-semibold">页面</h2><Button size="sm" variant="outline" onClick={async () => { const created = await api<BroadcastPage>("/api/broadcast/pages", { method: "POST", body: JSON.stringify({ mode, title: "新页面", sort_order: pages.length, duration_ms: 10000, bg_color: "#000000", transition: "fade" }) }); await load(created.id); }}>新建</Button></div>
            {pages.map((entry, index) => (
              <div key={entry.id} className={"mb-1 flex items-center gap-1 rounded-md px-2 py-1 text-sm " + (entry.id === pageID ? "bg-primary/10" : "hover:bg-muted")}>
                <button className="min-w-0 flex-1 truncate text-left" onClick={() => { setPageID(entry.id); setItemID(null); }}>{entry.title || "未命名"}</button>
                <button className="text-xs text-muted-foreground" onClick={() => move(index, -1)}>↑</button>
                <button className="text-xs text-muted-foreground" onClick={() => move(index, 1)}>↓</button>
              </div>
            ))}
          </section>
          <section className="rounded-xl border border-border bg-card p-3 text-sm">
            <h2 className="mb-2 font-semibold">字体</h2>
            <select className="mb-2 h-8 w-full rounded border border-border bg-background px-2" value={config?.active_font || ""} onChange={async (event) => { await api("/api/broadcast/config", { method: "PUT", body: JSON.stringify({ active_font: event.target.value }) }); load(); }}>
              <option value="">默认无衬线</option>
              {fonts.map((font) => <option key={font.id} value={font.filename}>{font.name}</option>)}
            </select>
            <label className="text-xs text-primary">上传字体<input type="file" accept=".ttf,.woff,.woff2" className="hidden" onChange={async (event) => { const file = event.target.files?.[0]; if (!file) return; const body = new FormData(); body.append("file", file); await api("/api/broadcast/fonts", { method: "POST", body }); load(); }} /></label>
          </section>
          {config && (
            <section className="rounded-xl border border-border bg-card p-3 text-sm">
              <h2 className="mb-2 font-semibold">倒计时与地址</h2>
              <Input className="mb-2" type="datetime-local" defaultValue={toLocal(config.countdown_target)} onBlur={async (event) => { const value = event.target.value ? new Date(event.target.value).toISOString() : ""; await api("/api/broadcast/config", { method: "PUT", body: JSON.stringify({ countdown_target: value }) }); }} />
              {!app.cloud && <Input defaultValue={config.base_url} placeholder="本机房推送地址" onBlur={async (event) => { await api("/api/broadcast/config", { method: "PUT", body: JSON.stringify({ base_url: event.target.value.trim() }) }); }} />}
            </section>
          )}
        </aside>
        <Stage page={page} selected={itemID} onSelect={setItemID} onCommit={async (id, box) => { await api(`/api/broadcast/items/${id}/position`, { method: "PATCH", body: JSON.stringify(box) }); setPages((list) => list.map((entry) => entry.id !== page?.id ? entry : { ...entry, items: entry.items?.map((it) => it.id === id ? { ...it, ...box } : it) })); }} />
        <Inspector page={page} item={item} dirty={dirty} setDirty={setDirty} onReload={() => load(pageID)} />
      </div>
    </div>
  );

  async function move(index: number, delta: number) {
    const next = index + delta;
    if (next < 0 || next >= pages.length) return;
    const copy = pages.slice();
    const [row] = copy.splice(index, 1);
    copy.splice(next, 0, row);
    await api("/api/broadcast/pages/reorder", { method: "PUT", body: JSON.stringify(copy.map((entry, order) => ({ id: entry.id, sort_order: order }))) });
    load(pageID);
  }
}

function Stage({ page, selected, onSelect, onCommit }: { page: BroadcastPage | null; selected: number | null; onSelect: (id: number | null) => void; onCommit: (id: number, box: { pos_x: number; pos_y: number; width: number; height: number }) => void }) {
  const ref = useRef<HTMLDivElement>(null);
  if (!page) return <div className="grid place-items-center rounded-xl border border-dashed border-border text-sm text-muted-foreground">先新建一个页面</div>;
  return (
    <div className="grid place-items-center overflow-hidden rounded-xl border border-border bg-muted/40">
      <div ref={ref} className="relative aspect-video w-full max-w-5xl overflow-hidden shadow-lg" style={{ background: page.bg_color || "#000", containerType: "size" }} onMouseDown={() => onSelect(null)}>
        {(page.items || []).map((item) => (
          <CanvasItem key={item.id} item={item} active={item.id === selected} stage={ref} onSelect={() => onSelect(item.id)} onCommit={(box) => onCommit(item.id, box)} />
        ))}
      </div>
    </div>
  );
}

function CanvasItem({ item, active, stage, onSelect, onCommit }: { item: BroadcastItem; active: boolean; stage: RefObject<HTMLDivElement | null>; onSelect: () => void; onCommit: (box: { pos_x: number; pos_y: number; width: number; height: number }) => void }) {
  const [box, setBox] = useState({ pos_x: item.pos_x, pos_y: item.pos_y, width: item.width, height: item.height });
  useEffect(() => setBox({ pos_x: item.pos_x, pos_y: item.pos_y, width: item.width, height: item.height }), [item.pos_x, item.pos_y, item.width, item.height]);
  function drag(event: ReactPointerEvent, resize: boolean) {
    event.stopPropagation();
    event.preventDefault();
    onSelect();
    const bounds = stage.current?.getBoundingClientRect();
    if (!bounds) return;
    const start = { x: event.clientX, y: event.clientY, ...box };
    let latest = { pos_x: box.pos_x, pos_y: box.pos_y, width: box.width, height: box.height };
    const move = (ev: PointerEvent) => {
      const dx = (ev.clientX - start.x) / bounds.width * 100;
      const dy = (ev.clientY - start.y) / bounds.height * 100;
      latest = resize
        ? { pos_x: start.pos_x, pos_y: start.pos_y, width: Math.max(4, start.width + dx), height: Math.max(4, start.height + dy) }
        : { pos_x: clamp(start.pos_x + dx), pos_y: clamp(start.pos_y + dy), width: start.width, height: start.height };
      setBox(latest);
    };
    const up = (ev: PointerEvent) => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      move(ev);
      onCommit(latest);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  }
  const font = Number(item.font_size) || 4;
  return (
    <div className={"absolute overflow-hidden " + (active ? "outline outline-2 outline-primary" : "")} style={{ left: box.pos_x + "%", top: box.pos_y + "%", width: box.width + "%", height: box.height + "%", color: item.font_color, background: item.bg_color === "transparent" ? undefined : item.bg_color, fontSize: font + "cqh", fontWeight: item.font_weight, borderRadius: item.border_radius, zIndex: item.z_index }} onPointerDown={(event) => drag(event, false)}>
      {item.item_type === "image" && item.content ? <img src={item.content} alt="" className="h-full w-full object-contain" draggable={false} /> : <div className="flex h-full items-center justify-center whitespace-pre-wrap px-1 text-center">{label(item)}</div>}
      {active && <button className="absolute bottom-0 right-0 h-3 w-3 cursor-se-resize bg-primary" onPointerDown={(event) => drag(event, true)} />}
    </div>
  );
}

function Inspector({ page, item, dirty, setDirty, onReload }: { page: BroadcastPage | null; item: BroadcastItem | null; dirty: boolean; setDirty: (v: boolean) => void; onReload: () => void }) {
  const app = useApp();
  const [draft, setDraft] = useState<BroadcastItem | null>(item);
  useEffect(() => setDraft(item), [item?.id]);
  if (!page) return <aside />;
  return (
    <aside className="space-y-3 overflow-auto rounded-xl border border-border bg-card p-3 text-sm">
      <h2 className="font-semibold">页面</h2>
      <Input defaultValue={page.title} key={page.id + "title"} onBlur={(event) => api("/api/broadcast/pages/" + page.id, { method: "PUT", body: JSON.stringify({ ...page, title: event.target.value, duration_ms: page.duration_ms, bg_color: page.bg_color, transition: "fade" }) }).then(onReload)} />
      <div className="grid grid-cols-2 gap-2">
        <label className="text-xs text-muted-foreground">停留秒<Input type="number" defaultValue={Math.round((page.duration_ms || 10000) / 1000)} key={page.id + "dur"} onBlur={(event) => api("/api/broadcast/pages/" + page.id, { method: "PUT", body: JSON.stringify({ ...page, duration_ms: (Number(event.target.value) || 10) * 1000, transition: "fade" }) }).then(onReload)} /></label>
        <label className="text-xs text-muted-foreground">背景<Input type="color" defaultValue={page.bg_color || "#000000"} key={page.id + "bg"} onChange={(event) => api("/api/broadcast/pages/" + page.id, { method: "PUT", body: JSON.stringify({ ...page, bg_color: event.target.value, transition: "fade" }) }).then(onReload)} /></label>
      </div>
      <div className="flex flex-wrap gap-1">
        {["text", "image", "clock", "countdown", "scrolling_notice"].map((type) => (
          <Button key={type} size="sm" variant="outline" onClick={async () => {
            let content = type === "text" ? "文本" : type === "clock" ? "" : type === "countdown" ? "" : type === "scrolling_notice" ? "通知" : "";
            if (type === "image") {
              content = await pickImage();
              if (!content) return;
            }
            const created = await api<BroadcastItem>("/api/broadcast/items", { method: "POST", body: JSON.stringify({ page_id: page.id, item_type: type, content, pos_x: 10, pos_y: 10, width: 30, height: 16, font_size: "4", font_color: "#ffffff", font_weight: "500", text_align: "center", bg_color: "transparent", border_radius: "0", animation: "", z_index: 10, extra_json: "{}" }) });
            onReload();
            return created;
          }}>{({ text: "文字", image: "图片", clock: "时钟", countdown: "倒计时", scrolling_notice: "滚动" } as Record<string, string>)[type]}</Button>
        ))}
      </div>
      <div className="flex gap-2">
        <Button size="sm" variant="outline" onClick={() => api("/api/broadcast/pages/" + page.id + "/duplicate", { method: "POST" }).then(onReload)}>复制页面</Button>
        <Button size="sm" variant="ghost" onClick={async () => { if (await app.confirm("删除这个页面？")) { await api("/api/broadcast/pages/" + page.id, { method: "DELETE" }); onReload(); } }}>删除页面</Button>
      </div>
      {draft && (
        <div className="space-y-2 border-t border-border pt-3">
          <h2 className="font-semibold">元素</h2>
          {draft.item_type !== "image" && draft.item_type !== "clock" && <textarea className="h-20 w-full rounded-md border border-border bg-background p-2" value={draft.content} onChange={(event) => { setDraft({ ...draft, content: event.target.value }); setDirty(true); }} />}
          <div className="grid grid-cols-2 gap-2">
            <Input value={draft.font_size} onChange={(event) => { setDraft({ ...draft, font_size: event.target.value }); setDirty(true); }} />
            <Input type="color" value={toColor(draft.font_color)} onChange={(event) => { setDraft({ ...draft, font_color: event.target.value }); setDirty(true); }} />
            <Select value={draft.text_align} onChange={(event) => { setDraft({ ...draft, text_align: event.target.value }); setDirty(true); }}><option value="left">左</option><option value="center">中</option><option value="right">右</option></Select>
            <Select value={draft.font_weight} onChange={(event) => { setDraft({ ...draft, font_weight: event.target.value }); setDirty(true); }}><option value="400">常规</option><option value="600">半粗</option><option value="700">粗</option></Select>
          </div>
          <div className="flex gap-2">
            <Button size="sm" disabled={!dirty} onClick={async () => { await api("/api/broadcast/items/" + draft.id, { method: "PUT", body: JSON.stringify(draft) }); setDirty(false); onReload(); }}>保存元素</Button>
            <Button size="sm" variant="ghost" onClick={async () => { if (await app.confirm("删除元素？")) { await api("/api/broadcast/items/" + draft.id, { method: "DELETE" }); setDirty(false); onReload(); } }}>删除</Button>
          </div>
        </div>
      )}
    </aside>
  );
}

async function pickImage() {
  const file = await new Promise<File | null>((resolve) => {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = "image/*";
    input.onchange = () => resolve(input.files?.[0] || null);
    input.click();
  });
  if (!file) return "";
  const body = new FormData();
  body.append("file", file);
  const uploaded = await api<{ url: string }>("/api/broadcast/images/upload", { method: "POST", body });
  return uploaded.url;
}

function label(item: BroadcastItem) {
  if (item.item_type === "clock") return "00:00:00";
  if (item.item_type === "countdown") return "倒计时";
  return item.content || item.item_type;
}
function clamp(value: number) { return Math.max(0, Math.min(96, Math.round(value * 100) / 100)); }
function toColor(value: string) { return /^#[0-9a-fA-F]{6}$/.test(value) ? value : "#ffffff"; }
function toLocal(iso: string) {
  if (!iso) return "";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}
