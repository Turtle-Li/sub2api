<template>
  <AppLayout>
    <div class="space-y-4">
      <div class="inline-flex rounded-lg bg-gray-100 p-1 dark:bg-dark-800" role="tablist" :aria-label="t('payment.admin.productConfig')">
        <button
          v-for="tab in catalogTabs"
          :key="tab.value"
          type="button"
          role="tab"
          :aria-selected="activeCatalog === tab.value"
          :class="[
            'rounded-md px-4 py-2 text-sm font-medium transition-colors',
            activeCatalog === tab.value
              ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
              : 'text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white',
          ]"
          @click="activeCatalog = tab.value"
        >
          {{ tab.label }}
        </button>
      </div>

      <template v-if="activeCatalog === 'subscription'">
      <!-- Actions -->
      <div class="flex items-center justify-end gap-2">
        <button @click="loadPlans" :disabled="plansLoading" class="btn btn-secondary" :title="t('common.refresh')">
          <Icon name="refresh" size="md" :class="plansLoading ? 'animate-spin' : ''" />
        </button>
        <button @click="openPlanEdit(null)" class="btn btn-primary">{{ t('payment.admin.createPlan') }}</button>
      </div>

      <section class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800" :aria-label="t('payment.admin.monthlyResetCardsTitle')">
        <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('payment.admin.monthlyResetCardsTitle') }}</h2>
            <p class="mt-1 max-w-3xl text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('payment.admin.monthlyResetCardsHint') }}</p>
          </div>
          <div class="flex items-center gap-3">
            <span class="text-sm font-medium text-gray-700 dark:text-gray-200">{{ monthlyResetCardsEnabled ? t('payment.admin.monthlyResetCardsEnabled') : t('payment.admin.monthlyResetCardsDisabled') }}</span>
            <button
              type="button"
              role="switch"
              data-testid="monthly-reset-cards-toggle"
              :aria-label="t('payment.admin.monthlyResetCardsTitle')"
              :aria-checked="monthlyResetCardsEnabled"
              :disabled="!paymentConfig || monthlyResetCardsSaving"
              :class="[
                'relative inline-flex h-6 w-11 shrink-0 rounded-full border-2 border-transparent transition-colors focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-60',
                monthlyResetCardsEnabled ? 'bg-primary-500' : 'bg-gray-300 dark:bg-dark-600',
              ]"
              @click="toggleMonthlyResetCards"
            >
              <span :class="[
                'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow transition duration-200 ease-in-out',
                monthlyResetCardsEnabled ? 'translate-x-5' : 'translate-x-0',
              ]" />
            </button>
          </div>
        </div>
        <p v-if="monthlyResetCardsError" role="alert" class="mt-3 text-sm text-red-600 dark:text-red-400">{{ monthlyResetCardsError }}</p>
      </section>

      <ResetCardTierPolicyPanel :plans="plans" :groups="groups" @saved="loadPlans" />

      <!-- Plans Table -->
      <DataTable :columns="planColumns" :data="plans" :loading="plansLoading">
        <template #cell-name="{ value, row }">
          <span class="text-sm font-medium" :class="getPlanNameClass(row.group_id)">{{ value }}</span>
        </template>
        <template #cell-group_id="{ value }">
          <span v-if="isGroupMissing(value)" class="text-sm">
            <span class="text-gray-400">#{{ value }}</span>
            <span class="ml-1 badge badge-danger">{{ t('payment.admin.groupMissing') }}</span>
          </span>
          <GroupBadge
            v-else-if="getGroup(value)"
            :name="getGroup(value)!.name"
            :platform="getGroup(value)!.platform"
            :rate-multiplier="getGroup(value)!.rate_multiplier"
          />
          <span v-else class="text-sm text-gray-400">-</span>
        </template>
        <template #cell-price="{ value, row }">
          <div class="text-sm">
            <span class="font-medium text-gray-900 dark:text-white">{{ planCurrencySymbol(row.currency) }}{{ (value ?? 0).toFixed(2) }}</span>
            <span v-if="row.currency" class="ml-1 text-xs text-gray-400">{{ row.currency }}</span>
            <span v-if="row.original_price" class="ml-1 text-xs text-gray-400 line-through">{{ planCurrencySymbol(row.currency) }}{{ row.original_price.toFixed(2) }}</span>
          </div>
        </template>
        <template #cell-validity_days="{ row }">
          <span class="text-sm">{{ planValidityLabel(row, t) }}</span>
        </template>
        <template #cell-reset_card_tier="{ row }">
          <span v-if="row.reset_card_tier" class="text-sm text-gray-700 dark:text-gray-200">
            {{ t('payment.admin.resetCardTierConfigured', { family: row.reset_card_tier.family_key, rank: row.reset_card_tier.tier_rank }) }}
          </span>
          <span v-else class="text-sm text-amber-700 dark:text-amber-300">{{ t('payment.admin.resetCardTierUnconfigured') }}</span>
        </template>
        <template #cell-reset_card_delivery="{ row }">
          <span v-if="monthlyResetCardDeliveryLabel(row)" class="text-sm text-gray-700 dark:text-gray-200">
            {{ monthlyResetCardDeliveryLabel(row) }}
          </span>
          <span v-else class="text-sm text-gray-400">-</span>
        </template>
        <template #cell-for_sale="{ value, row }">
          <button
            type="button"
            :class="[
              'relative inline-flex h-5 w-9 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
              value ? 'bg-primary-500' : 'bg-gray-300 dark:bg-dark-600'
            ]"
            @click="toggleForSale(row)"
          >
            <span :class="[
              'pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
              value ? 'translate-x-4' : 'translate-x-0'
            ]" />
          </button>
        </template>
        <template #cell-actions="{ row }">
          <div class="flex items-center gap-2">
            <button @click="openPlanEdit(row)" class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-blue-50 hover:text-blue-600 dark:hover:bg-blue-900/20 dark:hover:text-blue-400">
              <Icon name="edit" size="sm" />
              <span class="text-xs">{{ t('common.edit') }}</span>
            </button>
            <button @click="confirmDeletePlan(row)" class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400">
              <Icon name="trash" size="sm" />
              <span class="text-xs">{{ t('common.delete') }}</span>
            </button>
          </div>
        </template>
      </DataTable>
      </template>

      <AdminRechargeCatalogPanel v-else />
    </div>

    <!-- Plan Edit Dialog -->
    <PlanEditDialog :show="showPlanDialog" :plan="editingPlan" :groups="groups" :payment-config="paymentConfig" @close="showPlanDialog = false" @saved="loadPlans" />

    <ConfirmDialog :show="showDeletePlanDialog" :title="t('payment.admin.deletePlan')" :message="t('payment.admin.deletePlanConfirm')" :confirm-text="t('common.delete')" danger @confirm="handleDeletePlan" @cancel="showDeletePlanDialog = false" />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import type { AdminPaymentConfig } from '@/api/admin/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'
