<template>
  <section class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800" :aria-label="t('payment.admin.resetCardTierPolicy')">
    <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div>
        <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('payment.admin.resetCardTierPolicy') }}</h2>
        <p class="mt-1 max-w-3xl text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('payment.admin.resetCardTierPolicyHint') }}</p>
      </div>
      <button type="button" class="btn btn-secondary shrink-0" :disabled="loading || savingGroupIDs.size > 0" @click="load">
        <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
        {{ t('common.refresh') }}
      </button>
    </div>

    <div v-if="loadError || saveError" role="alert" class="mt-4 rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">
      {{ loadError || saveError }}
    </div>

    <div v-if="loading" role="status" class="mt-4 rounded-lg border border-gray-200 p-4 text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">
      {{ t('common.loading') }}
    </div>
    <p v-else-if="groupRows.length === 0" class="mt-4 text-sm text-gray-500 dark:text-gray-400">
      {{ t('payment.admin.resetCardTierNoSubscriptionGroups') }}
    </p>
    <div v-else class="mt-4 divide-y divide-gray-200 rounded-lg border border-gray-200 dark:divide-dark-600 dark:border-dark-600">
      <article v-for="row in groupRows" :key="row.groupID" class="p-4">
        <div class="flex flex-col gap-1 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
          <div>
            <h3 class="text-sm font-medium text-gray-900 dark:text-white">{{ row.groupName }}</h3>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.resetCardTierGroupMeta', { id: row.groupID, count: row.planCount }) }}</p>
          </div>
          <span
            :class="row.policy
              ? 'text-primary-700 dark:text-primary-300'
              : 'text-amber-700 dark:text-amber-300'"
            class="text-xs font-medium"
          >
            {{ row.policy
              ? t('payment.admin.resetCardTierConfigured', { family: row.policy.family_key, rank: row.policy.tier_rank })
              : t('payment.admin.resetCardTierUnconfigured') }}
          </span>
        </div>

        <div class="mt-4 grid gap-3 sm:grid-cols-[minmax(0,1fr)_10rem_auto] sm:items-end">
          <div>
            <label :for="`reset-card-family-${row.groupID}`" class="input-label">{{ t('payment.admin.resetCardTierFamilyKey') }}</label>
            <input
              :id="`reset-card-family-${row.groupID}`"
              v-model="draftFor(row.groupID).family_key"
              :data-testid="`tier-family-${row.groupID}`"
              class="input"
              maxlength="64"
              autocomplete="off"
              spellcheck="false"
              :placeholder="t('payment.admin.resetCardTierFamilyKeyPlaceholder')"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.resetCardTierFamilyKeyHint') }}</p>
          </div>
          <div>
            <label :for="`reset-card-rank-${row.groupID}`" class="input-label">{{ t('payment.admin.resetCardTierRank') }}</label>
            <input
              :id="`reset-card-rank-${row.groupID}`"
              v-model.number="draftFor(row.groupID).tier_rank"
              :data-testid="`tier-rank-${row.groupID}`"
              class="input"
              type="number"
              min="1"
              step="1"
              inputmode="numeric"
            />
          </div>
          <button
            type="button"
            class="btn btn-primary"
            :data-testid="`save-tier-${row.groupID}`"
            :disabled="loading || isSaving(row.groupID) || !isDraftValid(row.groupID)"
            @click="save(row.groupID)"
          >
            {{ isSaving(row.groupID) ? t('common.processing') : t('common.save') }}
          </button>
        </div>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'
import type { AdminGroup } from '@/types'
import type { ResetCardTierPolicy, ResetCardTierPolicyInput, SubscriptionPlan } from '@/types/payment'
import Icon from '@/components/icons/Icon.vue'

interface TierRow {
  groupID: number
  groupName: string
  planCount: number
  policy?: ResetCardTierPolicy
}

const props = withDefaults(defineProps<{
  plans: SubscriptionPlan[]
  groups?: AdminGroup[]
}>(), {
  groups: () => [],
})

const emit = defineEmits<{ saved: [] }>()
const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(false)
const loadError = ref('')
const saveError = ref('')
const policyByGroup = ref<Record<number, ResetCardTierPolicy>>({})
const drafts = ref<Record<number, ResetCardTierPolicyInput>>({})
const savingGroupIDs = ref(new Set<number>())
const familyKeyPattern = /^[a-z][a-z0-9_-]{0,63}$/

