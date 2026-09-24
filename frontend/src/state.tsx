import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { api, setApiRoom } from "@/lib/api";
import { PAGE_TITLE, parsePage, roomFromPath, type PageId } from "@/lib/utils";
import type { AdminEvent, Deployment, Device, Room } from "@/lib/types";
import { Button, Dialog } from "@/components/ui";

type Toast = { id: number; text: string; tone: "info" | "ok" | "bad" };
type ConfirmState = { text: string; resolve: (ok: boolean) => void };

type AppState = {
  page: PageId;
  go: (page: PageId) => void;
  deployment: Deployment;
  connection: string;
  roomID: string;
  setRoom: (id: string) => void;
  rooms: Room[];
  refreshRooms: () => Promise<void>;
  cloud: boolean;
  cloudAll: boolean;
  fleet: string[];
  toggleFleet: (key: string, on?: boolean) => void;
  clearFleet: () => void;
  selectFleet: (keys: string[]) => void;
  stats: { online: number; total: number };
  toast: (text: string, tone?: Toast["tone"]) => void;
  confirm: (text: string) => Promise<boolean>;
  onAdmin: (fn: (event: AdminEvent) => void) => () => void;
  connected: boolean;
};

const Ctx = createContext<AppState | null>(null);

const emptyDeploy: Deployment = {
  mode: "standalone",
  node_id: "",
  room_name: "",
  cloud_url: "",
  token_set: false,
  allow_insecure: false,
  device_id_start: 1,
  advertise_ip: "",
};

