<script setup lang="ts">
import { reactive, ref, watch } from "vue";
import type { FormInstance } from "element-plus";
import { api } from "@/lib/api";
import { deviceByID, loadDevices, run } from "@/lib/store";

const props = defineProps<{ id: number | null }>();
const emit = defineEmits<{ (e: "close"): void }>();

const form = reactive({ name: "", num: "" });
const formRef = ref<FormInstance>();
const busy = ref(false);

watch(() => props.id, (id) => {
  const device = deviceByID(id);
  form.name = device?.student_name || "";
  form.num = device?.student_num || "";
});

async function submit() {
  if (!(await formRef.value?.validate().catch(() => false))) return;
  busy.value = true;
  const ok = await run(() => api(`/api/checkin/${props.id}/checkin`, { json: { student_name: form.name.trim(), student_num: form.num.trim() } }), `#${props.id} 已签到`);
  busy.value = false;
  if (ok !== undefined) {
    loadDevices();
    emit("close");
  }
}
</script>

<template>
  <el-dialog :model-value="id != null" :title="`为 #${id} 签到`" width="400px" append-to-body @update:model-value="(v: boolean) => !v && emit('close')">
    <el-form ref="formRef" :model="form" label-width="56px" @submit.prevent="submit">
      <el-form-item label="姓名" prop="name" :rules="[{ required: true, whitespace: true, message: '请填写姓名' }]">
        <el-input v-model="form.name" autofocus />
      </el-form-item>
      <el-form-item label="学号" prop="num" :rules="[{ required: true, whitespace: true, message: '请填写学号' }]">
        <el-input v-model="form.num" @keyup.enter="submit" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="emit('close')">取消</el-button>
      <el-button type="primary" :loading="busy" @click="submit">确认签到</el-button>
    </template>
  </el-dialog>
</template>