const groupRows = computed<TierRow[]>(() => {
  const rows = new Map<number, TierRow>()
  for (const plan of props.plans) {
    if (!Number.isSafeInteger(plan.group_id) || plan.group_id <= 0) continue
    const existing = rows.get(plan.group_id)
    const group = props.groups.find(candidate => candidate.id === plan.group_id)
    if (existing) {
      existing.planCount += 1
      if (!existing.policy && plan.reset_card_tier) existing.policy = plan.reset_card_tier
      continue
    }
    rows.set(plan.group_id, {
      groupID: plan.group_id,
      groupName: plan.group_name || group?.name || t('payment.groupFallback', { id: plan.group_id }),
      planCount: 1,
      policy: policyByGroup.value[plan.group_id] || plan.reset_card_tier,
    })
  }
  return [...rows.values()]
    .map(row => ({ ...row, policy: policyByGroup.value[row.groupID] || row.policy }))
    .sort((left, right) => left.groupID - right.groupID)
})

function draftFromPolicy(policy?: ResetCardTierPolicy): ResetCardTierPolicyInput {
  return {
    family_key: policy?.family_key || '',
    tier_rank: policy?.tier_rank || 1,
  }
}

function syncMissingDrafts() {
  const next = { ...drafts.value }
  for (const row of groupRows.value) {
    if (!next[row.groupID]) next[row.groupID] = draftFromPolicy(row.policy)
  }
  drafts.value = next
}

watch(groupRows, syncMissingDrafts, { immediate: true })

function draftFor(groupID: number): ResetCardTierPolicyInput {
  return drafts.value[groupID] || draftFromPolicy(policyByGroup.value[groupID])
}

function normalizedDraft(groupID: number): ResetCardTierPolicyInput {
  const draft = draftFor(groupID)
  return {
    family_key: draft.family_key.trim().toLowerCase(),
    tier_rank: Number(draft.tier_rank),
  }
}

function isDraftValid(groupID: number): boolean {
  const draft = normalizedDraft(groupID)
  return familyKeyPattern.test(draft.family_key) && Number.isSafeInteger(draft.tier_rank) && draft.tier_rank > 0
}

function isSaving(groupID: number): boolean {
  return savingGroupIDs.value.has(groupID)
}

function setPolicies(policies: ResetCardTierPolicy[]) {
  const nextPolicies: Record<number, ResetCardTierPolicy> = {}
  for (const policy of policies) {
    if (Number.isSafeInteger(policy.group_id) && policy.group_id > 0) nextPolicies[policy.group_id] = policy
  }
  policyByGroup.value = nextPolicies
  const nextDrafts: Record<number, ResetCardTierPolicyInput> = {}
  for (const row of groupRows.value) {
    nextDrafts[row.groupID] = draftFromPolicy(nextPolicies[row.groupID] || row.policy)
  }
  drafts.value = nextDrafts
}

async function load() {
  loading.value = true
  loadError.value = ''
  saveError.value = ''
  try {
    const response = await adminPaymentAPI.getResetCardTierPolicies()
    setPolicies(response.data || [])
  } catch (err: unknown) {
    loadError.value = extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))
  } finally {
    loading.value = false
  }
}

async function save(groupID: number) {
  if (isSaving(groupID)) return
  const payload = normalizedDraft(groupID)
  if (!familyKeyPattern.test(payload.family_key) || !Number.isSafeInteger(payload.tier_rank) || payload.tier_rank <= 0) {
    saveError.value = t('payment.admin.resetCardTierInvalidInput')
    return
  }

  saveError.value = ''
  savingGroupIDs.value = new Set(savingGroupIDs.value).add(groupID)
  try {
    const response = await adminPaymentAPI.updateResetCardTierPolicy(groupID, payload)
    const policy = response.data
    policyByGroup.value = { ...policyByGroup.value, [groupID]: policy }
    drafts.value = { ...drafts.value, [groupID]: draftFromPolicy(policy) }
    appStore.showSuccess(t('payment.admin.resetCardTierSaved'))
    emit('saved')
  } catch (err: unknown) {
    saveError.value = extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))
    appStore.showError(saveError.value)
  } finally {
    const next = new Set(savingGroupIDs.value)
    next.delete(groupID)
    savingGroupIDs.value = next
  }
}

onMounted(load)
</script>
