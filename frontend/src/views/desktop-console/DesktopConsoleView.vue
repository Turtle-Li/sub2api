<template>
  <DesktopConsoleLayout>
    <div class="space-y-7">
      <div class="flex flex-wrap items-end justify-between gap-4">
        <div>
          <p class="text-xs font-semibold uppercase tracking-[0.24em] text-blue-300/80">
            Desktop control plane
          </p>
          <h1 class="mt-2 text-3xl font-semibold tracking-tight text-white">
            桌面端设置
          </h1>
          <p class="mt-2 max-w-2xl text-sm leading-6 text-slate-400">
            这里只维护 TT Switch 使用的配置，不会读取或修改服务管理台的其他系统设置。
          </p>
        </div>
        <div class="flex items-center gap-3">
          <span
            v-if="lastSavedAt"
            class="text-xs text-slate-500"
            data-testid="desktop-console-last-saved"
          >
            已保存 {{ lastSavedAt }}
          </span>
          <button
            type="button"
            class="rounded-xl bg-blue-400 px-5 py-2.5 text-sm font-semibold text-slate-950 shadow-lg shadow-blue-950/30 transition hover:bg-blue-300 disabled:cursor-not-allowed disabled:opacity-50"
            :disabled="loading || saving"
            data-testid="desktop-console-save"
            @click="save"
          >
            {{ saving ? "保存中…" : "保存设置" }}
          </button>
        </div>
      </div>

      <div
        v-if="loading"
        class="grid min-h-64 place-items-center rounded-2xl border border-white/10 bg-white/[0.035]"
      >
        <div class="flex items-center gap-3 text-sm text-slate-400">
          <span class="h-4 w-4 animate-spin rounded-full border-2 border-slate-600 border-t-blue-300"></span>
          正在加载桌面配置
        </div>
      </div>

      <form v-else class="space-y-6" novalidate @submit.prevent="save">
        <section class="rounded-2xl border border-white/10 bg-white/[0.045] p-5 sm:p-6">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div>
              <h2 class="text-base font-semibold text-white">账号控制面</h2>
              <p class="mt-1 text-sm leading-6 text-slate-400">
                TT Switch 登录、订阅与账号数据所连接的服务地址。留空时使用当前发现服务器。
              </p>
            </div>
            <span
              class="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-slate-400"
            >
              Schema v{{ schemaVersion }}
            </span>
          </div>

          <label class="mt-5 block">
            <span class="mb-2 block text-xs font-medium text-slate-300">服务 Origin</span>
            <input
              v-model="form.control_plane_url"
              type="url"
              inputmode="url"
              autocomplete="off"
              class="w-full rounded-xl border border-white/10 bg-slate-950/70 px-4 py-3 font-mono text-sm text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-blue-300/60 focus:ring-2 focus:ring-blue-400/10"
              placeholder="https://accounts.example.com"
              data-testid="desktop-console-control-plane"
            />
          </label>
        </section>

        <section class="rounded-2xl border border-white/10 bg-white/[0.045] p-5 sm:p-6">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div>
              <h2 class="text-base font-semibold text-white">版本与强制更新</h2>
              <p class="mt-1 max-w-2xl text-sm leading-6 text-slate-400">
                低于最低可用版本的 TT Switch 会被服务端拦截，并只保留更新与退出能力。
              </p>
            </div>
            <label class="flex cursor-pointer items-center gap-3 rounded-xl border border-white/10 bg-slate-950/45 px-4 py-3">
              <input
                v-model="form.update_policy.enforcement_enabled"
                type="checkbox"
                class="h-4 w-4 rounded border-white/20 bg-slate-900 text-blue-400"
                data-testid="desktop-console-update-enforcement"
              />
              <span class="text-sm font-medium text-slate-200">启用强制更新</span>
            </label>
          </div>

          <div class="mt-5 grid gap-4 md:grid-cols-2">
            <label class="block">
              <span class="mb-2 block text-xs font-medium text-slate-300">最新版本</span>
              <input
                v-model.trim="form.update_policy.latest_version"
                type="text"
                autocomplete="off"
                class="w-full rounded-xl border border-white/10 bg-slate-950/70 px-4 py-3 font-mono text-sm text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-blue-300/60 focus:ring-2 focus:ring-blue-400/10"
                placeholder="1.4.0"
                data-testid="desktop-console-latest-version"
              />
            </label>
            <label class="block">
              <span class="mb-2 block text-xs font-medium text-slate-300">最低可用版本</span>
              <input
                v-model.trim="form.update_policy.minimum_supported_version"
                type="text"
                autocomplete="off"
                class="w-full rounded-xl border border-white/10 bg-slate-950/70 px-4 py-3 font-mono text-sm text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-blue-300/60 focus:ring-2 focus:ring-blue-400/10"
                placeholder="1.2.0"
                data-testid="desktop-console-minimum-version"
              />
            </label>
            <label class="block">
              <span class="mb-2 block text-xs font-medium text-slate-300">生效时间</span>
              <input
                v-model="form.update_policy.enforce_after"
                type="datetime-local"
                class="w-full rounded-xl border border-white/10 bg-slate-950/70 px-4 py-3 text-sm text-slate-100 outline-none transition focus:border-blue-300/60 focus:ring-2 focus:ring-blue-400/10"
                data-testid="desktop-console-enforce-after"
              />
            </label>
            <label class="block">
              <span class="mb-2 block text-xs font-medium text-slate-300">手动下载页</span>
              <input
                v-model.trim="form.update_policy.manual_download_url"
                type="url"
                inputmode="url"
                autocomplete="off"
                class="w-full rounded-xl border border-white/10 bg-slate-950/70 px-4 py-3 font-mono text-sm text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-blue-300/60 focus:ring-2 focus:ring-blue-400/10"
                placeholder="https://download.example.com/tt-switch"
                data-testid="desktop-console-manual-download"
              />
            </label>
          </div>

          <label class="mt-4 block">
            <span class="mb-2 block text-xs font-medium text-slate-300">更新原因</span>
            <textarea
              v-model.trim="form.update_policy.reason"
              rows="3"
              maxlength="240"
              class="w-full resize-y rounded-xl border border-white/10 bg-slate-950/70 px-4 py-3 text-sm leading-6 text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-blue-300/60 focus:ring-2 focus:ring-blue-400/10"
              placeholder="说明本次必须更新解决的问题"
              data-testid="desktop-console-update-reason"
            ></textarea>
          </label>
        </section>

        <DesktopStorageEditor />

        <DesktopPromotionsEditor v-model="form.promotions" />
      </form>
    </div>
    <TotpStepUpDialog :controller="desktopConsoleStepUp" />
  </DesktopConsoleLayout>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import TotpStepUpDialog from "@/components/auth/TotpStepUpDialog.vue";
