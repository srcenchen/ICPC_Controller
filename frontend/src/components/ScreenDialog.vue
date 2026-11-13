<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { ArrowLeft, ArrowRight, FullScreen, Monitor, Refresh } from "@element-plus/icons-vue";
import { screenStreamURL } from "@/lib/api";
import { store, deviceByID, onlineDevices } from "@/lib/store";
import { parseIP } from "@/lib/utils";

// Explicit blank image assigned before unmounting/switching so Chromium
// immediately closes the underlying multipart/x-mixed-replace MJPEG socket.
const BLANK = "data:image/gif;base64,R0lGODlhAQABAAAAACH5BAEKAAEALAAAAAABAAEAAAICTAEAOw==";
const hd = ref(true);
const nonce = ref(0);
const failed = ref(false);
const box = ref<HTMLElement | null>(null);
const imgRef = ref<HTMLImageElement | null>(null);

function releaseStream() {
  if (imgRef.value) imgRef.value.src = BLANK;
}

const open = computed({
  get: () => store.screenID != null,
  set: (value) => {
    if (!value) {
      releaseStream();
      store.screenID = null;
    }
  },
});

const device = computed(() => deviceByID(store.screenID));
const onlineIDs = computed(() => onlineDevices.value.map((d) => d.assigned_id));
const currentIndex = computed(() => (store.screenID == null ? -1 : onlineIDs.value.indexOf(store.screenID)));

const src = computed(() =>
  store.screenID == null ? BLANK : screenStreamURL(store.roomID, store.screenID, hd.value) + "&_n=" + nonce.value,
);

watch([() => store.screenID, hd], () => {
  releaseStream();
  failed.value = false;
});

function step(delta: number) {
  const list = onlineIDs.value;
  if (list.length < 2) return;
  releaseStream();
  const idx = currentIndex.value;
  const next = idx < 0 ? 0 : (idx + delta + list.length) % list.length;
  store.screenID = list[next];
}

function reconnect() {
  releaseStream();
  failed.value = false;
  nonce.value++;
}

function fullscreen() {
  box.value?.requestFullscreen?.();
}

function openTerminal() {
  const id = store.screenID;
  open.value = false;
  store.terminalID = id;
}

function onKey(e: KeyboardEvent) {
  if (!open.value || store.terminalID != null) return;
  if (e.key === "ArrowLeft") step(-1);
  if (e.key === "ArrowRight") step(1);
}

onMounted(() => window.addEventListener("keydown", onKey));
onBeforeUnmount(() => {
  releaseStream();
  window.removeEventListener("keydown", onKey);
});
</script>

<template>
  <el-dialog
    v-model="open"
    width="min(1200px, 94vw)"
    top="4vh"
    destroy-on-close
    append-to-body
    class="screen-dialog"
    @close="releaseStream"
  >
    <template #header>
      <div class="row">
        <b class="mono">#{{ store.screenID }}</b>
        <template v-if="device">
          <b>{{ device.student_name || device.hostname }}</b>
          <span v-if="device.student_num" class="muted mono small">{{ device.student_num }}</span>
          <span class="muted small">· {{ parseIP(device.local_ip) || device.hostname }}</span>
        </template>
        <el-tag size="small" :type="hd ? 'primary' : 'info'" effect="plain">{{ hd ? "实时高清流" : "实时流畅流" }}</el-tag>
      </div>
    </template>
    <div ref="box" class="stage">
      <img v-if="open" ref="imgRef" :key="src" :src="src" alt="" @error="failed = true" @load="failed = false" />
      <div v-if="failed" class="overlay">
        <div>画面暂不可用（设备离线或未开启屏幕采集）</div>
        <el-button size="small" :icon="Refresh" style="margin-top: 8px" @click="reconnect">重试</el-button>
      </div>
    </div>
    <template #footer>
      <div class="row">
        <el-button-group>
          <el-button :icon="ArrowLeft" :disabled="onlineIDs.length < 2" @click="step(-1)">上一台</el-button>
          <el-button :disabled="onlineIDs.length < 2" @click="step(1)">下一台<el-icon class="el-icon--right"><ArrowRight /></el-icon></el-button>
        </el-button-group>
        <el-switch v-model="hd" active-text="高清" inactive-text="流畅" inline-prompt />
        <el-button :icon="Refresh" @click="reconnect">重连</el-button>
        <el-button :icon="FullScreen" @click="fullscreen">全屏</el-button>
        <span class="spacer" />
        <el-button v-if="store.screenID != null" @click="store.detailID = store.screenID">设备详情</el-button>
        <el-button v-if="store.screenID != null" type="primary" :icon="Monitor" @click="openTerminal">打开终端</el-button>
      </div>
    </template>
  </el-dialog>
</template>

<style scoped>
.stage { position: relative; aspect-ratio: 16 / 9; background: #000; border-radius: 6px; overflow: hidden; }
.stage img { width: 100%; height: 100%; object-fit: contain; display: block; }
.overlay { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; color: #aab; }
</style>
