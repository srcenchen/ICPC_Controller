<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { FullScreen, Refresh, VideoCamera, ZoomIn, ZoomOut } from "@element-plus/icons-vue";
import { terminalURL } from "@/lib/api";
import { store, deviceByID } from "@/lib/store";
import { parseIP } from "@/lib/utils";

type State = "connecting" | "connected" | "closed" | "offline";

const host = ref<HTMLElement | null>(null);
const state = ref<State>("connecting");
const full = ref(false);
const fontSize = ref(Number(localStorage.getItem("icpc-term-font")) || 14);

const open = computed({
  get: () => store.terminalID != null,
  set: (value) => { if (!value) store.terminalID = null; },
});
const device = computed(() => deviceByID(store.terminalID));

let term: Terminal | null = null;
let fit: FitAddon | null = null;
let socket: WebSocket | null = null;
let resizeObs: ResizeObserver | null = null;
let currentID: number | null = null;
let mountedID: number | null = null;
let currentRoom = "";

const encoder = new TextEncoder();

function sendResize() {
  if (term && socket?.readyState === WebSocket.OPEN) {
    socket.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
  }
}

function refit() {
  if (!term || !fit || !host.value || host.value.clientWidth === 0) return;
  try {
    fit.fit();
  } catch {
    /* element hidden during transition */
  }
  sendResize();
}

function closeSocket() {
  if (!socket) return;
  socket.onopen = socket.onmessage = socket.onclose = socket.onerror = null;
  socket.close();
  socket = null;
}

function connect() {
  if (!term || currentID == null) return;
  closeSocket();
  state.value = "connecting";
  term.write(`\r\n\x1b[90m正在连接 #${currentID}…\x1b[0m\r\n`);
  const ws = new WebSocket(terminalURL(currentRoom, currentID, term.cols, term.rows));
  ws.binaryType = "arraybuffer";
  socket = ws;
  let gotData = false;
  ws.onopen = () => {
    state.value = "connected";
    term?.focus();
    // The pty may have been opened with a stale size if the dialog was still animating.
    setTimeout(refit, 50);
  };
  ws.onmessage = (event) => {
    gotData = true;
    term?.write(typeof event.data === "string" ? event.data : new Uint8Array(event.data));
  };
  ws.onclose = (event) => {
    if (socket !== ws) return;
    socket = null;
    if (event.code === 4404) {
      state.value = "offline";
      return;
    }
    state.value = "closed";
    term?.write(`\r\n\x1b[33m连接已断开${gotData ? "" : "（未收到任何输出，设备可能未响应）"}。按 Enter 或点击“重新连接”。\x1b[0m\r\n`);
  };
}

function setup() {
  if (!host.value || currentID == null) return;
  term = new Terminal({
    fontSize: fontSize.value,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
    cursorBlink: true,
    scrollback: 5000,
    allowProposedApi: false,
    theme: { background: "#0f1115", foreground: "#d6dde6", cursor: "#9ec1ff", selectionBackground: "#264f78" },
  });
  fit = new FitAddon();
  term.loadAddon(fit);
  term.open(host.value);
  refit();
  term.onData((data) => {
    if (socket?.readyState === WebSocket.OPEN) {
      socket.send(encoder.encode(data));
    } else if (state.value === "closed" && data === "\r") {
      connect();
    }
  });
  resizeObs = new ResizeObserver(() => refit());
  resizeObs.observe(host.value);
  connect();
}

function teardown() {
  closeSocket();
  resizeObs?.disconnect();
  resizeObs = null;
  term?.dispose();
  term = null;
  fit = null;
  mountedID = null;
}

function onDialogOpened() {
  refit();
  term?.focus();
}

watch(
  [() => store.terminalID, host],
  ([id, el]) => {
    if (id == null) {
      teardown();
      currentID = null;
      return;
    }
    if (!el) return;
    if (mountedID === id && currentRoom === store.roomID && term) return;
    teardown();
    currentID = id;
    mountedID = id;
    currentRoom = store.roomID;
    setup();
  },
  { flush: "post" },
);

watch(fontSize, (size) => {
  localStorage.setItem("icpc-term-font", String(size));
  if (term) {
    term.options.fontSize = size;
    refit();
  }
});
watch(full, () => nextTick(() => setTimeout(refit, 60)));

onBeforeUnmount(teardown);

const stateText = computed(() => ({ connecting: "正在连接", connected: "已连接", closed: "已断开", offline: "设备不在线" })[state.value]);
const stateType = computed(() => ({ connecting: "warning", connected: "success", closed: "info", offline: "danger" })[state.value] as "warning" | "success" | "info" | "danger");
</script>

<template>
  <el-dialog
    v-model="open"
    :fullscreen="full"
    width="min(1100px, 96vw)"
    top="5vh"
    append-to-body
    destroy-on-close
    :close-on-click-modal="false"
    class="terminal-dialog"
    @opened="onDialogOpened"
    @closed="teardown"
  >
    <template #header>
      <div class="row">
        <b class="mono">终端 #{{ store.terminalID }}</b>
        <span v-if="device" class="muted small">
          {{ device.student_name || "未签到" }}<template v-if="device.student_num"> ({{ device.student_num }})</template>
          · {{ parseIP(device.local_ip) || device.hostname }}
        </span>
        <el-tag size="small" :type="stateType" effect="light">{{ stateText }}</el-tag>
      </div>
    </template>
    <div class="term-wrap" :class="{ full }">
      <div ref="host" class="term-host" />
      <div v-if="state === 'offline'" class="term-overlay">
        <p>设备 #{{ store.terminalID }} 当前不在线，无法打开终端。</p>
        <el-button :icon="Refresh" @click="connect">重试</el-button>
      </div>
    </div>
    <template #footer>
      <div class="row">
        <span class="muted small">以 root 身份运行 · 关闭窗口即结束会话</span>
        <span class="spacer" />
        <el-button-group>
          <el-button :icon="ZoomOut" :disabled="fontSize <= 10" @click="fontSize--" />
          <el-button :icon="ZoomIn" :disabled="fontSize >= 24" @click="fontSize++" />
        </el-button-group>
        <el-button v-if="store.terminalID != null" :icon="VideoCamera" @click="store.screenID = store.terminalID">看屏幕</el-button>
        <el-button :icon="FullScreen" @click="full = !full">{{ full ? "退出全屏" : "全屏" }}</el-button>
        <el-button type="primary" :icon="Refresh" :disabled="state === 'connecting'" @click="connect">重新连接</el-button>
      </div>
    </template>
  </el-dialog>
</template>

<style scoped>
.term-wrap { position: relative; height: 62vh; min-height: 320px; background: #0f1115; border-radius: 8px; padding: 8px; }
.term-wrap.full { height: calc(100vh - 150px); }
.term-host { width: 100%; height: 100%; }
.term-overlay { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 8px; color: #c9d1d9; background: rgba(15, 17, 21, .85); border-radius: 8px; }
</style>
