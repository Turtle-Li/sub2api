<template>
  <AppLayout>
    <div class="space-y-4">
      <div class="flex justify-end">
        <RouterLink to="/admin/accounts" class="btn btn-secondary">{{ t('admin.codexTurnState.accounts') }}</RouterLink>
      </div>
      <div v-if="panelStartupError" role="alert" class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200">
        <span>{{ t('admin.codexTurnState.unavailable') }}</span>
        <button type="button" class="btn btn-secondary" @click="reloadPanel">{{ t('common.refresh') }}</button>
      </div>
      <iframe :key="panelGeneration" ref="panel" :srcdoc="panelDocument" sandbox="allow-scripts" :title="t('admin.codexTurnState.title')" class="block w-full border-0" :style="{ height: `${panelHeight}px` }" @load="syncPanelTheme" />

      <BaseDialog
        :show="accountPickerOpen"
        :title="t('admin.codexTurnState.accountPicker.title')"
        @close="cancelAccountPicker"
      >
        <div class="space-y-3">
          <div>
            <label for="codex-turn-state-account-picker" class="input-label">
              {{ t('admin.codexTurnState.accountPicker.label') }}
            </label>
            <Select
              id="codex-turn-state-account-picker"
              v-model="accountPickerValue"
              :options="accountPickerOptions"
              :placeholder="t('admin.codexTurnState.accountPicker.placeholder')"
              remote
              :loading="accountPickerLoading"
              @search="searchEligibleAccounts"
            />
          </div>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.codexTurnState.accountPicker.hint') }}</p>
          <p v-if="hasIncompatibleAccountNames" class="text-xs text-amber-700 dark:text-amber-300">
            {{ t('admin.codexTurnState.accountPicker.incompatibleHint') }}
          </p>
          <p v-if="accountPickerError" role="alert" class="text-sm text-red-700 dark:text-red-300">
            {{ accountPickerError }}
          </p>
          <p v-else-if="!accountPickerLoading && !accountPickerOptions.length" class="text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.codexTurnState.accountPicker.empty') }}
          </p>
        </div>

        <template #footer>
          <div class="flex justify-end gap-2">
            <button type="button" class="btn btn-secondary" @click="cancelAccountPicker">{{ t('common.cancel') }}</button>
            <button type="button" class="btn btn-primary" :disabled="!selectedAccount || accountPickerLoading" @click="confirmAccountPicker">
              {{ t('admin.codexTurnState.accountPicker.confirm') }}
            </button>
          </div>
        </template>
      </BaseDialog>
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import { list as listAccounts } from '@/api/admin/accounts'
import { requestCodexPanel } from '@/api/admin/codexTurnState'
import panelHTML from '@/assets/codex-turn-state-panel.html?raw'

interface EligibleAccount {
  id: number
  name: string
}

const eligibleAccountTypes = ['oauth', 'setup-token'] as const

