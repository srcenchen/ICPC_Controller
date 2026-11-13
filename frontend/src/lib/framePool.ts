import { sleep } from "./utils";

/**
 * FramePool limits how many screen snapshot requests are in flight at once.
 *
 * Browsers allow only ~6 concurrent HTTP/1.1 connections per host. The old
 * screen wall opened one never-ending MJPEG stream per device, so from the 7th
 * device on every image — and every API call — queued forever. Tiles now
 * fetch single JPEG frames through this FIFO semaphore, so connections are
 * always released and devices are refreshed round-robin.
 */
export class FramePool {
  private active = 0;
  private waiters: (() => void)[] = [];
  constructor(private limit: number) {}

  setLimit(limit: number) {
    this.limit = limit;
    this.drain();
  }

  private drain() {
    while (this.active < this.limit && this.waiters.length) {
      this.active++;
      this.waiters.shift()!();
    }
  }

  async acquire(signal?: AbortSignal) {
    if (this.active < this.limit) {
      this.active++;
      return;
    }
    await new Promise<void>((resolve, reject) => {
      const entry = () => resolve();
      this.waiters.push(entry);
      signal?.addEventListener("abort", () => {
        const index = this.waiters.indexOf(entry);
        if (index >= 0) this.waiters.splice(index, 1);
        reject(new DOMException("aborted", "AbortError"));
      }, { once: true });
    });
  }

  release() {
    this.active = Math.max(0, this.active - 1);
    this.drain();
  }
}

export const framePool = new FramePool(4);

/** fetchFrame downloads one JPEG and returns an object URL (caller revokes). */
export async function fetchFrame(url: string, signal: AbortSignal, timeoutMs = 8000) {
  await framePool.acquire(signal);
  const timeout = new AbortController();
  const timer = setTimeout(() => timeout.abort(), timeoutMs);
  const onAbort = () => timeout.abort();
  signal.addEventListener("abort", onAbort, { once: true });
  try {
    const response = await fetch(url, { signal: timeout.signal, credentials: "same-origin", cache: "no-store" });
    if (!response.ok) throw new Error(response.status === 404 ? "设备不存在" : "画面不可用");
    const blob = await response.blob();
    if (!blob.size) throw new Error("空画面");
    return URL.createObjectURL(blob);
  } finally {
    clearTimeout(timer);
    signal.removeEventListener("abort", onAbort);
    framePool.release();
  }
}

export { sleep };
