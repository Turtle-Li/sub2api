<template>
  <TablePageLayout>
    <template #filters>
      <div class="flex flex-wrap items-center gap-3">
        <div class="relative min-w-0 flex-1 sm:max-w-72">
          <Icon name="search" size="md" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
          <input
            v-model="search"
            type="search"
            class="input pl-10"
            :placeholder="t('admin.paymentCoupons.search')"
            @input="handleSearch"
          />
        </div>
        <div class="ml-auto flex items-center gap-2">
          <button type="button" class="btn btn-secondary" :disabled="loading" :title="t('common.refresh')" @click="loadCoupons">
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          </button>
          <button type="button" class="btn btn-primary" data-test="create-payment-coupon" @click="openCreate">
            <Icon name="plus" size="md" class="mr-1" />
            {{ t('admin.paymentCoupons.create') }}
          </button>
        </div>
      </div>
    </template>

    <template #table>
      <DataTable :columns="columns" :data="coupons" :loading="loading" row-key="id">
        <template #cell-code="{ value }">
          <code class="font-mono text-sm font-medium text-gray-900 dark:text-white">{{ value }}</code>
        </template>
        <template #cell-discount="{ row }">
          <span class="font-medium text-gray-900 dark:text-white">{{ formatDiscount(row) }}</span>
        </template>
        <template #cell-scope="{ row }">
          <span class="text-sm text-gray-600 dark:text-gray-300">{{ formatCouponScope(row) }}</span>
        </template>
        <template #cell-usage="{ row }">
          <span class="text-sm text-gray-600 dark:text-gray-300">{{ usageLabel(row) }}</span>
        </template>
        <template #cell-status="{ row }">
          <span :class="['badge', couponStatusClass(row)]">{{ couponStatusLabel(row) }}</span>
        </template>
        <template #cell-expires_at="{ value }">
          <span class="text-sm text-gray-500 dark:text-dark-400">{{ formatDateTime(value) }}</span>
        </template>
        <template #cell-actions="{ row }">
          <div class="flex items-center gap-1">
            <button
              type="button"
              data-test="payment-coupon-history"
              class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-blue-50 hover:text-blue-700 dark:hover:bg-blue-900/20 dark:hover:text-blue-300"
              :title="t('admin.paymentCoupons.history')"
              @click="openHistory(row)"
            >
              <Icon name="eye" size="sm" />
              <span class="text-xs">{{ t('admin.paymentCoupons.history') }}</span>
            </button>
            <button
              type="button"
              data-test="edit-payment-coupon"
              class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-700 dark:hover:text-gray-200"
              :title="t('common.edit')"
              @click="openEdit(row)"
            >
              <Icon name="edit" size="sm" />
              <span class="text-xs">{{ t('common.edit') }}</span>
            </button>
          </div>
        </template>
      </DataTable>
    </template>

    <template #pagination>
      <Pagination
        v-if="pagination.total > 0"
        :page="pagination.page"
        :total="pagination.total"
        :page-size="pagination.page_size"
        @update:page="changePage"
        @update:pageSize="changePageSize"
      />
    </template>
  </TablePageLayout>

  <BaseDialog
    :show="showEditor"
    :title="editingCoupon ? t('admin.paymentCoupons.edit') : t('admin.paymentCoupons.create')"
    width="wide"
    :show-close-button="!saving"
    :close-on-escape="!saving"
    @close="closeEditor"
  >
    <form id="payment-discount-coupon-form" data-test="payment-discount-coupon-form" class="space-y-5" @submit.prevent="saveCoupon">
      <div class="grid gap-4 sm:grid-cols-2">
        <div>
          <label class="input-label" for="payment-discount-code">{{ t('admin.paymentCoupons.code') }}</label>
          <input
            id="payment-discount-code"
            v-model="form.code"
            class="input font-mono uppercase"
            maxlength="32"
            autocomplete="off"
            autocapitalize="characters"
            spellcheck="false"
            pattern="[A-Za-z0-9_-]{8,32}"
            :aria-invalid="form.code.trim() ? !isCustomCodeValid(form.code) : undefined"
            :readonly="!!editingCoupon"
            :placeholder="editingCoupon ? '' : t('admin.paymentCoupons.codePlaceholder')"
            @input="normalizeCodeInput"
          />
          <p class="input-hint">{{ editingCoupon ? t('admin.paymentCoupons.codeImmutable') : t('admin.paymentCoupons.codeHint') }}</p>
          <p v-if="form.code.trim() && !isCustomCodeValid(form.code)" class="input-error">{{ t('admin.paymentCoupons.invalidCode') }}</p>
        </div>
        <div>
          <label class="input-label" for="payment-discount-type">{{ t('admin.paymentCoupons.discountType') }}</label>
          <Select id="payment-discount-type" v-model="form.discountType" :options="discountTypeOptions" :aria-label="t('admin.paymentCoupons.discountType')" />
        </div>
        <div>
          <label class="input-label" for="payment-discount-value">{{ t('admin.paymentCoupons.discountValue') }}</label>
          <input
            id="payment-discount-value"
            v-model.trim="form.discountValue"
            class="input"
            inputmode="decimal"
            pattern="[0-9]+([.][0-9]{1,2})?"
            :aria-invalid="form.discountValue ? !isDiscountValueValid(form.discountValue) : undefined"
            required
          />
          <p class="input-hint">{{ t('admin.paymentCoupons.discountValueHint') }}</p>
        </div>
        <div>
          <label class="input-label" for="payment-discount-currency">{{ t('admin.paymentCoupons.currency') }}</label>
          <Select id="payment-discount-currency" v-model="form.currency" :options="currencyOptions" :aria-label="t('admin.paymentCoupons.currency')" />
        </div>
        <div>
          <label class="input-label" for="payment-discount-max-uses">{{ t('admin.paymentCoupons.maxUses') }}</label>
          <input id="payment-discount-max-uses" v-model.number="form.maxUses" class="input" type="number" min="0" step="1" required />
          <p class="input-hint">{{ t('admin.paymentCoupons.zeroUnlimited') }}</p>
        </div>
        <div>
          <label class="input-label" for="payment-discount-per-user">{{ t('admin.paymentCoupons.perUserMaxUses') }}</label>
          <input id="payment-discount-per-user" v-model="form.perUserMaxUses" class="input" type="number" min="0" step="1" :placeholder="t('admin.paymentCoupons.defaultOne')" />
          <p class="input-hint">{{ t('admin.paymentCoupons.perUserHint') }}</p>
        </div>
        <div>
          <label class="input-label" for="payment-discount-target-user">{{ t('admin.paymentCoupons.targetUser') }}</label>
          <input id="payment-discount-target-user" v-model="form.targetUserId" class="input" type="number" min="1" step="1" :placeholder="t('admin.paymentCoupons.allUsers')" />
        </div>
        <div class="flex items-end">
          <div class="flex items-center gap-3 pb-2">
            <Toggle v-model="form.enabled" :aria-label="t('admin.paymentCoupons.enabled')" />
            <div>
              <p class="text-sm font-medium text-gray-800 dark:text-gray-100">{{ t('admin.paymentCoupons.enabled') }}</p>
              <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.paymentCoupons.disableHint') }}</p>
            </div>
          </div>
        </div>
        <div>
          <label class="input-label" for="payment-discount-starts-at">{{ t('admin.paymentCoupons.startsAt') }}</label>
          <input id="payment-discount-starts-at" v-model="form.startsAt" type="datetime-local" class="input" required />
        </div>
        <div>
          <label class="input-label" for="payment-discount-expires-at">{{ t('admin.paymentCoupons.expiresAt') }}</label>
          <input id="payment-discount-expires-at" v-model="form.expiresAt" type="datetime-local" class="input" required />
        </div>
      </div>
      <fieldset class="space-y-3 border-t border-gray-200 pt-5 dark:border-dark-700">
        <legend class="text-sm font-medium text-gray-800 dark:text-gray-100">{{ t('admin.paymentCoupons.applicableProducts') }}</legend>
        <div class="grid gap-2 sm:grid-cols-3" role="radiogroup" :aria-label="t('admin.paymentCoupons.applicableProducts')">
          <label
            v-for="scope in productScopeOptions"
            :key="scope.value"
            :class="[
              'flex cursor-pointer items-center gap-2 rounded border px-3 py-2 text-sm transition-colors',
              productScope === scope.value
                ? 'border-primary-500 bg-primary-50 text-primary-900 dark:bg-primary-900/20 dark:text-primary-100'
                : 'border-gray-200 text-gray-700 hover:border-gray-300 dark:border-dark-700 dark:text-dark-200 dark:hover:border-dark-500',
            ]"
          >
            <input
              :id="`payment-coupon-scope-${scope.value}`"
              class="h-4 w-4 border-gray-300 text-primary-600 focus:ring-primary-500"
              type="radio"
              name="payment-coupon-product-scope"
              :checked="productScope === scope.value"
              :value="scope.value"
              :disabled="saving"
              @change="setProductScope(scope.value)"
            />
            <span>{{ scope.label }}</span>
          </label>
        </div>
        <p class="input-hint">{{ t('admin.paymentCoupons.resetCardExcluded') }}</p>
      </fieldset>
      <section v-if="includesSubscription" class="space-y-3 border-t border-gray-200 pt-5 dark:border-dark-700">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <p class="text-sm font-medium text-gray-800 dark:text-gray-100">{{ t('admin.paymentCoupons.subscriptionScope') }}</p>
          <div class="flex flex-wrap gap-3" role="radiogroup" :aria-label="t('admin.paymentCoupons.subscriptionScope')">
            <label class="flex cursor-pointer items-center gap-2 text-sm text-gray-700 dark:text-dark-200">
              <input
                id="payment-coupon-plan-mode-all"
                v-model="form.subscriptionPlanMode"
                class="h-4 w-4 border-gray-300 text-primary-600 focus:ring-primary-500"
                type="radio"
                value="all"
                :disabled="saving"
                @change="selectAllSubscriptionPlans"
              />
              {{ t('admin.paymentCoupons.allSubscriptionPlans') }}
            </label>
            <label class="flex cursor-pointer items-center gap-2 text-sm text-gray-700 dark:text-dark-200">
              <input
                id="payment-coupon-plan-mode-specific"
                v-model="form.subscriptionPlanMode"
                class="h-4 w-4 border-gray-300 text-primary-600 focus:ring-primary-500"
                type="radio"
                value="specific"
                :disabled="saving"
              />
              {{ t('admin.paymentCoupons.selectedSubscriptionPlans') }}
            </label>
          </div>
        </div>
        <p class="input-hint">{{ form.subscriptionPlanMode === 'all' ? t('admin.paymentCoupons.allSubscriptionPlansHint') : t('admin.paymentCoupons.selectedSubscriptionPlansHint') }}</p>
        <div v-if="form.subscriptionPlanMode === 'specific'" class="space-y-3">
          <p v-if="plansLoading" data-test="payment-coupon-plans-loading" class="text-sm text-gray-500 dark:text-dark-400">{{ t('common.loading') }}</p>
          <div v-else-if="plansLoadError" data-test="payment-coupon-plans-error" class="flex flex-wrap items-center gap-3" role="alert">
            <span class="text-sm text-red-600 dark:text-red-400">{{ t('admin.paymentCoupons.plansLoadFailed') }}</span>
            <button type="button" data-test="payment-coupon-plans-retry" class="btn btn-secondary btn-sm" :disabled="saving" @click="refreshPlans">
              {{ t('admin.paymentCoupons.retryPlans') }}
            </button>
          </div>
          <template v-else>
            <p v-if="plans.length === 0" class="text-sm text-gray-500 dark:text-dark-400">{{ t('admin.paymentCoupons.noSubscriptionPlans') }}</p>
            <div v-else class="grid gap-2 sm:grid-cols-2">
              <label
                v-for="plan in plans"
                :key="plan.id"
                class="flex min-w-0 cursor-pointer items-start gap-2 rounded border border-gray-200 px-3 py-2 text-sm dark:border-dark-700"
              >
                <input
                  :id="`payment-coupon-plan-${plan.id}`"
                  :data-test="`payment-coupon-plan-${plan.id}`"
                  class="mt-0.5 h-4 w-4 flex-none border-gray-300 text-primary-600 focus:ring-primary-500"
                  type="checkbox"
                  :checked="form.planIds.includes(plan.id)"
                  :disabled="saving"
                  @change="togglePlan(plan.id, $event)"
                />
                <span class="min-w-0 text-gray-700 dark:text-dark-200">
                  <span class="block truncate">{{ plan.name }}</span>
                  <span class="text-xs text-gray-500 dark:text-dark-400">#{{ plan.id }} · {{ planValidityLabel(plan, t) }}<template v-if="plan.for_sale === false"> · {{ t('admin.paymentCoupons.planUnavailable') }}</template></span>
                </span>
              </label>
            </div>
            <div v-if="missingSelectedPlanIds.length" class="space-y-2" data-test="payment-coupon-missing-plans">
              <p class="text-sm text-amber-700 dark:text-amber-300">{{ t('admin.paymentCoupons.missingPlansHint') }}</p>
              <label
                v-for="planId in missingSelectedPlanIds"
                :key="planId"
                class="flex cursor-pointer items-center gap-2 rounded border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-100"
              >
                <input
                  :id="`payment-coupon-plan-${planId}`"
                  :data-test="`payment-coupon-missing-plan-${planId}`"
                  class="h-4 w-4 border-amber-400 text-primary-600 focus:ring-primary-500"
                  type="checkbox"
                  :checked="true"
                  :disabled="saving"
                  @change="togglePlan(planId, $event)"
                />
                {{ t('admin.paymentCoupons.missingPlan', { id: planId }) }}
              </label>
            </div>
          </template>
          <p v-if="form.subscriptionPlanMode === 'specific' && form.planIds.length === 0 && !plansLoading && !plansLoadError" class="input-error">
            {{ t('admin.paymentCoupons.selectAtLeastOnePlan') }}
          </p>
        </div>
      </section>
      <div>
        <label class="input-label" for="payment-discount-notes">{{ t('admin.paymentCoupons.notes') }}</label>
        <textarea id="payment-discount-notes" v-model="form.notes" class="input" rows="3" maxlength="2000" />
      </div>
    </form>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" :disabled="saving" @click="closeEditor">{{ t('common.cancel') }}</button>
        <button type="submit" form="payment-discount-coupon-form" class="btn btn-primary" :disabled="saving || !formValid">
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>

  <BaseDialog
    :show="showHistory"
    :title="historyCoupon ? t('admin.paymentCoupons.historyTitle', { code: historyCoupon.code }) : t('admin.paymentCoupons.history')"
    width="extra-wide"
    @close="closeHistory"
  >
    <div v-if="historyCoupon" class="space-y-6">
      <section class="space-y-3">
        <div class="flex items-center justify-between gap-3">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.paymentCoupons.usageHistory') }}</h3>
          <span class="text-xs text-gray-500 dark:text-dark-400">{{ usageLabel(historyCoupon) }}</span>
        </div>
        <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-700">
          <table class="w-full min-w-[42rem] divide-y divide-gray-200 text-sm dark:divide-dark-700">
            <thead class="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-dark-800 dark:text-dark-400">
              <tr>
                <th class="px-3 py-2.5">{{ t('admin.paymentCoupons.order') }}</th>
                <th class="px-3 py-2.5">{{ t('admin.paymentCoupons.user') }}</th>
                <th class="px-3 py-2.5">{{ t('admin.paymentCoupons.status') }}</th>
                <th class="px-3 py-2.5">{{ t('admin.paymentCoupons.original') }}</th>
                <th class="px-3 py-2.5">{{ t('admin.paymentCoupons.discount') }}</th>
                <th class="px-3 py-2.5">{{ t('admin.paymentCoupons.finalAmount') }}</th>
                <th class="px-3 py-2.5">{{ t('admin.paymentCoupons.updatedAt') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-200 dark:divide-dark-700">
              <tr v-if="usagesLoading"><td colspan="7" class="px-3 py-8 text-center text-gray-500 dark:text-dark-400">{{ t('common.loading') }}</td></tr>
              <tr v-else-if="usages.length === 0"><td colspan="7" class="px-3 py-8 text-center text-gray-500 dark:text-dark-400">{{ t('admin.paymentCoupons.noUsage') }}</td></tr>
              <tr v-for="usage in usages" :key="usage.id || `${usage.order_id}-${usage.created_at}`" class="bg-white dark:bg-dark-900">
                <td class="px-3 py-2.5"><RouterLink :to="{ path: '/admin/orders', query: { order_id: String(usage.order_id) } }" class="font-mono text-primary-700 hover:underline dark:text-primary-300">#{{ usage.order_id }}</RouterLink></td>
                <td class="px-3 py-2.5">
                  <RouterLink :to="{ path: '/admin/usage', query: { user_id: String(usage.user_id) } }" class="text-primary-700 hover:underline dark:text-primary-300">
                    {{ usage.user_email || `#${usage.user_id}` }}
                  </RouterLink>
                </td>
                <td class="px-3 py-2.5"><span :class="['badge', usageStatusClass(usage.status)]">{{ usageStatusLabel(usage.status) }}</span></td>
                <td class="px-3 py-2.5">{{ formatMoney(usage.original_amount, usage.currency) }}</td>
                <td class="px-3 py-2.5 text-emerald-700 dark:text-emerald-300">-{{ formatMoney(usage.discount_amount, usage.currency) }}</td>
                <td class="px-3 py-2.5 font-medium">{{ formatMoney(usage.pay_amount, usage.currency) }}</td>
                <td class="px-3 py-2.5 text-gray-500 dark:text-dark-400">{{ formatDateTime(usage.updated_at || usage.created_at) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <Pagination v-if="usagePagination.total > usagePagination.page_size" :page="usagePagination.page" :total="usagePagination.total" :page-size="usagePagination.page_size" @update:page="changeUsagePage" @update:pageSize="changeUsagePageSize" />
      </section>

      <section class="space-y-3 border-t border-gray-200 pt-5 dark:border-dark-700">
        <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.paymentCoupons.auditHistory') }}</h3>
        <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-700">
          <table class="w-full min-w-[34rem] divide-y divide-gray-200 text-sm dark:divide-dark-700">
            <thead class="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-dark-800 dark:text-dark-400"><tr><th class="px-3 py-2.5">{{ t('admin.paymentCoupons.action') }}</th><th class="px-3 py-2.5">{{ t('admin.paymentCoupons.admin') }}</th><th class="px-3 py-2.5">{{ t('admin.paymentCoupons.detail') }}</th><th class="px-3 py-2.5">{{ t('admin.paymentCoupons.createdAt') }}</th></tr></thead>
            <tbody class="divide-y divide-gray-200 dark:divide-dark-700">
              <tr v-if="auditsLoading"><td colspan="4" class="px-3 py-8 text-center text-gray-500 dark:text-dark-400">{{ t('common.loading') }}</td></tr>
              <tr v-else-if="audits.length === 0"><td colspan="4" class="px-3 py-8 text-center text-gray-500 dark:text-dark-400">{{ t('admin.paymentCoupons.noAudit') }}</td></tr>
              <tr v-for="audit in audits" :key="audit.id || `${audit.action}-${audit.created_at}`" class="bg-white align-top dark:bg-dark-900">
                <td class="px-3 py-2.5 font-medium">{{ audit.action }}</td>
                <td class="px-3 py-2.5">
                  <RouterLink
                    v-if="audit.admin_user_id"
                    :to="{ path: '/admin/usage', query: { user_id: String(audit.admin_user_id) } }"
                    class="font-mono text-primary-700 hover:underline dark:text-primary-300"
                  >#{{ audit.admin_user_id }}</RouterLink>
                  <span v-else>-</span>
                </td>
                <td class="max-w-xl break-all px-3 py-2.5 font-mono text-xs text-gray-600 dark:text-dark-300">{{ formatAuditDetail(audit.detail) }}</td>
                <td class="whitespace-nowrap px-3 py-2.5 text-gray-500 dark:text-dark-400">{{ formatDateTime(audit.created_at) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <Pagination v-if="auditPagination.total > auditPagination.page_size" :page="auditPagination.page" :total="auditPagination.total" :page-size="auditPagination.page_size" @update:page="changeAuditPage" @update:pageSize="changeAuditPageSize" />
      </section>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminPaymentAPI } from '@/api/admin/payment'
import type {
  PaymentDiscountCoupon,
  PaymentDiscountCouponAudit,
  PaymentDiscountCouponUsage,
  PaymentDiscountOrderType,
  PaymentDiscountType,
  SavePaymentDiscountCouponRequest,
} from '@/api/admin/payment'
import { useAppStore } from '@/stores/app'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { formatCurrency, formatDateTime, formatDateTimeLocalInput, parseDateTimeLocalInput } from '@/utils/format'
import { extractI18nErrorMessage } from '@/utils/apiError'
import type { SubscriptionPlan } from '@/types/payment'
import { planValidityLabel } from '@/components/payment/validity'
import type { Column } from '@/components/common/types'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'

interface CouponForm {
  code: string
  discountType: PaymentDiscountType
  discountValue: string
  currency: 'CNY' | 'USD'
  maxUses: number
  perUserMaxUses: string
  targetUserId: string
  startsAt: string
  expiresAt: string
  enabled: boolean
  notes: string
  orderTypes: PaymentDiscountOrderType[]
  planIds: number[]
  subscriptionPlanMode: 'all' | 'specific'
}

const { t } = useI18n()
const appStore = useAppStore()
const coupons = ref<PaymentDiscountCoupon[]>([])
const loading = ref(false)
const search = ref('')
const pagination = reactive({ page: 1, page_size: getPersistedPageSize(), total: 0 })
const showEditor = ref(false)
const saving = ref(false)
const editingCoupon = ref<PaymentDiscountCoupon | null>(null)
const showHistory = ref(false)
const historyCoupon = ref<PaymentDiscountCoupon | null>(null)
const usages = ref<PaymentDiscountCouponUsage[]>([])
const audits = ref<PaymentDiscountCouponAudit[]>([])
const usagesLoading = ref(false)
const auditsLoading = ref(false)
const usagePagination = reactive({ page: 1, page_size: 20, total: 0 })
const auditPagination = reactive({ page: 1, page_size: 20, total: 0 })
const plans = ref<SubscriptionPlan[]>([])
const plansLoading = ref(false)
const plansLoadError = ref(false)

function localDateTimeInputValue(value: Date): string {
  return formatDateTimeLocalInput(Math.floor(value.getTime() / 1000))
}

function defaultForm(): CouponForm {
  const starts = new Date()
  const expires = new Date(starts)
  expires.setDate(expires.getDate() + 30)
  return {
    code: '',
    discountType: 'percent',
    discountValue: '',
    currency: 'CNY',
    maxUses: 0,
    perUserMaxUses: '',
    targetUserId: '',
    startsAt: localDateTimeInputValue(starts),
    expiresAt: localDateTimeInputValue(expires),
    enabled: true,
    notes: '',
    orderTypes: ['balance', 'subscription'],
    planIds: [],
    subscriptionPlanMode: 'all',
  }
}

const form = reactive<CouponForm>(defaultForm())
const discountTypeOptions = computed(() => [
  { value: 'percent', label: t('admin.paymentCoupons.discountTypes.percent') },
  { value: 'fixed', label: t('admin.paymentCoupons.discountTypes.fixed') },
])
const currencyOptions = computed(() => [
  { value: 'CNY', label: 'CNY' },
  { value: 'USD', label: 'USD' },
])
const productScopeOptions = computed(() => [
  { value: 'both' as const, label: t('admin.paymentCoupons.productScopes.both') },
  { value: 'balance' as const, label: t('admin.paymentCoupons.productScopes.balance') },
  { value: 'subscription' as const, label: t('admin.paymentCoupons.productScopes.subscription') },
])
const includesSubscription = computed(() => form.orderTypes.includes('subscription'))
const productScope = computed<'balance' | 'subscription' | 'both'>(() => {
  const hasBalance = form.orderTypes.includes('balance')
  const hasSubscription = form.orderTypes.includes('subscription')
  if (hasBalance && hasSubscription) return 'both'
  return hasSubscription ? 'subscription' : 'balance'
})
const columns = computed<Column[]>(() => [
  { key: 'code', label: t('admin.paymentCoupons.columns.code') },
  { key: 'discount', label: t('admin.paymentCoupons.columns.discount') },
  { key: 'scope', label: t('admin.paymentCoupons.columns.scope') },
  { key: 'usage', label: t('admin.paymentCoupons.columns.usage') },
  { key: 'status', label: t('admin.paymentCoupons.columns.status') },
  { key: 'expires_at', label: t('admin.paymentCoupons.columns.expiresAt') },
  { key: 'actions', label: t('admin.paymentCoupons.columns.actions') },
])

let listRequest = 0
let searchTimer: ReturnType<typeof setTimeout> | undefined
let usageRequest = 0
let auditRequest = 0
let planRequest = 0

function resetForm(): void {
  Object.assign(form, defaultForm())
}

function normalizeOrderTypes(value: PaymentDiscountOrderType[] | undefined): PaymentDiscountOrderType[] {
  const normalized = [...new Set((value || []).filter((type): type is PaymentDiscountOrderType => type === 'balance' || type === 'subscription'))]
  return normalized.length > 0 ? normalized : ['balance', 'subscription']
}

function normalizePlanIds(value: unknown): number[] {
  if (!Array.isArray(value)) return []
  return [...new Set(value.filter((id): id is number => Number.isSafeInteger(id) && id > 0))]
}

function asLocalInput(iso: string): string {
  const timestamp = new Date(iso).getTime()
  return Number.isFinite(timestamp) ? formatDateTimeLocalInput(Math.floor(timestamp / 1000)) : ''
}

function normalizedCustomCode(value: string): string {
  return value.trim().toUpperCase()
}

function isCustomCodeValid(value: string): boolean {
  const code = normalizedCustomCode(value)
  return code === '' || /^[A-Z0-9_-]{8,32}$/.test(code)
}

function normalizeCodeInput(): void {
  form.code = normalizedCustomCode(form.code)
}

function isDiscountValueValid(value: string): boolean {
  const normalized = value.trim()
  if (!/^\d+(?:\.\d{1,2})?$/.test(normalized)) return false
  const amount = Number(normalized)
  return Number.isFinite(amount) && amount > 0 && (form.discountType !== 'percent' || amount <= 100)
}

function setProductScope(scope: 'balance' | 'subscription' | 'both'): void {
  form.orderTypes = scope === 'both' ? ['balance', 'subscription'] : [scope]
  if (scope === 'balance') {
    form.planIds = []
    form.subscriptionPlanMode = 'all'
  }
}

function selectAllSubscriptionPlans(): void {
  form.subscriptionPlanMode = 'all'
  form.planIds = []
}

function togglePlan(planId: number, event: Event): void {
  const target = event.target
  if (!(target instanceof HTMLInputElement)) return
  if (target.checked) {
    form.planIds = [...new Set([...form.planIds, planId])]
  } else {
    form.planIds = form.planIds.filter(id => id !== planId)
  }
}

const missingSelectedPlanIds = computed(() => {
  const availableIds = new Set(plans.value.map(plan => plan.id))
  return form.planIds.filter(id => !availableIds.has(id))
})

async function refreshPlans(): Promise<void> {
  const request = ++planRequest
  plansLoading.value = true
  plansLoadError.value = false
  plans.value = []
  try {
    const response = await adminPaymentAPI.getPlans()
    if (request !== planRequest || !showEditor.value) return
    plans.value = (response.data || []).filter(plan => Number.isSafeInteger(plan.id) && plan.id > 0)
  } catch (err: unknown) {
    if (request !== planRequest || !showEditor.value) return
    plansLoadError.value = true
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('admin.paymentCoupons.plansLoadFailed')))
  } finally {
    if (request === planRequest) plansLoading.value = false
  }
}

function openCreate(): void {
  editingCoupon.value = null
  resetForm()
  showEditor.value = true
  void refreshPlans()
}

function openEdit(coupon: PaymentDiscountCoupon): void {
  editingCoupon.value = coupon
  Object.assign(form, {
    code: coupon.code,
    discountType: coupon.discount_type,
    discountValue: coupon.discount_value,
    currency: coupon.currency,
    maxUses: coupon.max_uses,
    perUserMaxUses: String(coupon.per_user_max_uses),
    targetUserId: coupon.target_user_id == null ? '' : String(coupon.target_user_id),
    startsAt: asLocalInput(coupon.starts_at),
    expiresAt: asLocalInput(coupon.expires_at),
    enabled: coupon.enabled,
    notes: coupon.notes || '',
    orderTypes: normalizeOrderTypes(coupon.order_types),
    planIds: normalizePlanIds(coupon.plan_ids),
    subscriptionPlanMode: coupon.plan_ids?.length ? 'specific' : 'all',
  })
  showEditor.value = true
  void refreshPlans()
}

function closeEditor(): void {
  if (saving.value) return
  planRequest += 1
  plansLoading.value = false
  plansLoadError.value = false
  plans.value = []
  showEditor.value = false
  editingCoupon.value = null
}

function nonNegativeInteger(value: string | number): number | null {
  const normalized = String(value).trim()
  if (!/^\d+$/.test(normalized)) return null
  const parsed = Number(normalized)
  return Number.isSafeInteger(parsed) && parsed >= 0 ? parsed : null
}

function localInputToISO(value: string): string | null {
  const timestamp = parseDateTimeLocalInput(value)
  return timestamp === null ? null : new Date(timestamp * 1000).toISOString()
}

const formValid = computed(() => {
  const maxUses = nonNegativeInteger(form.maxUses)
  const perUser = form.perUserMaxUses.trim() ? nonNegativeInteger(form.perUserMaxUses) : 1
  const targetUser = form.targetUserId.trim() ? Number(form.targetUserId) : null
  const starts = localInputToISO(form.startsAt)
  const expires = localInputToISO(form.expiresAt)
  const hasValidScope = form.orderTypes.length > 0
    && form.orderTypes.every(type => type === 'balance' || type === 'subscription')
    && (!includesSubscription.value || form.subscriptionPlanMode === 'all' || form.planIds.length > 0)
  return isCustomCodeValid(form.code)
    && isDiscountValueValid(form.discountValue)
    && maxUses !== null
    && perUser !== null
    && (targetUser === null || (Number.isSafeInteger(targetUser) && targetUser > 0))
    && !!starts
    && !!expires
    && new Date(expires).getTime() > new Date(starts).getTime()
    && new Date(expires).getTime() > Date.now()
    && hasValidScope
    && !plansLoading.value
    && !plansLoadError.value
})

function buildPayload(): SavePaymentDiscountCouponRequest | null {
  const maxUses = nonNegativeInteger(form.maxUses)
  const startsAt = localInputToISO(form.startsAt)
  const expiresAt = localInputToISO(form.expiresAt)
  const perUserText = form.perUserMaxUses.trim()
  const perUserMaxUses = perUserText ? nonNegativeInteger(perUserText) : null
  const targetText = form.targetUserId.trim()
  const targetUserID = targetText ? Number(targetText) : null
  const validTargetUser = targetUserID === null || (Number.isSafeInteger(targetUserID) && targetUserID > 0)
  const orderTypes = normalizeOrderTypes(form.orderTypes)
  const appliesToSubscription = orderTypes.includes('subscription')
  const planIds = appliesToSubscription && form.subscriptionPlanMode === 'specific'
    ? normalizePlanIds(form.planIds)
    : []
  if (
    !isCustomCodeValid(form.code)
    || !isDiscountValueValid(form.discountValue)
    || maxUses === null
    || !startsAt
    || !expiresAt
    || new Date(expiresAt).getTime() <= new Date(startsAt).getTime()
    || (perUserText && perUserMaxUses === null)
    || !validTargetUser
    || new Date(expiresAt).getTime() <= Date.now()
    || !formValid.value
    || (appliesToSubscription && form.subscriptionPlanMode === 'specific' && planIds.length === 0)
  ) {
    return null
  }
  const payload: SavePaymentDiscountCouponRequest = {
    discount_type: form.discountType,
    discount_value: form.discountValue.trim(),
    currency: form.currency,
    max_uses: maxUses,
    target_user_id: targetUserID,
    starts_at: startsAt,
    expires_at: expiresAt,
    enabled: form.enabled,
    notes: form.notes.trim(),
    order_types: orderTypes,
    plan_ids: planIds,
  }
  if (perUserText && perUserMaxUses !== null) payload.per_user_max_uses = perUserMaxUses
  if (editingCoupon.value) {
    payload.version = editingCoupon.value.version
  } else {
    const customCode = normalizedCustomCode(form.code)
    if (customCode) payload.code = customCode
  }
  return payload
}

async function saveCoupon(): Promise<void> {
  const payload = buildPayload()
  if (!payload) {
    appStore.showError(t('admin.paymentCoupons.invalidForm'))
    return
  }
  saving.value = true
  let saved = false
  try {
    if (editingCoupon.value) {
      await adminPaymentAPI.updatePaymentDiscountCoupon(editingCoupon.value.id, payload)
      appStore.showSuccess(t('admin.paymentCoupons.updated'))
    } else {
      await adminPaymentAPI.createPaymentDiscountCoupon(payload)
      appStore.showSuccess(t('admin.paymentCoupons.created'))
    }
    saved = true
    await loadCoupons()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('admin.paymentCoupons.saveFailed')))
  } finally {
    saving.value = false
    if (saved) closeEditor()
  }
}

async function loadCoupons(): Promise<void> {
  const request = ++listRequest
  loading.value = true
  try {
    const response = await adminPaymentAPI.getPaymentDiscountCoupons({
      page: pagination.page,
      page_size: pagination.page_size,
      search: search.value.trim() || undefined,
    })
    if (request !== listRequest) return
    coupons.value = response.data.items || []
    pagination.total = response.data.total || 0
  } catch (err: unknown) {
    if (request === listRequest) appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('admin.paymentCoupons.loadFailed')))
  } finally {
    if (request === listRequest) loading.value = false
  }
}

