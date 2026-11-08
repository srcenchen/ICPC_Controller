import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { Button, Dialog } from "@/components/ui";
import { useApp } from "@/state";

export function TerminalDialog({ deviceID, onClose }: { deviceID: number | null; onClose: () => void }) {
  const app = useApp();
  const host = useRef<HTMLDivElement>(null);
  const reconnect = useRef<() => void>(() => {});
  const [connection, setConnection] = useState<"connecting" | "connected" | "disconnected">("connecting");
  useEffect(() => {
    if (deviceID == null || !host.current) return;
    const term = new Terminal({ fontSize: 13, cursorBlink: true, theme: { background: "#111417" } });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host.current);
    fit.fit();
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const base = app.roomID ? `/ws/cluster/rooms/${encodeURIComponent(app.roomID)}/terminal/` : "/ws/terminal/";
    let socket: WebSocket | null = null;
    const connect = () => {
      if (socket && socket.readyState !== WebSocket.CLOSED) return;
      setConnection("connecting");
      const next = new WebSocket(proto + "//" + location.host + base + deviceID + `?cols=${term.cols}&rows=${term.rows}`);
      socket = next;
      next.binaryType = "arraybuffer";
      next.onopen = () => {
        setConnection("connected");
        term.write(`\x1b[32m已连接 #${deviceID}\x1b[0m\r\n`);
        term.focus();
      };
      next.onmessage = (event) => term.write(typeof event.data === "string" ? event.data : new Uint8Array(event.data));
      next.onclose = () => {
        setConnection("disconnected");
        term.write("\r\n\x1b[31m连接已断开\x1b[0m\r\n");
      };
    };
    reconnect.current = connect;
    connect();
    term.onData((data) => { if (socket?.readyState === WebSocket.OPEN) socket.send(data); });
    const onResize = () => {
      fit.fit();
      if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
    };
    window.addEventListener("resize", onResize);
    return () => {
      window.removeEventListener("resize", onResize);
      reconnect.current = () => {};
      if (socket) {
        socket.onopen = socket.onmessage = socket.onclose = null;
        socket.close();
      }
      term.dispose();
    };
  }, [deviceID, app.roomID]);
  return (
    <Dialog open={deviceID != null} onOpenChange={(open) => { if (!open) onClose(); }} title={deviceID != null ? `终端 #${deviceID}` : "终端"} wide>
      <div className="mb-2 flex h-8 items-center justify-between">
        <p role="status" className="text-xs text-muted-foreground">{connection === "connected" ? "已连接" : connection === "disconnected" ? "连接已断开" : "正在连接…"}</p>
        {connection === "disconnected" && <Button size="sm" variant="outline" onClick={() => reconnect.current()}>重连</Button>}
      </div>
      <div ref={host} className="h-[460px] overflow-hidden rounded-md bg-[#111417] p-2" />
    </Dialog>
  );
}