export function AppProvider({ children }: { children: ReactNode }) {
  const [page, setPage] = useState<PageId>(() => parsePage(location.hash));
  const [deployment, setDeployment] = useState<Deployment>(emptyDeploy);
  const [connection, setConnection] = useState("");
  const [roomID, setRoomID] = useState(() => roomFromPath());
  const [rooms, setRooms] = useState<Room[]>([]);
  const [fleet, setFleet] = useState<string[]>([]);
  const [stats, setStats] = useState({ online: 0, total: 0 });
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [ask, setAsk] = useState<ConfirmState | null>(null);
  const [connected, setConnected] = useState(false);
  const listeners = useRef(new Set<(event: AdminEvent) => void>());

  const toast = useCallback((text: string, tone: Toast["tone"] = "info") => {
    const id = Date.now() + Math.random();
    setToasts((list) => [...list, { id, text, tone }]);
    setTimeout(() => setToasts((list) => list.filter((item) => item.id !== id)), 3200);
  }, []);

  const confirm = useCallback((text: string) => new Promise<boolean>((resolve) => setAsk({ text, resolve })), []);

  const refreshRooms = useCallback(async () => {
    if (deployment.mode !== "cloud") return;
    const list = await api<Room[]>("/api/cluster/rooms");
    setRooms(list || []);
  }, [deployment.mode]);

  const go = useCallback((next: PageId) => {
    const url = (roomID ? "/room/" + encodeURIComponent(roomID) : "/") + "#" + next;
    if (location.pathname + location.hash !== url) history.pushState(null, "", url);
    setPage(next);
  }, [roomID]);

  const setRoom = useCallback((id: string) => {
    setRoomID(id);
    const url = (id ? "/room/" + encodeURIComponent(id) : "/") + "#" + (location.hash.replace("#", "") || page);
    history.pushState(null, "", url);
  }, [page]);

  useEffect(() => { setApiRoom(roomID); }, [roomID]);

  useEffect(() => {
    const sync = () => {
      setPage(parsePage(location.hash));
      setRoomID(roomFromPath());
    };
    window.addEventListener("hashchange", sync);
    window.addEventListener("popstate", sync);
    return () => {
      window.removeEventListener("hashchange", sync);
      window.removeEventListener("popstate", sync);
    };
  }, []);

  useEffect(() => {
    api<{ deployment: Deployment; connection: string }>("/api/cluster/status")
      .then((result) => {
        setDeployment(result.deployment || emptyDeploy);
        setConnection(result.connection || "");
      })
      .catch(() => {});
  }, [roomID]);

  useEffect(() => {
    if (deployment.mode === "cloud") refreshRooms().catch(() => {});
  }, [deployment.mode, refreshRooms]);

  useEffect(() => {
    if (deployment.mode === "cloud" && !roomID) {
      const online = rooms.reduce((sum, room) => sum + room.devices.filter((device) => device.connected).length, 0);
      const total = rooms.reduce((sum, room) => sum + room.devices.length, 0);
      setStats({ online, total });
      return;
    }
    api<{ online_devices: number; total_devices: number }>("/api/stats")
      .then((result) => setStats({ online: result.online_devices, total: result.total_devices }))
      .catch(() => {});
  }, [deployment.mode, roomID, rooms, page]);

  useEffect(() => {
    let socket: WebSocket | null = null;
    let timer = 0;
    let stopped = false;
    const open = () => {
      if (stopped) return;
      const proto = location.protocol === "https:" ? "wss:" : "ws:";
      socket = new WebSocket(proto + "//" + location.host + "/ws/admin");
      socket.onopen = () => setConnected(true);
      socket.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data) as AdminEvent;
          listeners.current.forEach((fn) => fn(msg));
        } catch { /* ignore malformed frames */ }
      };
      socket.onclose = () => {
        setConnected(false);
        if (!stopped) timer = window.setTimeout(open, 3000);
      };
    };
    open();
    return () => {
      stopped = true;
      clearTimeout(timer);
      socket?.close();
    };
  }, []);

  const onAdmin = useCallback((fn: (event: AdminEvent) => void) => {
    listeners.current.add(fn);
    return () => listeners.current.delete(fn);
  }, []);

  const value = useMemo<AppState>(() => ({
    page,
    go,
    deployment,
    connection,
    roomID,
    setRoom,
    rooms,
    refreshRooms,
    cloud: deployment.mode === "cloud",
    cloudAll: deployment.mode === "cloud" && !roomID,
    fleet,
    toggleFleet: (key, on) => setFleet((list) => {
      const has = list.includes(key);
      const next = on ?? !has;
      if (next && !has) return [...list, key];
      if (!next && has) return list.filter((item) => item !== key);
      return list;
    }),
    clearFleet: () => setFleet([]),
    selectFleet: (keys) => setFleet(Array.from(new Set(keys))),
    stats,
    toast,
    confirm,
    onAdmin,
    connected,
  }), [page, go, deployment, connection, roomID, setRoom, rooms, refreshRooms, fleet, stats, toast, confirm, onAdmin, connected]);

  return (
    <Ctx.Provider value={value}>
      {children}
      <div className="pointer-events-none fixed bottom-4 right-4 z-[60] flex w-[min(360px,calc(100%-2rem))] flex-col gap-2">
        {toasts.map((item) => (
          <div key={item.id} className={"pointer-events-auto rounded-lg border px-3 py-2 text-sm shadow-lg " + (item.tone === "bad" ? "border-destructive/40 bg-card text-destructive" : item.tone === "ok" ? "border-ok/40 bg-card" : "border-border bg-card")}>
            {item.text}
          </div>
        ))}
      </div>
      <Dialog open={!!ask} onOpenChange={(open) => { if (!open && ask) { ask.resolve(false); setAsk(null); } }} title="请确认">
        <p className="text-sm text-muted-foreground">{ask?.text}</p>
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="outline" onClick={() => { ask?.resolve(false); setAsk(null); }}>取消</Button>
          <Button onClick={() => { ask?.resolve(true); setAsk(null); }}>继续</Button>
        </div>
      </Dialog>
    </Ctx.Provider>
  );
}

export function useApp() {
  const value = useContext(Ctx);
  if (!value) throw new Error("AppProvider missing");
  return value;
}

export function pageTitle(page: PageId) {
  return PAGE_TITLE[page];
}

export function useDevices(reloadKey = 0) {
  const { cloudAll, onAdmin } = useApp();
  const [devices, setDevices] = useState<Device[]>([]);
  const [error, setError] = useState("");
  const load = useCallback(() => {
    if (cloudAll) return;
    api<Device[]>("/api/devices").then(setDevices).catch((err) => setError(err.message));
  }, [cloudAll]);
  useEffect(() => { load(); }, [load, reloadKey]);
  useEffect(() => onAdmin((event) => {
    if (["device_connected", "device_disconnected", "device_updated", "checkin_updated", "device_health"].includes(event.event)) load();
  }), [onAdmin, load]);
  return { devices, reload: load, error };
}
