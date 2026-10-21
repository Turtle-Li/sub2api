<template>
  <div data-testid="account-pool-select">
    <label class="input-label">{{ label ?? t('admin.accounts.pools.importTarget') }}</label>
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
  label?: string
  noneLabel?: string
  /**
   * Controlled pool list. When provided, the component renders these pools
   * without fetching and hides inline creation, so callers that already loaded
   * pools (e.g. the account editor) stay in sync with the main list.
   */
  pools?: AccountPool[]
}>()

const model = defineModel<number | null>({ default: null })

const { t } = useI18n()
const appStore = useAppStore()
const fetchedPools = ref<AccountPool[]>([])
const creating = ref(false)
const newPoolName = ref('')
const submitting = ref(false)
const NONE = 0
const CREATE = -1

const isControlled = computed(() => props.pools !== undefined)
const pools = computed(() => props.pools ?? fetchedPools.value)
const availablePools = computed(() =>
  props.platform ? pools.value.filter(pool => pool.platform === props.platform) : pools.value
)

const options = computed(() => {
  const items = [
    { value: NONE, label: props.noneLabel ?? t('admin.accounts.pools.importTargetNone') },
    ...availablePools.value.map(pool => ({
      value: pool.id,
      label: props.platform ? `${pool.name} (${pool.stats.total})` : `${pool.name} · ${pool.platform} (${pool.stats.total})`
    }))
  ]
  // Keep a pooled account's current pool visible even when the caller's pool
  // list is still loading; otherwise the select would silently show "none".
  if (model.value != null && !availablePools.value.some(pool => pool.id === model.value)) {
    items.push({
      value: model.value,
      label: t('admin.accounts.pools.currentPoolFallback', { id: model.value })
    })
  }
  if (!isControlled.value && props.platform) {
    items.push({ value: CREATE, label: t('admin.accounts.pools.createInline') })
  }
  return items
})

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
    fetchedPools.value = [...fetchedPools.value, created]
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
    creating.value = false
    // Controlled callers own the list; do not fetch or clear a valid selection.
    if (isControlled.value) return
    const seq = ++requestSeq
    fetchedPools.value = []
    let loaded = false
    try {
      const result = await adminAPI.accountPools.list(platform || undefined)
      if (seq !== requestSeq) return
      fetchedPools.value = result
      loaded = true
    } catch (error) {
      // Optional field: without pools only "none" (and create) remain.
      console.error('Failed to load account pools:', error)
    }
    // Only clear a stale selection after a successful load. A transient list
    // failure must not silently move the account out of its pool.
    if (loaded && model.value != null && !availablePools.value.some(pool => pool.id === model.value)) {
      model.value = null
    }
  },
  { immediate: true }
)
</script>