import adminAPI from '@/api/admin'
import type { SubscriptionPlan } from '@/types/payment'
import type { AdminGroup } from '@/types'
import type { Column } from '@/components/common/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import PlanEditDialog from './PlanEditDialog.vue'
import { currencySymbol } from '@/components/payment/currency'
import { platformTextClass } from '@/utils/platformColors'
import { monthlyResetCardDeliveryLabel as formatMonthlyResetCardDelivery, planValidityLabel } from '@/components/payment/validity'
import AdminRechargeCatalogPanel from './AdminRechargeCatalogPanel.vue'
import ResetCardTierPolicyPanel from './ResetCardTierPolicyPanel.vue'

const { t } = useI18n()
const appStore = useAppStore()
const activeCatalog = ref<'subscription' | 'recharge'>('subscription')
const catalogTabs = computed(() => [
  { value: 'subscription' as const, label: t('payment.admin.subscriptionProducts') },
  { value: 'recharge' as const, label: t('payment.admin.balanceProducts') },
])

function planCurrencySymbol(currency?: string): string {
  return currencySymbol(currency || 'USD')
}

// ==================== Groups ====================

const groups = ref<AdminGroup[]>([])
const paymentConfig = ref<AdminPaymentConfig | null>(null)
const monthlyResetCardsSaving = ref(false)
const monthlyResetCardsError = ref('')
const monthlyResetCardsEnabled = computed(() => paymentConfig.value?.monthly_reset_cards_enabled === true)

async function loadGroups() {
  try {
    groups.value = await adminAPI.groups.getAll()
  } catch { /* ignore */ }
}

