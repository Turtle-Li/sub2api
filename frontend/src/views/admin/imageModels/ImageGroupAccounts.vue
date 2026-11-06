<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import * as accountsAPI from '@/api/admin/accounts'
import * as proxiesAPI from '@/api/admin/proxies'
import * as poolsAPI from '@/api/admin/accountPools'
import { setWorkBuddyImagePolicy, type AdminImageModel } from '@/api/admin/imageModels'
import type { Account, AccountListItem, AdminGroup, Proxy } from '@/types'
import type { AccountPool } from '@/api/admin/accountPools'
import EditAccountModal from '@/components/account/EditAccountModal.vue'
import Toggle from '@/components/common/Toggle.vue'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ groupId: number; groups: AdminGroup[]; candidates: AdminImageModel[] }>()
const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const accounts = ref<AccountListItem[]>([])
const page = ref(1)
const total = ref(0)
const loading = ref(false)
const error = ref('')
const busy = ref<number | null>(null)
const editing = ref<Account | null>(null)
const proxies = ref<Proxy[]>([])
const pools = ref<AccountPool[]>([])
let version = 0
let alive = true

async function load(next = page.value) {
  const current = ++version
  loading.value = true
  error.value = ''
  try {
    const result = await accountsAPI.list(next, 10, { group: String(props.groupId) })
    if (!alive || current !== version) return
    accounts.value = result.items
    total.value = result.total
    page.value = next
  } catch (cause) {
    if (alive && current === version) error.value = extractApiErrorMessage(cause)
  } finally {
    if (alive && current === version) loading.value = false
  }
}

function isWorkBuddy(account: AccountListItem) {
  const mapping = account.credentials?.model_mapping
  if (typeof mapping !== 'object' || mapping === null || Array.isArray(mapping)) return false
  const entries = Object.entries(mapping)
  return account.platform === 'openai' && account.type === 'apikey' && props.candidates.some(model =>
    model.provider === 'workbuddy_images' && entries.some(([key, value]) => key === model.id && value === model.id))
}

async function updatePolicy(account: AccountListItem, accepted: boolean) {
  busy.value = account.id
  error.value = ''
  try {
    const saved = await setWorkBuddyImagePolicy(account.id, accepted)
    if (!alive) return
    account.extra = { ...account.extra, workbuddy_image_native_retry_accepted: saved }
    emit('changed')
  } catch (cause) {
    if (alive) error.value = extractApiErrorMessage(cause)
  } finally {
    if (alive) busy.value = null
  }
}

async function edit(account: AccountListItem) {
  busy.value = account.id
  error.value = ''
  try {
    const [detail, proxyOptions, poolOptions] = await Promise.all([
      accountsAPI.getById(account.id), proxiesAPI.getAll(), poolsAPI.list(account.platform),
    ])
    if (!alive) return
    proxies.value = proxyOptions
    pools.value = poolOptions
    editing.value = detail
  } catch (cause) {
    if (alive) error.value = extractApiErrorMessage(cause)
  } finally {
    if (alive) busy.value = null
  }
}

async function updated() {
  editing.value = null
  await load()
  if (alive) emit('changed')
}
onMounted(() => void load())
onBeforeUnmount(() => { alive = false; version++ })
</script>

<template>
  <section class="space-y-4 border-t border-gray-200 pt-6 dark:border-dark-600" aria-labelledby="image-accounts-title">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 id="image-accounts-title" class="text-base font-semibold">{{ t('admin.imageModels.accounts') }}</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageModels.accountsHint') }}</p>
      </div>
      <RouterLink class="btn btn-secondary" to="/admin/accounts">{{ t('admin.imageModels.manageAccounts') }}</RouterLink>
    </div>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}
      <button class="ml-2 underline" type="button" @click="load()">{{ t('common.retry') }}</button>
    </p>
    <p v-if="loading" role="status" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
    <ul v-else-if="accounts.length" class="divide-y divide-gray-200 dark:divide-dark-600">
      <li v-for="account in accounts" :key="account.id" class="space-y-3 py-4">
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <p class="break-words text-sm font-medium">{{ account.name }}</p>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">#{{ account.id }} · {{ account.platform }} · {{ t(`admin.imageModels.accountStatus.${account.status}`) }}</p>
          </div>
          <button type="button" class="btn btn-secondary shrink-0" :disabled="busy !== null" @click="edit(account)">{{ t('admin.imageModels.editAccount') }}</button>
        </div>
        <div v-if="isWorkBuddy(account)" class="space-y-2 rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
          <label class="flex items-center justify-between gap-3 text-sm">
            {{ t('admin.imageModels.nativeRetry') }}
            <Toggle :model-value="account.extra?.workbuddy_image_native_retry_accepted === true" :disabled="busy !== null"
              :aria-label="`${account.name}: ${t('admin.imageModels.nativeRetry')}`" @update:model-value="updatePolicy(account, $event)" />
          </label>
          <p class="text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t('admin.imageModels.nativeRetryHint') }}</p>
          <p v-if="account.extra?.workbuddy_image_single_attempt_verified === true" class="text-xs text-amber-700 dark:text-amber-300">{{ t('admin.imageModels.verifiedMode') }}</p>
        </div>
      </li>
    </ul>
    <p v-else-if="!error" class="py-4 text-sm text-gray-500">{{ t('admin.imageModels.noAccounts') }}</p>
    <div v-if="total > 10" class="flex items-center justify-between gap-3">
      <span class="text-sm text-gray-500">{{ t('admin.imageModels.accountPage', { page, total }) }}</span>
      <div class="flex gap-2">
        <button type="button" class="btn btn-secondary" :disabled="loading || page === 1 || busy !== null" @click="load(page - 1)">{{ t('admin.imageModels.previousPage') }}</button>
        <button type="button" class="btn btn-secondary" :disabled="loading || page * 10 >= total || busy !== null" @click="load(page + 1)">{{ t('admin.imageModels.nextPage') }}</button>
      </div>
    </div>
    <EditAccountModal v-if="editing" :show="true" :account="editing" :groups="groups" :proxies="proxies" :pools="pools"
      @close="editing = null" @updated="updated" @pool-changed="updated" />
  </section>
</template>