const { t } = useI18n()
const panel = ref<HTMLIFrameElement | null>(null)
// srcdoc inherits the host CSP. Reuse its per-response nonce instead of
// weakening script-src or granting same-origin access to the frame.
const hostNonce = document.querySelector<HTMLScriptElement>('script[nonce]')?.nonce || ''
const escapedNonce = hostNonce.replace(/[&"<>]/g, value => ({ '&': '&amp;', '"': '&quot;', '<': '&lt;', '>': '&gt;' })[value]!)
const initialTheme = document.documentElement.classList.contains('dark') ? 'dark' : 'light'
const panelDocument = panelHTML.replace('<html lang="zh-CN">', `<html lang="zh-CN" data-theme="${initialTheme}">`)
  .replace(/<script>/g, `<script nonce="${escapedNonce}">`)
const panelHeight = ref(900)
const panelGeneration = ref(0)
const panelStartupError = ref(false)
let startupTimer: ReturnType<typeof setTimeout> | undefined
let themeObserver: MutationObserver | undefined
function markPanelReady() {
  clearTimeout(startupTimer)
  panelStartupError.value = false
}
function armStartupTimeout() {
  clearTimeout(startupTimer)
  startupTimer = setTimeout(() => { panelStartupError.value = true }, 12000)
}
function syncPanelTheme() {
  postToPanel({ type: 'ctsm-theme', theme: document.documentElement.classList.contains('dark') ? 'dark' : 'light' })
}
function reloadPanel() {
  panelStartupError.value = false
  panelGeneration.value += 1
  armStartupTimeout()
}
const active = new Set<string>()
const accountPickerOpen = ref(false)
const accountPickerRequestID = ref<string | null>(null)
const accountPickerAccounts = ref<EligibleAccount[]>([])
const selectedAccount = ref<EligibleAccount | null>(null)
const accountPickerLoading = ref(false)
const accountPickerError = ref('')
let accountSearchAbort: AbortController | null = null
let accountSearchSequence = 0
const isSupportedAccountName = (name: string) => /^[^\p{Cc}]{1,128}$/u.test(name)

const isValidMessageID = (value: unknown): value is string =>
  typeof value === 'string' && /^[a-z0-9-]{1,80}$/.test(value)

const accountPickerOptions = computed(() => {
  const accounts = [...accountPickerAccounts.value]
  const selected = selectedAccount.value
  if (selected && !accounts.some((account) => account.id === selected.id)) accounts.unshift(selected)
  return accounts.map((account) => {
    const compatible = isSupportedAccountName(account.name)
    return {
      value: String(account.id),
      label: compatible ? account.name : `${account.name} (${t('admin.codexTurnState.accountPicker.incompatible')})`,
      disabled: !compatible,
    }
  })
})

const hasIncompatibleAccountNames = computed(() =>
  accountPickerAccounts.value.some((account) => !isSupportedAccountName(account.name)),
)

const accountPickerValue = computed<string>({
  get: () => selectedAccount.value ? String(selectedAccount.value.id) : '',
  set: (value) => {
    const account = accountPickerAccounts.value.find((item) => String(item.id) === value) ?? null
    selectedAccount.value = account && isSupportedAccountName(account.name) ? account : null
  },
})

function postToPanel(message: Record<string, unknown>) {
  panel.value?.contentWindow?.postMessage(message, '*')
}

async function loadEligibleAccounts(search = '') {
  if (!accountPickerOpen.value) return
  const sequence = ++accountSearchSequence
  accountSearchAbort?.abort()
  const controller = new AbortController()
  accountSearchAbort = controller
  accountPickerLoading.value = true
  accountPickerError.value = ''
  try {
    const results = await Promise.all(eligibleAccountTypes.map((type) => listAccounts(
      1,
      50,
      {
        platform: 'openai',
        type,
        status: 'active',
        lite: 'true',
        ...(search ? { search } : {}),
      },
      { signal: controller.signal },
    )))
    if (sequence !== accountSearchSequence) return
    const uniqueAccounts = new Map<number, EligibleAccount>()
    for (const result of results) {
      for (const account of result.items) {
        if (!uniqueAccounts.has(account.id)) uniqueAccounts.set(account.id, { id: account.id, name: account.name })
      }
    }
    accountPickerAccounts.value = [...uniqueAccounts.values()]
  } catch {
    if (controller.signal.aborted || sequence !== accountSearchSequence) return
    accountPickerError.value = t('admin.codexTurnState.accountPicker.loadError')
    accountPickerAccounts.value = []
  } finally {
    if (sequence === accountSearchSequence) accountPickerLoading.value = false
  }
}

function searchEligibleAccounts(query: string) {
  void loadEligibleAccounts(query)
}

function finishAccountPicker(account: EligibleAccount | null) {
  const id = accountPickerRequestID.value
  accountPickerOpen.value = false
  accountPickerRequestID.value = null
  selectedAccount.value = null
  accountSearchAbort?.abort()
  accountSearchAbort = null
  accountSearchSequence += 1
  if (id) postToPanel({ type: 'ctsm-account-picked', id, account: account ? { id: account.id, name: account.name } : null })
}

function openAccountPicker(id: string) {
  if (accountPickerOpen.value) return
  accountPickerRequestID.value = id
  accountPickerAccounts.value = []
  selectedAccount.value = null
  accountPickerError.value = ''
  accountPickerOpen.value = true
  void loadEligibleAccounts()
}

function cancelAccountPicker() {
  finishAccountPicker(null)
}

function confirmAccountPicker() {
  if (selectedAccount.value && isSupportedAccountName(selectedAccount.value.name)) finishAccountPicker(selectedAccount.value)
}

async function handlePanelRequest(event: MessageEvent) {
  // The opaque iframe receives no admin token or same-origin access.
  if (event.source !== panel.value?.contentWindow || event.origin !== 'null') return
  const message = event.data
  if (!message) return
  if (message.type === 'ctsm-ready') {
    markPanelReady()
    syncPanelTheme()
    return
  }
  if (message.type === 'ctsm-resize') {
    if (typeof message.height === 'number' && Number.isFinite(message.height)) panelHeight.value = Math.min(16000, Math.max(480, Math.ceil(message.height)))
    return
  }
  if (!isValidMessageID(message.id)) return
  if (message.type === 'ctsm-account-picker') {
    openAccountPicker(message.id)
    return
  }
  if (message.type !== 'ctsm-request' || typeof message.path !== 'string' || typeof message.method !== 'string' || active.has(message.id) || active.size >= 16) return
  active.add(message.id)
  try {
    const data = await requestCodexPanel(message.method, message.path, message.body)
    postToPanel({ type: 'ctsm-result', id: message.id, ok: true, data })
  } catch {
    postToPanel({ type: 'ctsm-result', id: message.id, ok: false, error: t('admin.codexTurnState.unavailable') })
  } finally { active.delete(message.id) }
}
onMounted(() => {
  window.addEventListener('message', handlePanelRequest)
  armStartupTimeout()
  themeObserver = new MutationObserver(syncPanelTheme)
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
})
onUnmounted(() => {
  accountSearchAbort?.abort()
  clearTimeout(startupTimer)
  themeObserver?.disconnect()
  window.removeEventListener('message', handlePanelRequest)
})
</script>
