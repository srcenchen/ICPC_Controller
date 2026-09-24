import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { parseIP } from "@/lib/utils";
import type { Settings } from "@/lib/types";
import { Button, Dialog, Empty, PageHeader } from "@/components/ui";
import { useApp, useDevices } from "@/state";

function streamURL(roomID: string, id: number, single: boolean) {
  const query = "?hd=0" + (single ? "&single=1&_t=" + Date.now() : "");
  if (roomID) return `/api/cluster/rooms/${encodeURIComponent(roomID)}/${single ? "proxy/devices/" + id + "/screen" : "screen/" + id}${query}`;
  return `/api/devices/${id}/screen${query}`;
}

export function Screen() {
  const app = useApp();
  const { devices } = useDevices();
  const [enabled, setEnabled] = useState(false);
  const [large, setLarge] = useState<number | null>(null);
  const ios = /iPad|iPhone|iPod/.test(navigator.userAgent);
  useEffect(() => { api<Settings>("/api/settings").then((settings) => setEnabled(!!settings.screen_monitor_enabled)).catch(() => {}); }, [app.roomID]);

  if (app.cloud && !app.roomID) {
    return <Empty>屏幕流留在机房里，不会汇到云端。请先在顶栏选择一个机房。</Empty>;
  }

  async function toggle(next: boolean) {
    await api("/api/settings", { method: "POST", body: JSON.stringify({ screen_monitor_enabled: next }) });
    setEnabled(next);
  }

  const online = devices.filter((device) => device.connected);
  return (
    <div>
      <PageHeader title="屏幕" description="打开后，在线选手机才会推送画面。云端只在进入某个机房时查看。">
        <Button variant={enabled ? "destructive" : "default"} onClick={() => toggle(!enabled).catch((err) => app.toast(err.message, "bad"))}>{enabled ? "关闭采集" : "开启采集"}</Button>
      </PageHeader>
      {!enabled && <p className="mb-3 text-sm text-muted-foreground">采集关闭时下面不会出画面。</p>}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-4">
        {online.map((device) => (
          <button key={device.assigned_id} className="overflow-hidden rounded-xl border border-border bg-card text-left" onClick={() => setLarge(device.assigned_id)}>
            <div className="aspect-video bg-black">
              {enabled && <Frame roomID={app.roomID} id={device.assigned_id} ios={ios} />}
            </div>
            <div className="px-2 py-1.5 text-xs">#{device.assigned_id} {device.hostname}<div className="text-muted-foreground">{parseIP(device.local_ip)}</div></div>
          </button>
        ))}
      </div>
      {!online.length && <Empty>没有在线设备</Empty>}
      <Dialog open={large != null} onOpenChange={(open) => { if (!open) setLarge(null); }} title={large != null ? `屏幕 #${large}` : "屏幕"} wide>
        {large != null && enabled && <div className="aspect-video bg-black"><Frame roomID={app.roomID} id={large} ios={ios} /></div>}
      </Dialog>
    </div>
  );
}

function Frame({ roomID, id, ios }: { roomID: string; id: number; ios: boolean }) {
  const [src, setSrc] = useState(() => streamURL(roomID, id, ios));
  useEffect(() => {
    if (!ios) { setSrc(streamURL(roomID, id, false)); return; }
    const timer = window.setInterval(() => setSrc(streamURL(roomID, id, true)), 400);
    return () => clearInterval(timer);
  }, [roomID, id, ios]);
  return <img src={src} alt="" className="h-full w-full object-contain" />;
}
