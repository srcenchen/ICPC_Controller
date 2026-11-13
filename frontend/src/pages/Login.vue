<script setup lang="ts">
import { ref } from "vue";
import { Lock } from "@element-plus/icons-vue";
import { api, errorText } from "@/lib/api";

const password = ref("");
const error = ref("");
const busy = ref(false);

async function submit() {
  if (!password.value || busy.value) return;
  busy.value = true;
  error.value = "";
  try {
    await api("/api/auth/login", { json: { password: password.value } });
    location.href = "/";
  } catch (err) {
    error.value = errorText(err);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="login">
    <el-card class="box" shadow="always">
      <div class="head">
        <span class="logo">IC</span>
        <div>
          <h1>ICPC 远程集控</h1>
          <p>管理员登录</p>
        </div>
      </div>
      <el-form @submit.prevent="submit">
        <el-form-item :error="error">
          <el-input v-model="password" type="password" size="large" placeholder="管理员密码" show-password autofocus :prefix-icon="Lock" />
        </el-form-item>
        <el-button type="primary" size="large" native-type="submit" :loading="busy" :disabled="!password" class="full">进入控制台</el-button>
      </el-form>
    </el-card>
  </div>
</template>

<style scoped>
.login { min-height: 100vh; display: grid; place-items: center; padding: 16px;
  background: radial-gradient(circle at 20% 10%, var(--el-color-primary-light-8), transparent 45%), var(--app-bg); }
.box { width: 100%; max-width: 380px; border-radius: 14px; }
.head { display: flex; align-items: center; gap: 12px; margin-bottom: 24px; }
.logo { width: 44px; height: 44px; border-radius: 11px; display: grid; place-items: center; color: #fff; font-weight: 700;
  background: linear-gradient(135deg, var(--el-color-primary), var(--el-color-primary-light-3)); }
h1 { margin: 0; font-size: 18px; }
p { margin: 2px 0 0; font-size: 12px; color: var(--el-text-color-secondary); }
.full { width: 100%; }
</style>