function handleSearch(): void {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    pagination.page = 1
    void loadCoupons()
  }, 300)
}

function changePage(page: number): void {
  pagination.page = page
  void loadCoupons()
}

function changePageSize(pageSize: number): void {
  pagination.page_size = pageSize
  pagination.page = 1
  void loadCoupons()
}

async function openHistory(coupon: PaymentDiscountCoupon): Promise<void> {
  historyCoupon.value = coupon
  usagePagination.page = 1
  auditPagination.page = 1
  usages.value = []
  audits.value = []
  showHistory.value = true
  await Promise.all([loadUsages(), loadAudits()])
}

function closeHistory(): void {
  showHistory.value = false
  historyCoupon.value = null
  usageRequest += 1
  auditRequest += 1
}

async function loadUsages(): Promise<void> {
  const coupon = historyCoupon.value
  if (!coupon) return
  const request = ++usageRequest
  usagesLoading.value = true
  try {
    const response = await adminPaymentAPI.getPaymentDiscountCouponUsages(coupon.id, {
      page: usagePagination.page,
      page_size: usagePagination.page_size,
    })
    if (request !== usageRequest || historyCoupon.value?.id !== coupon.id) return
    usages.value = response.data.items || []
    usagePagination.total = response.data.total || 0
  } catch (err: unknown) {
    if (request === usageRequest && historyCoupon.value?.id === coupon.id) appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('admin.paymentCoupons.historyFailed')))
  } finally {
    if (request === usageRequest) usagesLoading.value = false
  }
}

