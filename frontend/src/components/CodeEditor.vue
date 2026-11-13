<script setup lang="ts">
import { computed, ref } from "vue";
import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";

hljs.registerLanguage("bash", bash);

/** Shell input with syntax highlighting: a transparent textarea over a highlighted <pre>. */
const model = defineModel<string>({ required: true });
const props = withDefaults(defineProps<{ rows?: number; placeholder?: string }>(), { rows: 6, placeholder: "" });
const emit = defineEmits<{ (e: "submit"): void }>();

const pre = ref<HTMLElement | null>(null);
const html = computed(() => hljs.highlight(model.value + "\n", { language: "bash" }).value);

function sync(event: Event) {
  const area = event.target as HTMLTextAreaElement;
  if (pre.value) {
    pre.value.scrollTop = area.scrollTop;
    pre.value.scrollLeft = area.scrollLeft;
  }
}
function keydown(event: KeyboardEvent) {
  const area = event.target as HTMLTextAreaElement;
  if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
    event.preventDefault();
    emit("submit");
  } else if (event.key === "Tab" && !event.shiftKey) {
    event.preventDefault();
    const { selectionStart: a, selectionEnd: b } = area;
    model.value = model.value.slice(0, a) + "  " + model.value.slice(b);
    requestAnimationFrame(() => area.setSelectionRange(a + 2, a + 2));
  }
}
</script>

<template>
  <div class="editor" :style="{ height: props.rows * 21 + 20 + 'px' }">
    <pre ref="pre" aria-hidden="true"><code class="hljs" v-html="html" /></pre>
    <textarea
      v-model="model"
      spellcheck="false"
      autocapitalize="off"
      autocomplete="off"
      :placeholder="placeholder"
      @scroll="sync"
      @keydown="keydown"
    />
  </div>
</template>

<style scoped>
.editor { position: relative; border: 1px solid var(--el-border-color); border-radius: 8px; background: #0f1115; overflow: hidden; transition: border-color .15s; }
.editor:focus-within { border-color: var(--el-color-primary); }
pre, textarea {
  position: absolute; inset: 0; margin: 0; padding: 10px 12px; border: 0;
  font: 13px/21px var(--mono); white-space: pre-wrap; word-break: break-all; overflow: auto; tab-size: 2;
}
pre { pointer-events: none; color: #d6dde6; }
textarea { resize: none; background: transparent; color: transparent; caret-color: #fff; outline: none; }
textarea::placeholder { color: #5c6773; }
textarea::selection { background: rgba(80, 140, 255, .35); }
:deep(.hljs) { background: transparent; padding: 0; }
:deep(.hljs-keyword), :deep(.hljs-built_in) { color: #ff7b72; }
:deep(.hljs-string) { color: #a5d6ff; }
:deep(.hljs-variable), :deep(.hljs-template-variable) { color: #ffa657; }
:deep(.hljs-comment) { color: #8b949e; font-style: italic; }
:deep(.hljs-number), :deep(.hljs-literal) { color: #79c0ff; }
:deep(.hljs-meta) { color: #d2a8ff; }
:deep(.hljs-subst) { color: #d6dde6; }
</style>