import DesktopConsoleLayout from "@/components/desktop-console/DesktopConsoleLayout.vue";
import DesktopPromotionsEditor from "./DesktopPromotionsEditor.vue";
import DesktopStorageEditor from "./DesktopStorageEditor.vue";
import {
  getDesktopConsoleSettings,
  updateDesktopConsoleSettings,
  type DesktopPromotion,
  type DesktopUpdatePolicy,
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
const desktopConsoleStepUp = useStepUp();
const loading = ref(true);
const saving = ref(false);
const schemaVersion = ref(1);
const lastSavedAt = ref("");
const form = reactive<{
  control_plane_url: string;
  promotions: DesktopPromotion[];
  update_policy: DesktopUpdatePolicy;
}>({
  control_plane_url: "",
  promotions: [],
  update_policy: {
    schema_version: 1,
    latest_version: "",
    minimum_supported_version: "",
    enforcement_enabled: false,
    enforce_after: "",
    reason: "",
    manual_download_url: "",
  },
});

function normalizeControlPlaneURL(value: string): string | null {
  const trimmed = value.trim();
  if (!trimmed) return "";

  try {
    const parsed = new URL(trimmed);
    const loopback =
      parsed.hostname === "localhost" ||
      parsed.hostname === "127.0.0.1" ||
      parsed.hostname === "::1";
    if (
      (parsed.protocol !== "https:" && !(parsed.protocol === "http:" && loopback)) ||
      parsed.username ||
      parsed.password ||
      parsed.search ||
      parsed.hash ||
      (parsed.pathname !== "" && parsed.pathname !== "/")
    ) {
      return null;
    }
    return parsed.origin;
  } catch {
    return null;
  }
}

function toLocalDateTime(value?: string): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const offset = date.getTimezoneOffset() * 60_000;
  return new Date(date.getTime() - offset).toISOString().slice(0, 16);
}

function toRFC3339(value?: string): string {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "" : date.toISOString();
}

function setUpdatePolicy(policy?: DesktopUpdatePolicy): void {
  form.update_policy = {
    schema_version: 1,
    latest_version: policy?.latest_version ?? "",
    minimum_supported_version: policy?.minimum_supported_version ?? "",
    enforcement_enabled: Boolean(policy?.enforcement_enabled),
    enforce_after: toLocalDateTime(policy?.enforce_after),
    reason: policy?.reason ?? "",
    manual_download_url: policy?.manual_download_url ?? "",
  };
}

async function load(): Promise<void> {
  loading.value = true;
  try {
    const settings = await getDesktopConsoleSettings();
    schemaVersion.value = settings.schema_version;
    form.control_plane_url = settings.control_plane_url;
    form.promotions = settings.promotions;
    setUpdatePolicy(settings.update_policy);
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, "桌面配置加载失败"));
  } finally {
    loading.value = false;
  }
}

async function save(): Promise<void> {
  const controlPlaneURL = normalizeControlPlaneURL(form.control_plane_url);
  if (controlPlaneURL === null) {
    appStore.showError("账号控制面必须是 HTTPS Origin，不能包含路径、参数或登录凭据");
    return;
  }
  if (
    form.update_policy.enforcement_enabled &&
    !form.update_policy.minimum_supported_version
  ) {
    appStore.showError("启用强制更新前必须填写最低可用版本");
    return;
  }

  saving.value = true;
  try {
    const settings = await desktopConsoleStepUp.run(() =>
      updateDesktopConsoleSettings({
        control_plane_url: controlPlaneURL,
        promotions: form.promotions,
        update_policy: {
          ...form.update_policy,
          schema_version: 1,
          enforce_after: toRFC3339(form.update_policy.enforce_after),
        },
      }),
    );
    schemaVersion.value = settings.schema_version;
    form.control_plane_url = settings.control_plane_url;
    form.promotions = settings.promotions;
    setUpdatePolicy(settings.update_policy);
    lastSavedAt.value = new Intl.DateTimeFormat("zh-CN", {
      hour: "2-digit",
      minute: "2-digit",
    }).format(new Date());
    appStore.showSuccess("TT Switch 设置已保存");
  } catch (error) {
    if (isStepUpCancelled(error)) return;
    if (isStepUpBlocked(error)) {
      appStore.showError(stepUpBlockReason(error));
      return;
    }
    appStore.showError(extractApiErrorMessage(error, "桌面配置保存失败"));
  } finally {
    saving.value = false;
  }
}

onMounted(load);
</script>