async function loadAudits(): Promise<void> {
  const coupon = historyCoupon.value
  if (!coupon) return
  const request = ++auditRequest
  auditsLoading.value = true
  try {
    const response = await adminPaymentAPI.getPaymentDiscountCouponAudits(coupon.id, {
      page: auditPagination.page,
      page_size: auditPagination.page_size,
    })
    if (request !== auditRequest || historyCoupon.value?.id !== coupon.id) return
    audits.value = response.data.items || []
    auditPagination.total = response.data.total || 0
  } catch (err: unknown) {
    if (request === auditRequest && historyCoupon.value?.id === coupon.id) appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('admin.paymentCoupons.historyFailed')))
  } finally {
    if (request === auditRequest) auditsLoading.value = false
  }
}

function changeUsagePage(page: number): void {
  usagePagination.page = page
  void loadUsages()
}

function changeUsagePageSize(pageSize: number): void {
  usagePagination.page_size = pageSize
  usagePagination.page = 1
  void loadUsages()
}

function changeAuditPage(page: number): void {
  auditPagination.page = page
  void loadAudits()
}

function changeAuditPageSize(pageSize: number): void {
  auditPagination.page_size = pageSize
  auditPagination.page = 1
  void loadAudits()
}

function formatMoney(value: string, currency: string): string {
  return formatCurrency(Number(value), currency || 'CNY')
}

