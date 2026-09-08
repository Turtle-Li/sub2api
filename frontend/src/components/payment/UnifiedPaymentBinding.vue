<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useStepUp, isStepUpCancelled } from '@/composables/useStepUp'
import { getUnifiedPaymentBinding, bindUnifiedPayment, useManualUnifiedPayment, type UnifiedPaymentBindingStatus } from '@/api/admin/unifiedPaymentBinding'
import { createIdempotencyKey } from '@/utils/idempotency'

const bindingMessages = {
 zh: { title: '绑定与配置同步', domain: '统一支付域名', code: '一次性授权码（首次登记时填写）', hint: '已登记的产品可直接同步。授权码由统一支付管理员签发，产品私钥由后端托管。', save: '验证并保存配置', manual: '使用手工配置', busy: '正在验证…', loadError: '无法读取接入状态，请重试。', failed: '操作未完成，请检查授权码、管理员二次认证和服务器接入配置后重试。', retry: '重试', bootstrap: '服务器尚未配置产品身份或签名密钥，请先完成一次产品初始化。', pending: '配置已保存，重启服务后加载。当前支付和退款继续使用现有配置。', disabled: '统一支付运行时尚未启用；完成验收后由管理员启用。', configured: '已保存公共接入配置', manualHint: '手工模式读取服务器中已有的接入配置；切换后同样需要重启服务。', app: '产品应用', returns: '付款返回地址', webhook: '付款通知地址', saved: '配置已验证并保存。' },
 en: { title: 'Binding and configuration sync', domain: 'Unified payment domain', code: 'One-time authorization code (first registration)', hint: 'Registered products can sync directly. The payment administrator issues the code; private keys stay on the backend.', save: 'Verify and save', manual: 'Use manual configuration', busy: 'Verifying…', loadError: 'Unable to load binding status. Please retry.', failed: 'Action failed. Check authorization, administrator MFA and server configuration, then retry.', retry: 'Retry', bootstrap: 'Initialize the product identity and signing key on the server first.', pending: 'Configuration saved. Restart the service to load it. Current payments and refunds keep using the existing configuration.', disabled: 'The unified payment runtime is disabled. An administrator can enable it after acceptance testing.', configured: 'Public integration configuration saved', manualHint: 'Manual mode uses the existing server configuration. Restart after switching.', app: 'Product application', returns: 'Payment return URL', webhook: 'Payment webhook URL', saved: 'Configuration verified and saved.' }
}
// The app ships vue-i18n's runtime-only build; constant message functions need no runtime compiler.
const { t } = useI18n({ useScope: 'local', messages: Object.fromEntries(
 Object.entries(bindingMessages).map(([locale, messages]) => [locale, Object.fromEntries(Object.entries(messages).map(([key, value]) => [key, () => value]))])
) })

interface PendingSaveMutation {
 idempotencyKey: string
 revision: number
 baseURL: string
 bindingCode: string
}

interface PendingManualMutation {
 idempotencyKey: string
 revision: number
}

const status = ref<UnifiedPaymentBindingStatus | null>(null)
const baseURL = ref('https://pay.totools.cn')
const code = ref('')
const busy = ref(false)
const error = ref('')
const notice = ref('')
const pendingSaveMutation = ref<PendingSaveMutation | null>(null)
const pendingManualMutation = ref<PendingManualMutation | null>(null)
const stepUp = useStepUp()

function clearPendingMutations() {
 pendingSaveMutation.value = null
 pendingManualMutation.value = null
}

function saveMutationFor(revision: number, nextBaseURL: string, bindingCode: string): PendingSaveMutation {
 const current = pendingSaveMutation.value
 if (current && current.revision === revision && current.baseURL === nextBaseURL && current.bindingCode === bindingCode) {
  return current
 }
 const mutation = {
  idempotencyKey: createIdempotencyKey('unified-payment-binding-save'),
  revision,
  baseURL: nextBaseURL,
  bindingCode,
 }
 pendingSaveMutation.value = mutation
 return mutation
}

function manualMutationFor(revision: number): PendingManualMutation {
 const current = pendingManualMutation.value
 if (current && current.revision === revision) return current
 const mutation = { idempotencyKey: createIdempotencyKey('unified-payment-binding-manual'), revision }
 pendingManualMutation.value = mutation
 return mutation
}

