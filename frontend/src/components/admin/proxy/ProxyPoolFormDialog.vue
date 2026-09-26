<template>
  <BaseDialog
    :show="show"
    :title="pool ? t('admin.proxies.pools.editTitle') : t('admin.proxies.pools.createTitle')"
    width="narrow"
    @close="$emit('close')"
  >
    <form id="proxy-pool-form" class="space-y-4" @submit.prevent="submit">
      <div>
        <label class="input-label">{{ t('admin.proxies.pools.name') }}</label>
        <input v-model="form.name" type="text" class="input" maxlength="100" required data-testid="proxy-pool-name" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.proxies.pools.notes') }}</label>
        <textarea v-model="form.notes" rows="3" class="input" maxlength="500" />
      </div>
    </form>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="$emit('close')">{{ t('common.cancel') }}</button>
        <button type="submit" form="proxy-pool-form" class="btn btn-primary" :disabled="submitting || !form.name.trim()">
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
import { adminAPI } from '@/api/admin'
import type { ProxyPool } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ show: boolean; pool: ProxyPool | null }>()
const emit = defineEmits<{ close: []; saved: [pool: ProxyPool] }>()
const { t } = useI18n()
const appStore = useAppStore()
const submitting = ref(false)
const form = reactive({ name: '', notes: '' })

watch(
  () => props.show,
  (show) => {
    if (!show) return
    form.name = props.pool?.name ?? ''
    form.notes = props.pool?.notes ?? ''
  },
  { immediate: true }
)

const submit = async () => {
  const name = form.name.trim()
  if (!name || submitting.value) return
  submitting.value = true
  try {
    const notes = form.notes.trim() || null
    const saved = props.pool
      ? await adminAPI.proxyPools.update(props.pool.id, { name, notes })
      : await adminAPI.proxyPools.create({ name, notes })
    appStore.showSuccess(t('admin.proxies.pools.saved'))
    emit('saved', saved)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.proxies.pools.saveFailed')))
  } finally {
    submitting.value = false
  }
}
</script>
