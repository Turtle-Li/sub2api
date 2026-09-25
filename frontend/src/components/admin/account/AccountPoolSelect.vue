<template>
  <div data-testid="account-pool-select">
    <label class="input-label">{{ t('admin.accounts.pools.importTarget') }}</label>
    <Select v-model="selected" :options="options" />
    <div v-if="creating" class="mt-2 flex gap-2">
      <input
        v-model="newPoolName"
        type="text"
        class="input flex-1"
        maxlength="100"
        :placeholder="t('admin.accounts.pools.name')"
        data-testid="account-pool-select-new-name"
        @keydown.enter.prevent="createPool"
      />
      <button
        type="button"
        class="btn btn-secondary shrink-0"
        :disabled="submitting || !newPoolName.trim()"
        data-testid="account-pool-select-create"
        @click="createPool"
      >
        {{ submitting ? t('common.saving') : t('admin.accounts.pools.createAndSelect') }}
      </button>
    </div>
    <p class="input-hint">{{ hint ?? t('admin.accounts.pools.importTargetHint') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import { adminAPI } from '@/api/admin'
import type { AccountPool } from '@/api/admin/accountPools'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{
  /**
   * Pools are single-platform; only pools of this platform are offered and new
   * pools are created for it. Empty lists pools of every platform (no create).
   */
  platform?: string
  hint?: string
}>()

const model = defineModel<number | null>({ default: null })

const { t } = useI18n()
const appStore = useAppStore()
const pools = ref<AccountPool[]>([])
const creating = ref(false)
const newPoolName = ref('')
const submitting = ref(false)
const NONE = 0
const CREATE = -1

const options = computed(() => [
  { value: NONE, label: t('admin.accounts.pools.importTargetNone') },
  ...pools.value.map(pool => ({
    value: pool.id,
    label: props.platform ? `${pool.name} (${pool.stats.total})` : `${pool.name} · ${pool.platform} (${pool.stats.total})`
  })),
  ...(props.platform ? [{ value: CREATE, label: t('admin.accounts.pools.createInline') }] : [])
])

const selected = computed({
  get: () => (creating.value ? CREATE : model.value ?? NONE),
  set: (value: string | number | boolean | null) => {
    const id = Number(value)
    creating.value = id === CREATE
    model.value = id > 0 ? id : null
  }
})

const createPool = async () => {
  const name = newPoolName.value.trim()
  if (!name || !props.platform || submitting.value) return
  submitting.value = true
  try {
    const created = await adminAPI.accountPools.create({ name, platform: props.platform })
    pools.value = [...pools.value, created]
    creating.value = false
    newPoolName.value = ''
    model.value = created.id
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accounts.pools.saveFailed')))
  } finally {
    submitting.value = false
  }
}

let requestSeq = 0
watch(
  () => props.platform,
  async (platform) => {
    const seq = ++requestSeq
    pools.value = []
    creating.value = false
    try {
      const loaded = await adminAPI.accountPools.list(platform || undefined)
      if (seq !== requestSeq) return
      pools.value = loaded
    } catch (error) {
      // Optional field: without pools only "none" (and create) remain.
      console.error('Failed to load account pools:', error)
    }
    if (seq === requestSeq && model.value != null && !pools.value.some(pool => pool.id === model.value)) {
      model.value = null
    }
  },
  { immediate: true }
)
</script>