function formatDiscount(coupon: PaymentDiscountCoupon): string {
  return coupon.discount_type === 'percent'
    ? `${coupon.discount_value}%`
    : formatMoney(coupon.discount_value, coupon.currency)
}

function formatProductScope(orderTypes: PaymentDiscountOrderType[]): string {
  const hasBalance = orderTypes.includes('balance')
  const hasSubscription = orderTypes.includes('subscription')
  if (hasBalance && hasSubscription) return t('admin.paymentCoupons.productScopes.both')
  if (hasSubscription) return t('admin.paymentCoupons.productScopes.subscription')
  return t('admin.paymentCoupons.productScopes.balance')
}

function formatCouponScope(coupon: PaymentDiscountCoupon): string {
  const orderTypes = normalizeOrderTypes(coupon.order_types)
  if (!orderTypes.includes('subscription')) return formatProductScope(orderTypes)
  const planIds = normalizePlanIds(coupon.plan_ids)
  const planScope = planIds.length
    ? t('admin.paymentCoupons.selectedPlanCount', { count: planIds.length })
    : t('admin.paymentCoupons.allSubscriptionPlansShort')
  return `${formatProductScope(orderTypes)} · ${planScope}`
}

function usageLabel(coupon: PaymentDiscountCoupon): string {
  const active = coupon.reserved_uses + coupon.consumed_uses
  return `${active} / ${coupon.max_uses === 0 ? '∞' : coupon.max_uses}`
}

