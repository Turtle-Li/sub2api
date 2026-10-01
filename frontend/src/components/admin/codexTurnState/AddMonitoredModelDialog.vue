<template>
  <BaseDialog :show="show" :title="t('admin.codexTurnState.addModel.title')" width="normal" @close="emit('close')">
    <form id="codex-turn-state-add-model" class="space-y-4" data-test="cts-add-form" @submit.prevent="submit">
      <div>
        <label class="input-label">{{ t('admin.codexTurnState.addModel.account') }}</label>
        <Select
          v-model="accountValue"
          :options="accountOptions"
          :placeholder="t('admin.codexTurnState.addModel.accountPlaceholder')"
          remote
          :loading="accountsLoading"
          data-test="cts-add-account"
          @search="searchAccounts"
        />
      </div>

      <div>
        <label class="input-label">{{ t('admin.codexTurnState.addModel.model') }}</label>
        <select v-model="modelChoice" class="input font-mono" :disabled="!accountId || modelsLoading" data-test="cts-add-model">
          <option value="" disabled>{{ modelsLoading ? t('admin.codexTurnState.addModel.modelsLoading') : t('admin.codexTurnState.addModel.modelPlaceholder') }}</option>
          <option v-for="item in modelOptions" :key="item" :value="item">{{ item }}</option>
          <option :value="CUSTOM">{{ t('admin.codexTurnState.addModel.customModel') }}</option>
        </select>
        <input
          v-if="modelChoice === CUSTOM"
          v-model="customModel"
          type="text"
          class="input mt-2 font-mono"
          maxlength="128"
          :placeholder="t('admin.codexTurnState.addModel.customPlaceholder')"
          data-test="cts-add-custom-model"
        />
      </div>

      <div>
        <label class="input-label">{{ t('admin.codexTurnState.addModel.targetLen') }}</label>
        <input v-model.number="targetLen" type="number" min="1" max="8192" step="1" class="input" data-test="cts-add-target-len" />
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.codexTurnState.addModel.targetLenHint') }}</p>
      </div>
    </form>

    <template #footer>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.cancel') }}</button>
        <button type="submit" form="codex-turn-state-add-model" class="btn btn-primary" :disabled="!canSubmit" data-test="cts-add-submit">
          {{ t('admin.codexTurnState.addModel.submit') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import { list as listAccounts } from '@/api/admin/accounts'
import { addMonitoredModel, listAccountModels } from '@/api/admin/codexTurnState'
import { useAppStore } from '@/stores'
import { describeManagerError } from './managerError'

interface AccountChoice { id: number; name: string }

const props = defineProps<{
  show: boolean
  /** 从账号卡片打开时预选的账号 */
  presetAccount?: AccountChoice | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'saved'): void
}>()

const CUSTOM = '__custom__'
const ACCOUNT_NAME_RE = /^[^\p{Cc}]{1,128}$/u
const eligibleAccountTypes = ['oauth', 'setup-token'] as const

const { t } = useI18n()
const appStore = useAppStore()

const accountValue = ref<string | null>(null)
const searchResults = ref<AccountChoice[]>([])
const accountsLoading = ref(false)
const modelOptions = ref<string[]>([])
const modelsLoading = ref(false)
const modelChoice = ref('')
const customModel = ref('')
const targetLen = ref(292)
const saving = ref(false)
let accountAbort: AbortController | null = null
let modelsSequence = 0

const knownAccounts = computed(() => {
  const unique = new Map<number, AccountChoice>()
  if (props.presetAccount) unique.set(props.presetAccount.id, props.presetAccount)
  for (const account of searchResults.value) unique.set(account.id, account)
  return unique
})
const accountOptions = computed(() => [...knownAccounts.value.values()]
  .map((account) => ({ value: String(account.id), label: `${account.name} (#${account.id})` })))
const accountId = computed(() => {
  const id = Number(accountValue.value)
  return Number.isInteger(id) && id > 0 ? id : 0
})
const selectedModel = computed(() => (modelChoice.value === CUSTOM ? customModel.value.trim() : modelChoice.value))
const canSubmit = computed(() => !saving.value && accountId.value > 0 && !!selectedModel.value
  && Number.isInteger(targetLen.value) && targetLen.value >= 1 && targetLen.value <= 8192)

watch(() => props.show, (show) => {
  if (!show) {
    accountAbort?.abort()
    return
  }
  accountValue.value = props.presetAccount ? String(props.presetAccount.id) : null
  modelChoice.value = ''
  customModel.value = ''
  targetLen.value = 292
  void searchAccounts()
}, { immediate: true })

watch(accountId, async (id) => {
  const sequence = ++modelsSequence
  modelOptions.value = []
  modelChoice.value = ''
  if (!id) return
  modelsLoading.value = true
  try {
    const models = await listAccountModels(id)
    if (sequence === modelsSequence) modelOptions.value = models
  } catch {
    // 模型列表不可用时仍可手动填写。
    if (sequence === modelsSequence) modelChoice.value = CUSTOM
  } finally {
    if (sequence === modelsSequence) modelsLoading.value = false
  }
})

async function searchAccounts(search = '') {
  accountAbort?.abort()
  const controller = new AbortController()
  accountAbort = controller
  accountsLoading.value = true
  try {
    const results = await Promise.all(eligibleAccountTypes.map((type) => listAccounts(
      1,
      50,
      { platform: 'openai', type, status: 'active', lite: 'true', ...(search ? { search } : {}) },
      { signal: controller.signal },
    )))
    if (controller.signal.aborted) return
    const unique = new Map<number, AccountChoice>()
    for (const result of results) {
      for (const account of result.items) unique.set(account.id, { id: account.id, name: account.name })
    }
    searchResults.value = [...unique.values()]
  } catch {
    if (!controller.signal.aborted) searchResults.value = []
  } finally {
    if (accountAbort === controller) accountsLoading.value = false
  }
}

async function submit() {
  if (!canSubmit.value) return
  const account = knownAccounts.value.get(accountId.value)
  const name = account?.name.trim() || `#${accountId.value}`
  if (!ACCOUNT_NAME_RE.test(name)) {
    appStore.showError(t('admin.codexTurnState.addModel.invalidName'))
    return
  }
  saving.value = true
  try {
    await addMonitoredModel({ account_id: accountId.value, name, model: selectedModel.value, target_state_len: targetLen.value })
    appStore.showSuccess(t('admin.codexTurnState.addModel.saved'))
    emit('saved')
  } catch (err) {
    appStore.showError(describeManagerError(err, t, 'admin.codexTurnState.saveFailed'))
  } finally {
    saving.value = false
  }
}

onUnmounted(() => {
  accountAbort?.abort()
})
</script>
