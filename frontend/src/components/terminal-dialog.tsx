import { useEffect, useRef } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { Dialog } from "@/components/ui";
import { useApp } from "@/state";

export function TerminalDialog({ deviceID, onClose }: { deviceID: number | null; onClose: () => void }) {
  const app = useApp();
  const host = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (deviceID == null || !host.current) return;
    const term = new Terminal({ fontSize: 13, cursorBlink: true, theme: { background: "#111417" } });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host.current);
    fit.fit();
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const base = app.roomID ? `/ws/cluster/rooms/${encodeURIComponent(app.roomID)}/terminal/` : "/ws/terminal/";
    const socket = new WebSocket(proto + "//" + location.host + base + deviceID + `?cols=${term.cols}&rows=${term.rows}`);
    socket.binaryType = "arraybuffer";
    socket.onopen = () => term.write(`\x1b[32m已连接 #${deviceID}\x1b[0m\r\n`);
    socket.onmessage = (event) => term.write(new Uint8Array(event.data));
    socket.onclose = () => term.write("\r\n\x1b[31m连接已断开\x1b[0m\r\n");
    term.onData((data) => { if (socket.readyState === WebSocket.OPEN) socket.send(data); });
    const onResize = () => {
      fit.fit();
      if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
    };
    window.addEventListener("resize", onResize);
    return () => {
      window.removeEventListener("resize", onResize);
      socket.close();
      term.dispose();
    };
  }, [deviceID, app.roomID]);
  return (
    <Dialog open={deviceID != null} onOpenChange={(open) => { if (!open) onClose(); }} title={deviceID != null ? `终端 #${deviceID}` : "终端"} wide>
      <div ref={host} className="h-[460px] overflow-hidden rounded-md bg-[#111417] p-2" />
    </Dialog>
  );
}
