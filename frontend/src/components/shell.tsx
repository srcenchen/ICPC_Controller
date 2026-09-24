import { useState } from "react";
import {
  LayoutDashboard, Monitor, TerminalSquare, Globe, FolderDown, Power, ClipboardCheck,
  Presentation, Settings, LogOut, Building2, Sun, Moon, Menu, Radio,
} from "lucide-react";
import { api } from "@/lib/api";
import { PAGE_TITLE, type PageId } from "@/lib/utils";
import { useApp } from "@/state";
import { cn } from "@/lib/utils";

const NAV: { id: PageId; icon: typeof LayoutDashboard; group: string; cloudOnly?: boolean }[] = [
  { id: "rooms", icon: Building2, group: "监控", cloudOnly: true },
  { id: "dashboard", icon: LayoutDashboard, group: "监控" },
  { id: "devices", icon: Monitor, group: "监控" },
  { id: "screen", icon: Radio, group: "监控" },
  { id: "commands", icon: TerminalSquare, group: "运维" },
  { id: "network", icon: Globe, group: "运维" },
  { id: "distribute", icon: FolderDown, group: "运维" },
  { id: "power", icon: Power, group: "运维" },
  { id: "checkin", icon: ClipboardCheck, group: "比赛" },
  { id: "broadcast", icon: Presentation, group: "比赛" },
  { id: "settings", icon: Settings, group: "系统" },
];

export function Shell({ children }: { children: React.ReactNode }) {
  const app = useApp();
  const [open, setOpen] = useState(false);
  const [dark, setDark] = useState(() => document.documentElement.getAttribute("data-theme") === "dark");
  const groups = ["监控", "运维", "比赛", "系统"];
  const modeLabel = app.deployment.mode === "cloud" ? "云端" : app.deployment.mode === "relay" ? "机房中转" : "单机";
  const roomName = app.rooms.find((room) => room.id === app.roomID)?.name;

  function toggleTheme() {
    const next = dark ? "light" : "dark";
    document.documentElement.setAttribute("data-theme", next);
    localStorage.setItem("icpc-theme", next);
    setDark(!dark);
  }

  async function logout() {
    if (!(await app.confirm("确定退出登录吗？"))) return;
    await api("/api/auth/logout", { method: "POST" });
    location.href = "/login.html";
  }

  return (
    <div className="flex h-full min-h-screen bg-background">
      {open && <button className="fixed inset-0 z-30 bg-black/40 md:hidden" aria-label="关闭菜单" onClick={() => setOpen(false)} />}
      <aside className={cn("fixed z-40 flex h-full w-60 shrink-0 flex-col bg-sidebar text-sidebar-foreground transition-transform md:static md:translate-x-0", open ? "translate-x-0" : "-translate-x-full")}>
        <div className="flex items-center gap-2 px-4 py-4">
          <span className="grid h-8 w-8 place-items-center rounded-md bg-primary text-xs font-bold text-primary-foreground">IC</span>
          <div>
            <div className="text-sm font-semibold tracking-tight">ICPC 集控</div>
            <div className="text-[11px] text-sidebar-foreground/60">{modeLabel}</div>
          </div>
        </div>
        <nav className="flex-1 overflow-auto px-2 pb-4">
          {groups.map((group) => (
            <div key={group} className="mb-3">
              <div className="px-2 pb-1 text-[11px] font-medium uppercase tracking-wider text-sidebar-foreground/45">{group}</div>
              {NAV.filter((item) => item.group === group && (!item.cloudOnly || app.cloud)).map((item) => {
                const Icon = item.icon;
                const active = app.page === item.id;
                return (
                  <button
                    key={item.id}
                    className={cn("mb-0.5 flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-sm", active ? "bg-white/10 text-white" : "text-sidebar-foreground/80 hover:bg-white/5")}
                    onClick={() => { app.go(item.id); setOpen(false); }}
                  >
                    <Icon className="h-4 w-4" />
                    {PAGE_TITLE[item.id]}
                  </button>
                );
              })}
            </div>
          ))}
        </nav>
        <button className="m-2 flex items-center gap-2 rounded-md px-2 py-2 text-sm text-sidebar-foreground/80 hover:bg-white/5" onClick={logout}>
          <LogOut className="h-4 w-4" />退出
        </button>
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 items-center gap-3 border-b border-border px-3 md:px-5">
          <button className="rounded-md p-2 hover:bg-muted md:hidden" onClick={() => setOpen(true)} aria-label="打开菜单"><Menu className="h-4 w-4" /></button>
          <div className="min-w-0 flex-1">
            <div className="truncate text-sm font-semibold">{PAGE_TITLE[app.page]}</div>
            <div className="truncate text-xs text-muted-foreground">
              {app.cloudAll ? "全部机房" : app.roomID ? `正在管理 ${roomName || app.roomID}` : app.deployment.room_name || "当前局域网"}
            </div>
          </div>
          {app.cloud && (
            <select className="h-9 max-w-[220px] rounded-md border border-border bg-card px-2 text-sm" aria-label="管理范围" value={app.roomID} onChange={(event) => app.setRoom(event.target.value)}>
              <option value="">全部机房</option>
              {app.rooms.map((room) => <option key={room.id} value={room.id}>{room.name}{room.online ? "" : " · 离线"}</option>)}
            </select>
          )}
          <span className="hidden items-center gap-1.5 text-xs text-muted-foreground sm:flex">
            <span className={cn("h-2 w-2 rounded-full", app.connected ? "bg-ok" : "bg-warning")} />
            {app.connected ? "实时" : "重连中"}
          </span>
          <span className="hidden text-xs text-muted-foreground lg:inline">在线 {app.stats.online} / {app.stats.total}</span>
          <button className="rounded-md p-2 hover:bg-muted" onClick={toggleTheme} aria-label="切换主题">{dark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}</button>
        </header>
        <main className="min-h-0 flex-1 overflow-auto p-3 md:p-5">{children}</main>
      </div>
    </div>
  );
}
