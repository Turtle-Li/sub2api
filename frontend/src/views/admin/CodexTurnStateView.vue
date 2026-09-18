<template>
  <AppLayout>
    <div class="space-y-4">
      <header class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('admin.codexTurnState.title') }}</h1>
          <p class="mt-1 text-sm text-gray-600 dark:text-gray-300">{{ t('admin.codexTurnState.description') }}</p>
        </div>
        <RouterLink to="/admin/accounts" class="btn btn-secondary">{{ t('admin.codexTurnState.accounts') }}</RouterLink>
      </header>
      <iframe ref="panel" :srcdoc="panelHTML" sandbox="allow-scripts" :title="t('admin.codexTurnState.title')" class="w-full rounded-lg border border-gray-200 dark:border-dark-700" style="height: calc(100vh - 180px); min-height: 650px" />

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
  if (!message || !isValidMessageID(message.id)) return
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
onMounted(() => window.addEventListener('message', handlePanelRequest))
onUnmounted(() => {
  accountSearchAbort?.abort()
  window.removeEventListener('message', handlePanelRequest)
})
</script>
