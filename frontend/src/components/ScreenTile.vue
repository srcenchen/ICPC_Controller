<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from "vue";
import { screenFrameURL } from "@/lib/api";
import { fetchFrame, sleep } from "@/lib/framePool";

const props = defineProps<{ id: number; room: string; interval: number; paused?: boolean }>();

const src = ref("");
const error = ref("");
const loading = ref(true);
const el = ref<HTMLElement | null>(null);
let visible = false;
let controller: AbortController | null = null;
let observer: IntersectionObserver | null = null;
let wake: (() => void) | null = null;

function setFrame(url: string) {
  if (src.value) URL.revokeObjectURL(src.value);
  src.value = url;
}

async function loop(signal: AbortSignal) {
  while (!signal.aborted) {
    if (!visible || props.paused || document.visibilityState !== "visible") {
      await new Promise<void>((resolve) => (wake = resolve));
      wake = null;
      continue;
    }
    const started = Date.now();
    try {
      setFrame(await fetchFrame(screenFrameURL(props.room, props.id), signal));
      error.value = "";
    } catch (err) {
      if (signal.aborted) return;
      error.value = err instanceof Error && err.name !== "AbortError" ? err.message : "画面不可用";
    } finally {
      loading.value = false;
    }
    // Back off on errors so dead machines don't hog request slots.
    const wait = error.value ? Math.max(props.interval * 3, 5000) : props.interval;
    await sleep(Math.max(0, wait - (Date.now() - started)));
  }
}

function poke() {
  wake?.();
}

function start() {
  controller?.abort();
  controller = new AbortController();
  loop(controller.signal);
}

onMounted(() => {
  observer = new IntersectionObserver((entries) => {
    visible = entries.some((entry) => entry.isIntersecting);
    poke();
  }, { rootMargin: "200px" });
  if (el.value) observer.observe(el.value);
  document.addEventListener("visibilitychange", poke);
  start();
});
watch(() => [props.id, props.room], start);
watch(() => props.paused, poke);
onBeforeUnmount(() => {
  controller?.abort();
  poke();
  observer?.disconnect();
  document.removeEventListener("visibilitychange", poke);
  if (src.value) URL.revokeObjectURL(src.value);
});
</script>

<template>
  <div ref="el" class="tile-frame">
    <img v-if="src" :src="src" alt="" draggable="false" :class="{ stale: !!error }" />
    <div v-if="!src || error" class="overlay">
      <span v-if="loading" class="muted">加载中…</span>
      <span v-else>{{ error || "无画面" }}</span>
    </div>
  </div>
</template>

<style scoped>
.tile-frame { position: relative; aspect-ratio: 16 / 9; background: #0b0c0e; overflow: hidden; }
img { width: 100%; height: 100%; object-fit: contain; display: block; }
img.stale { opacity: .35; filter: grayscale(1); }
.overlay { position: absolute; inset: 0; display: grid; place-items: center; color: #9aa3ad; font-size: 12px; text-align: center; padding: 8px; }
</style>
