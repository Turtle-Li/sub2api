<template>
  <BaseDialog :show="show" :title="t('admin.accounts.pools.assignTitle')" width="narrow" @close="$emit('close')">
    <div class="space-y-4">
      <p class="text-sm text-gray-600 dark:text-gray-300">
        {{ t('admin.accounts.pools.assignHint', { count: accountIds.length }) }}
      </p>
      <div class="flex gap-4 text-sm">
        <label class="flex items-center gap-2">
          <input v-model="mode" type="radio" value="existing" :disabled="candidatePools.length === 0" />
          {{ t('admin.accounts.pools.assignExisting') }}
        </label>
        <label class="flex items-center gap-2">
          <input v-model="mode" type="radio" value="new" />
          {{ t('admin.accounts.pools.assignNew') }}
        </label>
      </div>
      <Select
        v-if="mode === 'existing'"
        v-model="targetPoolId"
        :options="candidatePools.map(pool => ({ value: pool.id, label: `${pool.name} (${pool.stats.total})` }))"
        :placeholder="t('admin.accounts.pools.selectPool')"
      />
      <input
        v-else
        v-model="newPoolName"
        type="text"
        class="input"
        maxlength="100"
        :placeholder="t('admin.accounts.pools.name')"
        data-testid="account-pool-assign-new-name"
      />
    </div>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="$emit('close')">{{ t('common.cancel') }}</button>
        <button type="button" class="btn btn-primary" :disabled="!canSubmit" @click="submit">
          {{ submitting ? t('common.saving') : t('common.confirm') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import { adminAPI } from '@/api/admin'
import type { AccountPool } from '@/api/admin/accountPools'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{
  show: boolean
  /** Pools are single-platform; the caller guarantees all accounts share it. */
  platform: string
  accountIds: number[]
  /** Pool the accounts currently belong to (hidden from targets). */
  excludePoolId?: number | null
}>()

const emit = defineEmits<{
  close: []
  assigned: [poolId: number]
}>()

const { t } = useI18n()
const appStore = useAppStore()
const pools = ref<AccountPool[]>([])
const mode = ref<'existing' | 'new'>('existing')
const targetPoolId = ref<number | null>(null)
const newPoolName = ref('')
const submitting = ref(false)

const candidatePools = computed(() => pools.value.filter(pool => pool.id !== props.excludePoolId))
const canSubmit = computed(() =>
  !submitting.value &&
  props.accountIds.length > 0 &&
  (mode.value === 'existing' ? targetPoolId.value != null : newPoolName.value.trim() !== '')
)

watch(
  () => props.show,
  async (show) => {
    if (!show) return
    targetPoolId.value = null
    newPoolName.value = ''
    pools.value = []
    try {
      pools.value = await adminAPI.accountPools.list(props.platform)
    } catch (error) {
      appStore.showError(extractApiErrorMessage(error, t('admin.accounts.pools.loadFailed')))
    }
    mode.value = candidatePools.value.length > 0 ? 'existing' : 'new'
  },
  { immediate: true }
)

const submit = async () => {
  if (!canSubmit.value) return
  submitting.value = true
  try {
    let poolId = targetPoolId.value
    if (mode.value === 'new') {
      const created = await adminAPI.accountPools.create({ name: newPoolName.value.trim(), platform: props.platform })
      poolId = created.id
    }
    if (poolId == null) return
    const result = await adminAPI.accountPools.addMembers(poolId, props.accountIds)
    appStore.showSuccess(t('admin.accounts.pools.assignSuccess', { count: result.affected }))
    emit('assigned', poolId)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accounts.pools.assignFailed')))
  } finally {
    submitting.value = false
  }
}
</script>
