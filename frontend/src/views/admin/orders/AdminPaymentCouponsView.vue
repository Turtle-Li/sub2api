<template>
  <AppLayout>
    <div class="space-y-4">
      <nav :aria-label="t('nav.orderManagement')" class="flex items-center gap-2 text-sm text-gray-500 dark:text-gray-400">
        <RouterLink to="/admin/orders" class="hover:text-primary-600 dark:hover:text-primary-400">{{ t('nav.orderManagement') }}</RouterLink>
        <span aria-hidden="true">/</span>
        <span class="font-medium text-gray-900 dark:text-white">{{ t('nav.paymentCoupons') }}</span>
      </nav>
      <PaymentDiscountCouponsPanel />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import PaymentDiscountCouponsPanel from '@/views/admin/PaymentDiscountCouponsPanel.vue'

const { t } = useI18n()
</script>
