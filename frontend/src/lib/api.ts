const PROXIED = /^(devices|commands|stats|checkin|network|power|presets|distribution|snapshots)(\/|$)|^settings\/(presets|checkin)(\/|$)/;

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

function resolve(path: string) {
  const raw = path.startsWith("/api/") ? path.slice(5) : path.replace(/^\//, "");
  const q = raw.indexOf("?");
  const head = q >= 0 ? raw.slice(0, q) : raw;
  const query = q >= 0 ? raw.slice(q) : "";
  if (roomID && PROXIED.test(head)) {
    return `/api/cluster/rooms/${encodeURIComponent(roomID)}/proxy/${head}${query}`;
  }
  return path.startsWith("/") ? path : "/api/" + raw;
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body && !(init.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const response = await fetch(resolve(path), { credentials: "same-origin", ...init, headers });
  if (response.status === 401 && !location.pathname.startsWith("/login")) {
    location.href = "/login.html";
  }
  const text = await response.text();
  const data = text ? JSON.parse(text) : null;
  if (!response.ok) throw new ApiError(response.status, data?.error || response.statusText || "请求失败");
  return data as T;
}

export function apiRoomPath(path: string) {
  return resolve(path);
}
