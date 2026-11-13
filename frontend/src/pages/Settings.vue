<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { Plus, Delete, CopyDocument, Download, Key, Upload } from "@element-plus/icons-vue";
import { api, apiPath } from "@/lib/api";
import { store, isCloud, run, toast, confirmAction, toastError } from "@/lib/store";
import type { Deployment, Preset, Settings } from "@/lib/types";

const tab = ref(store.roomID ? "presets" : "deploy");
const loading = ref(true);

const deploy = ref<Deployment>({
  mode: "standalone",
  node_id: "",
  room_name: "",
  cloud_url: "",
  token: "",
  token_set: false,
  allow_insecure: false,
  device_id_start: 1,
  advertise_ip: "",
});
const hostnamePrefix = ref("cwxu-icpc");
const presets = ref<Preset[]>([]);
const checkin = ref<Settings["checkin_config"]>({
  welcome_text: "",
  warning_text: "",
  post_checkin_msg: "",
  post_checkout_cmd: "",
  post_checkout_msg: "",
});
const maint = ref<Settings["maintenance"]>({
  cmd_retention_days: 30,
  cmd_retention_max: 5000,
  event_retention_day: 30,
  disk_alert_pct: 90,
  temp_alert_c: 85,
  mem_alert_pct: 90,
});
const pwd = ref({ oldPassword: "", newPassword: "", confirm: "" });

const installCmd = computed(() => `curl -fsSL ${location.origin}/install.sh | sudo bash`);

async function load() {
  loading.value = true;
  try {
    const [s, p, c] = await Promise.all([
      api<Settings>("/api/settings"),
      api<Preset[]>("/api/settings/presets").catch(() => null),
      api<Settings["checkin_config"]>("/api/settings/checkin").catch(() => null),
    ]);
    if (s.deployment) deploy.value = { ...deploy.value, ...s.deployment, token: s.deployment.token || "" };
    hostnamePrefix.value = s.hostname_prefix || "cwxu-icpc";
    if (s.maintenance) maint.value = { ...maint.value, ...s.maintenance };
    presets.value = p || s.presets || [];
    checkin.value = { ...checkin.value, ...(c || s.checkin_config || {}) };
  } catch (err) {
    toastError(err);
  } finally {
    loading.value = false;
  }
}

onMounted(load);

function genToken() {
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  deploy.value.token = Array.from(bytes, (v) => v.toString(16).padStart(2, "0")).join("");
}

function copyText(text: string, msg = "已复制") {
  navigator.clipboard.writeText(text).then(() => toast(msg, "success")).catch(() => toast("复制失败", "error"));
}

async function saveDeploy() {
  if (!(await confirmAction("保存部署配置可能切换运行模式或重连云端。确定继续？"))) return;
  const ok = await run(() => api("/api/settings", { json: { deployment: deploy.value } }), "部署配置已保存");
  if (ok !== undefined) location.href = "/";
}

async function savePrefix() {
  if (!hostnamePrefix.value.trim()) return toast("主机名前缀不能为空", "warning");
  await run(() => api("/api/settings", { json: { hostname_prefix: hostnamePrefix.value.trim() } }), "主机名前缀已保存");
}

async function savePresets() {
  if (presets.value.some((p) => !p.name.trim() || !p.command.trim())) {
    return toast("每条预设的名称和命令都不能为空", "warning");
  }
  const saved = await run(() => api<Preset[]>("/api/settings/presets", { method: "PUT", json: presets.value }), "预设命令已保存");
  if (saved) presets.value = saved;
}

async function saveCheckin() {
  await run(() => api("/api/settings/checkin", { method: "PUT", json: checkin.value }), "签到页文案已保存");
}

async function saveMaint() {
  await run(() => api("/api/settings", { json: { maintenance: maint.value } }), "保留与告警配置已保存");
}

async function pushClientUpdate() {
  if (!(await confirmAction("向所有在线选手机推送客户端自更新？（需已上传名为 icpc-client 的新客户端二进制）"))) return;
  const res = await run(() => api<{ sent: number }>("/api/client/update", { method: "POST" }));
  if (res) toast(`已向 ${res.sent} 台在线选手机推送更新指令`, "success");
}

