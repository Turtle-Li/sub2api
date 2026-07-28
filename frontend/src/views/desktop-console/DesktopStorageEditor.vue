<template>
  <section class="rounded-2xl border border-white/10 bg-white/[0.045] p-5 sm:p-6">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div>
        <div class="flex flex-wrap items-center gap-2">
          <h2 class="text-base font-semibold text-white">腾讯云 COS</h2>
          <span
            v-if="secretConfigured"
            class="rounded-full border border-emerald-300/20 bg-emerald-400/10 px-2.5 py-1 text-xs text-emerald-200"
          >
            SecretKey 已保存
          </span>
        </div>
        <p class="mt-1 text-sm leading-6 text-slate-400">
          TT Switch 更新包与主题商店使用的专用存储桶。
        </p>
      </div>
      <label class="flex cursor-pointer items-center gap-3 rounded-xl border border-white/10 bg-slate-950/45 px-4 py-3">
        <input
          v-model="form.enabled"
          type="checkbox"
          class="h-4 w-4 rounded border-white/20 bg-slate-900 text-blue-400"
          data-testid="desktop-storage-enabled"
        />
        <span class="text-sm font-medium text-slate-200">启用 COS</span>
      </label>
    </div>

    <div v-if="loading" class="mt-5 text-sm text-slate-500">正在加载 COS 配置</div>

    <template v-else>
      <div class="mt-5 grid gap-4 md:grid-cols-2">
        <label class="block">
          <span class="mb-2 block text-xs font-medium text-slate-300">地域</span>
          <input
            v-model.trim="form.region"
            type="text"
            autocomplete="off"
            class="w-full rounded-xl border border-white/10 bg-slate-950/70 px-4 py-3 font-mono text-sm text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-blue-300/60 focus:ring-2 focus:ring-blue-400/10"
            placeholder="ap-guangzhou"
            data-testid="desktop-storage-region"
          />
        </label>
        <label class="block">
          <span class="mb-2 block text-xs font-medium text-slate-300">Bucket</span>
          <input
            v-model.trim="form.bucket"
            type="text"
            autocomplete="off"
            class="w-full rounded-xl border border-white/10 bg-slate-950/70 px-4 py-3 font-mono text-sm text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-blue-300/60 focus:ring-2 focus:ring-blue-400/10"
            placeholder="tt-switch-1250000000"
            data-testid="desktop-storage-bucket"
          />
        </label>
        <label class="block">
          <span class="mb-2 block text-xs font-medium text-slate-300">SecretId</span>
          <input
            v-model.trim="form.secret_id"
            type="text"
            autocomplete="off"
            class="w-full rounded-xl border border-white/10 bg-slate-950/70 px-4 py-3 font-mono text-sm text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-blue-300/60 focus:ring-2 focus:ring-blue-400/10"
            placeholder="AKID…"
            data-testid="desktop-storage-secret-id"
          />
        </label>
        <label class="block">
          <span class="mb-2 block text-xs font-medium text-slate-300">SecretKey</span>
          <input
            v-model="form.secret_key"
            type="password"
            autocomplete="new-password"
            class="w-full rounded-xl border border-white/10 bg-slate-950/70 px-4 py-3 font-mono text-sm text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-blue-300/60 focus:ring-2 focus:ring-blue-400/10"
            :placeholder="secretConfigured ? '留空保持当前密钥' : '输入 COS SecretKey'"
            data-testid="desktop-storage-secret-key"
          />
        </label>
      </div>

      <label class="mt-4 block">
        <span class="mb-2 block text-xs font-medium text-slate-300">公开下载域名</span>
        <input
          v-model.trim="form.public_base_url"
          type="url"
          inputmode="url"
          autocomplete="off"
          class="w-full rounded-xl border border-white/10 bg-slate-950/70 px-4 py-3 font-mono text-sm text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-blue-300/60 focus:ring-2 focus:ring-blue-400/10"
          placeholder="https://download.example.com"
          data-testid="desktop-storage-public-url"
        />
      </label>

      <div class="mt-4 grid gap-4 md:grid-cols-3">
        <label class="block">
          <span class="mb-2 block text-xs font-medium text-slate-300">更新目录</span>
          <input
            v-model.trim="form.release_prefix"
            type="text"
            autocomplete="off"
            class="w-full rounded-xl border border-white/10 bg-slate-950/70 px-4 py-3 font-mono text-sm text-slate-100 outline-none transition focus:border-blue-300/60 focus:ring-2 focus:ring-blue-400/10"
            data-testid="desktop-storage-release-prefix"
          />
        </label>
        <label class="block">
          <span class="mb-2 block text-xs font-medium text-slate-300">主题目录</span>
          <input
            v-model.trim="form.theme_prefix"
            type="text"
            autocomplete="off"
            class="w-full rounded-xl border border-white/10 bg-slate-950/70 px-4 py-3 font-mono text-sm text-slate-100 outline-none transition focus:border-blue-300/60 focus:ring-2 focus:ring-blue-400/10"
            data-testid="desktop-storage-theme-prefix"
          />
        </label>
        <label class="block">
          <span class="mb-2 block text-xs font-medium text-slate-300">隔离目录</span>
          <input
            v-model.trim="form.quarantine_prefix"
            type="text"
            autocomplete="off"
            class="w-full rounded-xl border border-white/10 bg-slate-950/70 px-4 py-3 font-mono text-sm text-slate-100 outline-none transition focus:border-blue-300/60 focus:ring-2 focus:ring-blue-400/10"
            data-testid="desktop-storage-quarantine-prefix"
          />
        </label>
      </div>

      <div
        v-if="endpoint || releaseManifestURL"
        class="mt-4 rounded-xl border border-white/10 bg-slate-950/45 px-4 py-3 text-xs text-slate-400"
      >
        <p v-if="endpoint" class="break-all">
          <span class="text-slate-500">COS Endpoint</span>
          <span class="ml-2 font-mono text-slate-300">{{ endpoint }}</span>
        </p>
        <p v-if="releaseManifestURL" class="mt-2 break-all">
          <span class="text-slate-500">更新清单</span>
          <span class="ml-2 font-mono text-slate-300">{{ releaseManifestURL }}</span>
        </p>
      </div>

      <div class="mt-5 flex flex-wrap items-center justify-end gap-3">
        <button
          type="button"
          class="rounded-xl border border-white/10 bg-white/5 px-4 py-2.5 text-sm font-semibold text-slate-200 transition hover:bg-white/10 disabled:cursor-not-allowed disabled:opacity-50"
          :disabled="saving || testing"
          data-testid="desktop-storage-test"
          @click="testConnection"
        >
          {{ testing ? "测试中…" : "测试连接" }}
        </button>
        <button
          type="button"
          class="rounded-xl bg-blue-400 px-4 py-2.5 text-sm font-semibold text-slate-950 transition hover:bg-blue-300 disabled:cursor-not-allowed disabled:opacity-50"
          :disabled="saving || testing"
          data-testid="desktop-storage-save"
          @click="save"
        >
          {{ saving ? "保存中…" : "保存 COS" }}
        </button>
      </div>
    </template>

    <TotpStepUpDialog :controller="storageStepUp" />
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import TotpStepUpDialog from "@/components/auth/TotpStepUpDialog.vue";
import {
  getDesktopStorageSettings,
  testDesktopStorageConnection,
  updateDesktopStorageSettings,
  type DesktopStorageConfig,
  type DesktopStorageSettings,
} from "@/api/desktopConsole";
import {
  isStepUpBlocked,
  isStepUpCancelled,
  stepUpBlockReason,
  useStepUp,
} from "@/composables/useStepUp";
import { useAppStore } from "@/stores";
import { extractApiErrorMessage } from "@/utils/apiError";

