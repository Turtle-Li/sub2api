<template>
  <span
    v-if="isPaidFallback(account.extra)"
    class="inline-flex h-7 items-center px-6 text-sm text-gray-500 dark:text-gray-400"
    :title="t('admin.paidFallback.accountHint')"
    data-testid="account-fallback-label"
  >
    {{ t('admin.paidFallback.priorityLabel') }}
  </span>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { isPaidFallback } from '@/utils/paidFallback'
import type { Account } from '@/types'

defineProps<{ account: Account }>()
const { t } = useI18n()
</script>