async function load() {
 error.value = ''
 try {
  const nextStatus = await getUnifiedPaymentBinding()
  status.value = nextStatus
  baseURL.value = nextStatus.base_url
  // A fresh status may carry a new optimistic-concurrency revision, so neither
  // mutation key remains valid for a future request payload.
  clearPendingMutations()
 }
 catch { error.value = t('loadError') }
}
async function save() {
 const loadedStatus = status.value
 if (busy.value || !loadedStatus?.bootstrap_ready) return
 busy.value = true; error.value = ''; notice.value = ''
 const mutation = saveMutationFor(loadedStatus.revision, baseURL.value.trim(), code.value.trim())
 try {
  status.value = await stepUp.run(() => bindUnifiedPayment(mutation.baseURL, mutation.bindingCode, mutation.revision, mutation.idempotencyKey))
  code.value = ''; clearPendingMutations(); notice.value = t('saved')
 } catch (err) { if (!isStepUpCancelled(err)) error.value = t('failed') }
 finally { busy.value = false }
}
async function manual() {
 const loadedStatus = status.value
 if (busy.value || !loadedStatus) return
 busy.value = true; error.value = ''; notice.value = ''
 const mutation = manualMutationFor(loadedStatus.revision)
 try {
  status.value = await stepUp.run(() => useManualUnifiedPayment(mutation.revision, mutation.idempotencyKey))
  code.value = ''; clearPendingMutations()
 }
 catch (err) { if (!isStepUpCancelled(err)) error.value = t('failed') }
 finally { busy.value = false }
}
onMounted(load)
</script>

<template>
 <section class="mt-4 min-w-0 border-t border-primary-200 pt-4 dark:border-primary-900/60" aria-labelledby="unified-binding-title">
  <h4 id="unified-binding-title" class="text-sm font-medium text-gray-900 dark:text-white">{{ t('title') }}</h4>
  <p class="mt-1 text-xs text-gray-600 dark:text-gray-400">{{ t('hint') }}</p>
  <p v-if="error" role="alert" class="mt-2 text-sm text-red-700 dark:text-red-300">{{ error }} <button v-if="!status" type="button" class="underline" @click="load">{{ t('retry') }}</button></p>
  <template v-if="status">
   <p v-if="!status.bootstrap_ready" class="mt-2 text-sm text-amber-800 dark:text-amber-300">{{ t('bootstrap') }}</p>
   <div class="mt-3 grid min-w-0 gap-3 sm:grid-cols-2">
    <label class="min-w-0 text-sm text-gray-700 dark:text-gray-300" for="unified-binding-domain">{{ t('domain') }}
     <input id="unified-binding-domain" v-model="baseURL" type="url" :disabled="busy" class="input mt-1 w-full" spellcheck="false" autocomplete="off" />
    </label>
    <label class="min-w-0 text-sm text-gray-700 dark:text-gray-300" for="unified-binding-code">{{ t('code') }}
     <input id="unified-binding-code" v-model="code" type="password" :disabled="busy" class="input mt-1 w-full" autocomplete="off" maxlength="43" />
    </label>
   </div>
   <dl class="mt-3 space-y-1 break-all text-xs text-gray-600 dark:text-gray-400">
    <div><dt class="inline font-medium">{{ t('app') }}: </dt><dd class="inline">{{ status.app_id || '—' }} · {{ status.environment || '—' }}</dd></div>
    <div><dt class="inline font-medium">{{ t('returns') }}: </dt><dd class="inline">{{ status.return_url || '—' }}</dd></div>
    <div><dt class="inline font-medium">{{ t('webhook') }}: </dt><dd class="inline">{{ status.webhook_url || '—' }}</dd></div>
   </dl>
   <div class="mt-3 flex flex-wrap gap-2">
    <button type="button" class="btn btn-primary" :disabled="busy || !status.bootstrap_ready" @click="save">{{ busy ? t('busy') : t('save') }}</button>
    <button v-if="status.configured" type="button" class="btn btn-secondary" :disabled="busy" @click="manual">{{ t('manual') }}</button>
   </div>
   <p v-if="status.pending_restart" role="status" class="mt-3 text-sm text-amber-800 dark:text-amber-300">{{ t('pending') }}</p>
   <p v-else-if="notice" role="status" class="mt-3 text-sm text-emerald-800 dark:text-emerald-300">{{ notice }}</p>
   <p v-else-if="status.configured" class="mt-3 text-xs text-gray-600 dark:text-gray-400">{{ t('configured') }}</p>
   <p v-if="!status.runtime_enabled" class="mt-2 text-xs text-gray-600 dark:text-gray-400">{{ t('disabled') }}</p>
   <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('manualHint') }}</p>
  </template>
  <TotpStepUpDialog :controller="stepUp" />
 </section>
</template>
