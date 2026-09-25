<template>
  <BaseDialog
    :show="show"
    :title="pool ? t('admin.accounts.pools.editTitle') : t('admin.accounts.pools.createTitle')"
    width="narrow"
    @close="$emit('close')"
  >
    <form id="account-pool-form" class="space-y-4" @submit.prevent="submit">
      <div>
        <label class="input-label">{{ t('admin.accounts.pools.name') }}</label>
        <input v-model="form.name" type="text" class="input" maxlength="100" required data-testid="account-pool-name" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.accounts.pools.platform') }}</label>
        <Select v-if="!pool" v-model="form.platform" :options="platformOptions" data-testid="account-pool-platform" />
        <div v-else class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
          <PlatformIcon :platform="pool.platform as GroupPlatform" />
          {{ platformLabel(pool.platform) }}
        </div>
        <p class="input-hint">{{ t('admin.accounts.pools.platformHint') }}</p>
      </div>
      <div>
        <label class="input-label">{{ t('admin.accounts.pools.notes') }}</label>
        <textarea v-model="form.notes" rows="2" class="input" maxlength="500" />
      </div>
    </form>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="$emit('close')">{{ t('common.cancel') }}</button>
        <button type="submit" form="account-pool-form" class="btn btn-primary" :disabled="submitting || !form.name.trim() || !form.platform">
          {{ submitting ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import { adminAPI } from '@/api/admin'
import type { AccountPool } from '@/api/admin/accountPools'
import type { GroupPlatform } from '@/types'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{
  show: boolean
  /** Pool being edited; null creates a new pool. */
  pool: AccountPool | null
  defaultPlatform?: string
}>()

const emit = defineEmits<{
  close: []
  saved: [pool: AccountPool]
}>()

const { t } = useI18n()
const appStore = useAppStore()
const submitting = ref(false)
const form = reactive({ name: '', platform: '', notes: '' })
const platformOptions = [...CONCRETE_PLATFORM_OPTIONS]
const platformLabel = (platform: string) =>
  CONCRETE_PLATFORM_OPTIONS.find(option => option.value === platform)?.label ?? platform

watch(
  () => props.show,
  (show) => {
    if (!show) return
    form.name = props.pool?.name ?? ''
    form.platform = props.pool?.platform ?? props.defaultPlatform ?? 'grok'
    form.notes = props.pool?.notes ?? ''
  },
  { immediate: true }
)

const submit = async () => {
  const name = form.name.trim()
  if (!name || !form.platform || submitting.value) return
  submitting.value = true
  try {
    const notes = form.notes.trim() || null
    const saved = props.pool
      ? await adminAPI.accountPools.update(props.pool.id, { name, notes })
      : await adminAPI.accountPools.create({ name, platform: form.platform, notes })
    appStore.showSuccess(t('admin.accounts.pools.saved'))
    emit('saved', saved)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accounts.pools.saveFailed')))
  } finally {
    submitting.value = false
  }
}
</script>