function couponStatusLabel(coupon: PaymentDiscountCoupon): string {
  if (!coupon.enabled) return t('admin.paymentCoupons.statuses.disabled')
  if (new Date(coupon.expires_at).getTime() <= Date.now()) return t('admin.paymentCoupons.statuses.expired')
  if (coupon.max_uses > 0 && coupon.reserved_uses + coupon.consumed_uses >= coupon.max_uses) return t('admin.paymentCoupons.statuses.exhausted')
  return t('admin.paymentCoupons.statuses.enabled')
}

function couponStatusClass(coupon: PaymentDiscountCoupon): string {
  if (!coupon.enabled || new Date(coupon.expires_at).getTime() <= Date.now()) return 'badge-gray'
  if (coupon.max_uses > 0 && coupon.reserved_uses + coupon.consumed_uses >= coupon.max_uses) return 'badge-warning'
  return 'badge-success'
}

function usageStatusLabel(status: PaymentDiscountCouponUsage['status']): string {
  return t(`admin.paymentCoupons.usageStatuses.${status}`)
}

function usageStatusClass(status: PaymentDiscountCouponUsage['status']): string {
  if (status === 'consumed') return 'badge-success'
  if (status === 'paid_review') return 'badge-warning'
  if (status === 'released') return 'badge-gray'
  return 'badge-info'
}