async function changePassword() {
  if (!pwd.value.oldPassword || !pwd.value.newPassword) return toast("请填写旧密码和新密码", "warning");
  if (pwd.value.newPassword !== pwd.value.confirm) return toast("两次输入的新密码不一致", "warning");
  const ok = await run(() => api("/api/auth/password", { json: { old_password: pwd.value.oldPassword, new_password: pwd.value.newPassword } }), "管理员密码已修改");
  if (ok !== undefined) {
    pwd.value = { oldPassword: "", newPassword: "", confirm: "" };
  }
}
</script>

<template>
  <div v-loading="loading" class="page settings-page">
    <div class="page-head">
      <div>
        <h1>设置</h1>
        <p v-if="store.roomID">正在查看机房专属配置时，“预设命令”与“签到文案”作用于当前选中机房；部署、备份与管理员密码属于当前服务端。</p>
        <p v-else>管理部署模式、选手机接入、预设命令、签到文案、数据备份与管理员密码。</p>
      </div>
    </div>

    <el-tabs v-model="tab" type="border-card" class="tabs">
      <!-- Tab 1: Deployment -->
      <el-tab-pane label="部署与接入" name="deploy">
        <div class="section-grid">
          <el-card shadow="never">
            <template #header>
              <div class="card-title">部署模式与机房身份 <span v-if="deploy.node_id" class="muted small mono">UUID: {{ deploy.node_id }}</span></div>
            </template>
            <el-form label-width="120px">
              <el-form-item label="运行模式">
                <el-radio-group v-model="deploy.mode">
                  <el-radio-button value="standalone">单机模式</el-radio-button>
                  <el-radio-button value="relay">并机 · 机房中转</el-radio-button>
                  <el-radio-button value="cloud">云端汇总</el-radio-button>
                </el-radio-group>
              </el-form-item>
              <el-form-item v-if="deploy.mode !== 'cloud'" label="机房名称">
                <el-input v-model="deploy.room_name" placeholder="例如 A301 机房" />
              </el-form-item>
              <el-form-item v-if="deploy.mode === 'relay'" label="云端根地址">
                <el-input v-model="deploy.cloud_url" placeholder="https://control.example.com（不带末尾斜杠或子路径）" />
              </el-form-item>
              <el-form-item v-if="deploy.mode !== 'standalone'" label="连接密钥">
                <div class="row nowrap" style="width: 100%">
                  <el-input v-model="deploy.token" class="mono" placeholder="至少 32 字符随机密钥" show-password />
                  <el-button :icon="Key" @click="genToken">生成</el-button>
                  <el-button :icon="CopyDocument" :disabled="!deploy.token" @click="copyText(deploy.token || '', '密钥已复制')">复制</el-button>
                </div>
              </el-form-item>
              <el-form-item v-if="deploy.mode !== 'cloud'" label="新设备起始编号">
                <el-input-number v-model="deploy.device_id_start" :min="1" :max="99999" />
                <span class="muted small" style="margin-left: 10px">仅对新注册设备生效，已有设备不重排</span>
              </el-form-item>
              <el-form-item v-if="deploy.mode !== 'cloud'" label="中转局域网 IP">
                <el-input v-model="deploy.advertise_ip" placeholder="多网卡时建议填写选手机可访问的局域网 IP，可留空" />
              </el-form-item>
              <el-form-item v-if="deploy.mode !== 'standalone'" label="HTTP 选项">
                <el-checkbox v-model="deploy.allow_insecure">仅在可信内网测试时允许非 HTTPS 连接</el-checkbox>
              </el-form-item>
              <el-form-item>
                <el-button type="primary" @click="saveDeploy">保存并应用部署模式</el-button>
              </el-form-item>
            </el-form>
          </el-card>

          <div class="stack">
            <el-card shadow="never">
              <template #header><div class="card-title">选手机主机名前缀</div></template>
              <p class="muted small" style="margin-top: 0">新选手机注册后自动重命名为 <b class="mono">{{ hostnamePrefix || "cwxu-icpc" }}-1</b>、<b class="mono">{{ hostnamePrefix || "cwxu-icpc" }}-2</b>…</p>
              <div class="row nowrap">
                <el-input v-model="hostnamePrefix" class="mono" placeholder="cwxu-icpc" />
                <el-button type="primary" @click="savePrefix">保存</el-button>
              </div>
            </el-card>

            <el-card v-if="!isCloud" shadow="never">
              <template #header><div class="card-title">选手机一键安装脚本</div></template>
              <p class="muted small" style="margin-top: 0">在选手机（Ubuntu/Debian/Arch 等 root 终端）执行以下命令即可安装并开机自启：</p>
              <div class="row nowrap">
                <el-input :model-value="installCmd" readonly class="mono" />
                <el-button :icon="CopyDocument" @click="copyText(installCmd, '安装命令已复制')">复制</el-button>
              </div>
            </el-card>
          </div>
        </div>
      </el-tab-pane>

      <!-- Tab 2: Presets -->
      <el-tab-pane label="预设命令" name="presets">
        <div class="row" style="margin-bottom: 12px">
          <span class="muted small">预设命令会显示在“批量命令”页顶部，点击即可一键填入。</span>
          <span class="spacer" />
          <el-button :icon="Plus" @click="presets.push({ name: '', desc: '', command: '', color: 'primary' })">添加预设</el-button>
          <el-button type="primary" @click="savePresets">保存全部预设</el-button>
        </div>
        <el-table :data="presets" size="small" border empty-text="暂无预设命令">
          <el-table-column label="按钮名称" width="180">
            <template #default="{ row }"><el-input v-model="row.name" placeholder="如：清理选手目录" /></template>
          </el-table-column>
          <el-table-column label="备注说明" width="220">
            <template #default="{ row }"><el-input v-model="row.desc" placeholder="鼠标悬停时显示（可选）" /></template>
          </el-table-column>
          <el-table-column label="Shell 命令" min-width="280">
            <template #default="{ row }"><el-input v-model="row.command" class="mono" placeholder="例如：hostname && uptime" /></template>
          </el-table-column>
          <el-table-column width="64" align="center">
            <template #default="{ $index }"><el-button link type="danger" :icon="Delete" @click="presets.splice($index, 1)" /></template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <!-- Tab 3: Checkin Config -->
      <el-tab-pane label="签到页文案" name="checkin">
        <el-form label-width="140px" style="max-width: 720px">
          <el-form-item label="欢迎语">
            <el-input v-model="checkin.welcome_text" type="textarea" :rows="2" placeholder="选手机本机签到页顶部欢迎文案" />
          </el-form-item>
          <el-form-item label="赛场须知 / 警告">
            <el-input v-model="checkin.warning_text" type="textarea" :rows="2" placeholder="比赛纪律提醒文案" />
          </el-form-item>
          <el-form-item label="签到成功提示">
            <el-input v-model="checkin.post_checkin_msg" placeholder="例如：签到成功，祝比赛顺利！" />
          </el-form-item>
          <el-form-item label="签退后自动执行">
            <el-input v-model="checkin.post_checkout_cmd" class="mono" placeholder="选手机签退时自动运行的命令（可留空）" />
          </el-form-item>
          <el-form-item label="签退成功提示">
            <el-input v-model="checkin.post_checkout_msg" type="textarea" :rows="2" placeholder="签退后显示的提示语" />
          </el-form-item>
          <el-form-item>
            <el-button type="primary" @click="saveCheckin">保存签到配置并下发</el-button>
          </el-form-item>
        </el-form>
      </el-tab-pane>

      <!-- Tab 4: Maintenance & Client Update -->
      <el-tab-pane label="告警与客户端更新" name="maint">
        <div class="section-grid">
          <el-card shadow="never">
            <template #header><div class="card-title">日志保留与硬件告警阈值</div></template>
            <el-form label-width="140px">
              <el-form-item label="命令日志保留天数"><el-input-number v-model="maint.cmd_retention_days" :min="1" :max="3650" /></el-form-item>
              <el-form-item label="命令日志条数上限"><el-input-number v-model="maint.cmd_retention_max" :min="100" :max="1000000" :step="500" /></el-form-item>
              <el-form-item label="上下线事件保留天数"><el-input-number v-model="maint.event_retention_day" :min="1" :max="3650" /></el-form-item>
              <el-form-item label="磁盘占用告警 (%)"><el-input-number v-model="maint.disk_alert_pct" :min="50" :max="100" /></el-form-item>
              <el-form-item label="内存占用告警 (%)"><el-input-number v-model="maint.mem_alert_pct" :min="50" :max="100" /></el-form-item>
              <el-form-item label="CPU 温度告警 (°C)"><el-input-number v-model="maint.temp_alert_c" :min="40" :max="120" /></el-form-item>
              <el-form-item><el-button type="primary" @click="saveMaint">保存配置</el-button></el-form-item>
            </el-form>
          </el-card>

          <el-card shadow="never">
            <template #header><div class="card-title">选手机客户端在线自更新</div></template>
            <p class="muted small" style="margin-top: 0">
              1. 先前往“文件分发”页上传名为 <code class="mono">icpc-client</code> 的 Linux 客户端二进制文件；<br />
              2. 点击下方按钮，所有在线选手机会下载校验新版本并自动重启服务。
            </p>
            <el-button type="warning" :icon="Upload" @click="pushClientUpdate">向在线选手机推送自更新</el-button>
          </el-card>
        </div>
      </el-tab-pane>

      <!-- Tab 5: Backup & Password -->
      <el-tab-pane label="备份与密码" name="security">
        <div class="section-grid">
          <el-card shadow="never">
            <template #header><div class="card-title">数据导出与一致性备份</div></template>
            <p class="muted small" style="margin-top: 0">
              备份 ZIP 使用 SQLite <code class="mono">VACUUM INTO</code> 快照，包含凭据与学生签到信息，请妥善保管。<br />
              停机恢复命令：<code class="mono">./server --db icpc.db --restore backup.zip</code>
            </p>
            <div class="row" style="gap: 10px">
              <el-button type="primary" tag="a" :href="'/api/data/backup'" :icon="Download">下载配置与数据 ZIP</el-button>
              <el-button tag="a" :href="'/api/data/backup?uploads=true'" :icon="Download">完整备份 ZIP（含分发文件）</el-button>
            </div>
            <el-divider />
            <div class="row" style="gap: 10px">
              <el-button tag="a" :href="apiPath('/api/devices/export')" :icon="Download">导出设备 Excel</el-button>
              <el-button tag="a" :href="apiPath('/api/checkin/export')" :icon="Download">导出签到 Excel</el-button>
              <el-button tag="a" :href="'/api/data/export'" :icon="Download">导出脱敏业务 JSON</el-button>
            </div>
          </el-card>

          <el-card shadow="never">
            <template #header><div class="card-title">修改管理员密码</div></template>
            <el-form label-width="100px" @submit.prevent="changePassword">
              <el-form-item label="当前密码">
                <el-input v-model="pwd.oldPassword" type="password" show-password autocomplete="current-password" />
              </el-form-item>
              <el-form-item label="新密码">
                <el-input v-model="pwd.newPassword" type="password" show-password autocomplete="new-password" />
              </el-form-item>
              <el-form-item label="确认新密码">
                <el-input v-model="pwd.confirm" type="password" show-password autocomplete="new-password" @keyup.enter="changePassword" />
              </el-form-item>
              <el-form-item>
                <el-button type="primary" native-type="submit">修改密码</el-button>
              </el-form-item>
            </el-form>
          </el-card>
        </div>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<style scoped>
.tabs { border-radius: 10px; overflow: hidden; }
.section-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(380px, 1fr)); gap: 16px; align-items: start; }
</style>