async function loadPaymentConfig() {
  try {
    const res = await adminPaymentAPI.getConfig()
    paymentConfig.value = res.data
    monthlyResetCardsError.value = ''
  } catch (err: unknown) {
    paymentConfig.value = null
    monthlyResetCardsError.value = extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))
  }
}

async function toggleMonthlyResetCards() {
  if (!paymentConfig.value || monthlyResetCardsSaving.value) return
  const enabled = !monthlyResetCardsEnabled.value
  monthlyResetCardsSaving.value = true
  monthlyResetCardsError.value = ''
  try {
    await adminPaymentAPI.updateConfig({ monthly_reset_cards_enabled: enabled })
    paymentConfig.value = { ...paymentConfig.value, monthly_reset_cards_enabled: enabled }
    appStore.showSuccess(enabled ? t('payment.admin.monthlyResetCardsEnabledSaved') : t('payment.admin.monthlyResetCardsDisabledSaved'))
  } catch (err: unknown) {
    monthlyResetCardsError.value = extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))
    appStore.showError(monthlyResetCardsError.value)
  } finally {
    monthlyResetCardsSaving.value = false
  }
}

function monthlyResetCardDeliveryLabel(plan: SubscriptionPlan): string {
  return formatMonthlyResetCardDelivery(plan.entitlements || {}, t)
}

function getGroup(id: number): AdminGroup | undefined {
  return groups.value.find(g => g.id === id)
}

function isGroupMissing(id: number): boolean {
  return id > 0 && !groups.value.find(g => g.id === id)
}

function getPlanNameClass(groupId: number): string {
  const group = getGroup(groupId)
  return group ? platformTextClass(group.platform) : 'text-gray-900 dark:text-white'
}


// ==================== Plans ====================

const plansLoading = ref(false)
const plans = ref<SubscriptionPlan[]>([])
const showPlanDialog = ref(false)
const showDeletePlanDialog = ref(false)
const editingPlan = ref<SubscriptionPlan | null>(null)
const deletingPlanId = ref<number | null>(null)

const planColumns = computed((): Column[] => [
  { key: 'id', label: 'ID' },
  { key: 'name', label: t('payment.admin.planName') },
  { key: 'group_id', label: t('payment.admin.group') },
  { key: 'price', label: t('payment.admin.price') },
  { key: 'validity_days', label: t('payment.admin.validity') },
  { key: 'reset_card_tier', label: t('payment.admin.resetCardTier') },
  { key: 'reset_card_delivery', label: t('payment.admin.resetCardDelivery') },
  { key: 'for_sale', label: t('payment.admin.forSale') },
  { key: 'sort_order', label: t('payment.admin.sortOrder') },
  { key: 'actions', label: t('common.actions') },
])

async function loadPlans() {
  plansLoading.value = true
  try {
    const res = await adminPaymentAPI.getPlans()
    // Backend returns features as newline-separated string; parse to array
    plans.value = (res.data || []).map((p: Omit<SubscriptionPlan, 'features'> & { features: string | string[] }) => ({
      ...p,
      features: typeof p.features === 'string'
        ? p.features.split('\n').map((f: string) => f.trim()).filter(Boolean)
        : (p.features || []),
    }))
  }
  catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
  finally { plansLoading.value = false }
}

function openPlanEdit(plan: SubscriptionPlan | null) {
  editingPlan.value = plan
  showPlanDialog.value = true
}


/** Quick toggle for_sale from the list */
async function toggleForSale(plan: SubscriptionPlan) {
  try {
    await adminPaymentAPI.updatePlan(plan.id, { for_sale: !plan.for_sale })
    plan.for_sale = !plan.for_sale
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  }
}

function confirmDeletePlan(plan: SubscriptionPlan) { deletingPlanId.value = plan.id; showDeletePlanDialog.value = true }
async function handleDeletePlan() {
  if (!deletingPlanId.value) return
  try { await adminPaymentAPI.deletePlan(deletingPlanId.value); appStore.showSuccess(t('common.deleted')); showDeletePlanDialog.value = false; loadPlans() }
  catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
}

// ==================== Lifecycle ====================

onMounted(() => {
  loadGroups()
  loadPaymentConfig()
  loadPlans()
})
</script>