function formatAuditOrderTypes(value: unknown): string {
  if (!Array.isArray(value)) return '-'
  const orderTypes = value.filter((type): type is PaymentDiscountOrderType => type === 'balance' || type === 'subscription')
  return orderTypes.length ? formatProductScope(orderTypes) : '-'
}

function formatAuditValue(field: string, value: unknown): string {
  if (field === 'order_types') return formatAuditOrderTypes(value)
  if (field === 'plan_ids') {
    if (!Array.isArray(value)) return '-'
    const planIds = normalizePlanIds(value)
    return planIds.length ? planIds.map(id => `#${id}`).join(', ') : t('admin.paymentCoupons.allSubscriptionPlansShort')
  }
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  if (value == null) return '-'
  try {
    return JSON.stringify(value)
  } catch {
    return String(value)
  }
}

function auditFieldLabel(field: string): string {
  const labels: Record<string, string> = {
    order_types: 'orderTypes',
    plan_ids: 'planIds',
  }
  const key = labels[field]
  return key ? t(`admin.paymentCoupons.auditFields.${key}`) : field
}

function formatAuditConfig(value: unknown): string {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) return formatAuditValue('', value)
  const fields = Object.entries(value as Record<string, unknown>)
  return fields.length
    ? fields.map(([field, fieldValue]) => `${auditFieldLabel(field)}: ${formatAuditValue(field, fieldValue)}`).join('; ')
    : '-'
}

function formatAuditDetail(detail: unknown): string {
  if (detail == null || detail === '') return '-'
  let parsed = detail
  if (typeof detail === 'string') {
    try {
      parsed = JSON.parse(detail)
    } catch {
      return detail
    }
  }
  if (parsed == null || typeof parsed !== 'object' || Array.isArray(parsed)) return formatAuditValue('', parsed)
  const record = parsed as Record<string, unknown>
  if ('before' in record || 'after' in record) {
    return [
      record.before === undefined ? '' : `${t('admin.paymentCoupons.auditBefore')}: ${formatAuditConfig(record.before)}`,
      record.after === undefined ? '' : `${t('admin.paymentCoupons.auditAfter')}: ${formatAuditConfig(record.after)}`,
    ].filter(Boolean).join(' | ')
  }
  return formatAuditConfig(record)
}

onMounted(() => { void loadCoupons() })
onUnmounted(() => { if (searchTimer) clearTimeout(searchTimer) })
</script>
