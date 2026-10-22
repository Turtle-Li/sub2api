<template>
  <select
    v-if="canEnable || paid"
    :value="draft ? 'paid' : 'ordinary'"
    :disabled="saving"
    class="h-8 max-w-full cursor-pointer rounded-md border py-1 pl-2 pr-7 text-xs font-medium focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 disabled:cursor-wait"
    :class="draft ? 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-200' : 'border-gray-200 bg-gray-50 text-gray-600 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-300'"
    :aria-label="t('admin.paidFallback.editTier', { name: account.name })"
    :aria-busy="saving"
    :title="t('admin.paidFallback.accountHint')"
    data-testid="account-fallback-select"
    @change="save"
  >
    <option value="ordinary">{{ t('admin.paidFallback.ordinary') }}</option>
    <option value="paid" :disabled="!canEnable">{{ t('admin.paidFallback.paid') }}</option>
  </select>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { setPaidFallback } from '@/api/admin/accounts'
import { platformUsesGateway } from '@/constants/platformCatalog'
import { isPaidFallback } from '@/utils/paidFallback'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Account } from '@/types'

const props = defineProps<{ account: Account }>()
const emit = defineEmits<{ updated: [account: Account]; error: [message: string] }>()
const { t } = useI18n()
const paid = computed(() => isPaidFallback(props.account.extra))
const canEnable = computed(() => platformUsesGateway(props.account.platform, 'openai'))
const draft = ref(paid.value)
const saving = ref(false)
watch(paid, value => { if (!saving.value) draft.value = value })

async function save(event: Event) {
  if (saving.value) return
  const target = (event.target as HTMLSelectElement).value === 'paid'
  if (target === paid.value || (target && !canEnable.value)) return
  draft.value = target
  saving.value = true
  try {
    const result = await setPaidFallback(props.account.id, target)
    draft.value = result.enabled
    emit('updated', { ...props.account, extra: { ...props.account.extra, paid_fallback: result.enabled } })
  } catch (error) {
    draft.value = paid.value
    emit('error', extractApiErrorMessage(error, t('admin.paidFallback.saveFailed')))
  } finally { saving.value = false }
}
</script>