const appStore = useAppStore();
const storageStepUp = useStepUp();
const props = defineProps<{
  previewSettings?: DesktopStorageSettings;
}>();
const loading = ref(true);
const saving = ref(false);
const testing = ref(false);
const secretConfigured = ref(false);
const endpoint = ref("");
const releaseManifestURL = ref("");

const form = reactive<DesktopStorageConfig>({
  schema_version: 1,
  enabled: false,
  provider: "tencent_cos",
  region: "",
  bucket: "",
  secret_id: "",
  secret_key: "",
  public_base_url: "",
  release_prefix: "releases/",
  theme_prefix: "themes/",
  quarantine_prefix: "theme-quarantine/",
});

function applySettings(settings: DesktopStorageSettings): void {
  Object.assign(form, settings.config, { secret_key: "" });
  secretConfigured.value = settings.secret_configured;
  endpoint.value = settings.endpoint;
  releaseManifestURL.value = settings.release_manifest_url;
}

function validate(): boolean {
  if (!form.region || !form.bucket || !form.secret_id) {
    appStore.showError("请填写地域、Bucket 和 SecretId");
    return false;
  }
  if (!form.secret_key && !secretConfigured.value) {
    appStore.showError("请填写 SecretKey");
    return false;
  }
  if (!form.public_base_url.startsWith("https://")) {
    appStore.showError("公开下载域名必须使用 HTTPS");
    return false;
  }
  return true;
}

async function load(): Promise<void> {
  if (props.previewSettings) {
    applySettings(props.previewSettings);
    loading.value = false;
    return;
  }
  loading.value = true;
  try {
    applySettings(await getDesktopStorageSettings());
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, "COS 配置加载失败"));
  } finally {
    loading.value = false;
  }
}

async function save(): Promise<void> {
  if (!validate()) return;
  saving.value = true;
  try {
    const settings = await storageStepUp.run(() =>
      updateDesktopStorageSettings({ ...form }),
    );
    applySettings(settings);
    appStore.showSuccess("TT Switch COS 配置已保存");
  } catch (error) {
    if (isStepUpCancelled(error)) return;
    if (isStepUpBlocked(error)) {
      appStore.showError(stepUpBlockReason(error));
      return;
    }
    appStore.showError(extractApiErrorMessage(error, "COS 配置保存失败"));
  } finally {
    saving.value = false;
  }
}

async function testConnection(): Promise<void> {
  if (!validate()) return;
  testing.value = true;
  try {
    const result = await storageStepUp.run(() =>
      testDesktopStorageConnection({ ...form, enabled: true }),
    );
    if (!result.ok) {
      appStore.showError(result.message || "COS 连接测试失败");
      return;
    }
    endpoint.value = result.endpoint ?? endpoint.value;
    appStore.showSuccess("COS 写入、公开读取和删除测试通过");
  } catch (error) {
    if (isStepUpCancelled(error)) return;
    if (isStepUpBlocked(error)) {
      appStore.showError(stepUpBlockReason(error));
      return;
    }
    appStore.showError(extractApiErrorMessage(error, "COS 连接测试失败"));
  } finally {
    testing.value = false;
  }
}

onMounted(load);
</script>
