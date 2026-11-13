// Paths that a cloud server forwards to a room relay when a room is selected.
const PROXIED = /^(devices|commands|stats|checkin|network|power|presets|distribution|snapshots)(\/|$)|^settings\/(presets|checkin|screen)(\/|$)/;

let roomID = "";

export function setApiRoom(id: string) {
  roomID = id;
}

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

/** resolve maps "/api/x" to the room proxy when managing a room from the cloud. */
export function apiPath(path: string) {
  const raw = path.startsWith("/api/") ? path.slice(5) : path.replace(/^\//, "");
  const q = raw.indexOf("?");
  const head = q >= 0 ? raw.slice(0, q) : raw;
  const query = q >= 0 ? raw.slice(q) : "";
  if (roomID && PROXIED.test(head)) {
    return `/api/cluster/rooms/${encodeURIComponent(roomID)}/proxy/${head}${query}`;
  }
  return path.startsWith("/") ? path : "/api/" + raw;
}

export async function api<T = unknown>(path: string, init: RequestInit & { json?: unknown } = {}): Promise<T> {
  const { json, ...rest } = init;
  const headers = new Headers(rest.headers);
  if (json !== undefined) {
    rest.body = JSON.stringify(json);
    if (!rest.method) rest.method = "POST";
  }
  if (rest.body && !(rest.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const response = await fetch(apiPath(path), { credentials: "same-origin", ...rest, headers });
  if (response.status === 401 && !location.pathname.startsWith("/login")) {
    location.href = "/login.html";
  }
  const text = await response.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = text;
  }
  if (!response.ok) {
    const message = (data && typeof data === "object" && "error" in data ? String((data as { error: unknown }).error) : typeof data === "string" && data.trim() ? data.trim() : "") || response.statusText || "请求失败";
    throw new ApiError(response.status, message);
  }
  return data as T;
}

export function wsURL(path: string) {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  return proto + "//" + location.host + path;
}

/** Single low-res screen frame (completes quickly, so it never pins a connection). */
export function screenFrameURL(room: string, id: number, hd = false) {
  const query = `?hd=${hd ? 1 : 0}&single=1&_t=${Date.now()}`;
  if (room) return `/api/cluster/rooms/${encodeURIComponent(room)}/proxy/devices/${id}/screen${query}`;
  return `/api/devices/${id}/screen${query}`;
}

/** Continuous MJPEG stream; use for at most one enlarged view at a time. */
export function screenStreamURL(room: string, id: number, hd = true) {
  const query = `?hd=${hd ? 1 : 0}`;
  if (room) return `/api/cluster/rooms/${encodeURIComponent(room)}/screen/${id}${query}`;
  return `/api/devices/${id}/screen${query}`;
}

export function terminalURL(room: string, id: number, cols: number, rows: number) {
  const base = room ? `/ws/cluster/rooms/${encodeURIComponent(room)}/terminal/` : "/ws/terminal/";
  return wsURL(`${base}${id}?cols=${cols}&rows=${rows}`);
}

export function errorText(err: unknown) {
  return err instanceof Error ? err.message : String(err || "操作失败");
}
