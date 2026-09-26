<template>
  <div data-testid="proxy-pool-select">
    <label class="input-label">{{ label ?? t('admin.proxies.pools.label') }}</label>
    <Select v-model="selected" :options="options" />
    <div v-if="creating" class="mt-2 flex gap-2">
      <input
        v-model="newPoolName"
        type="text"
        class="input flex-1"
        maxlength="100"
        :placeholder="t('admin.proxies.pools.name')"
        data-testid="proxy-pool-select-new-name"
        @keydown.enter.prevent="createPool"
      />
      <button
        type="button"
        class="btn btn-secondary shrink-0"
        :disabled="submitting || !newPoolName.trim()"
        data-testid="proxy-pool-select-create"
        @click="createPool"
      >
        {{ submitting ? t('common.saving') : t('admin.proxies.pools.createAndSelect') }}
      </button>
    </div>
    <p class="input-hint">{{ hint ?? t('admin.proxies.pools.hint') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import { adminAPI } from '@/api/admin'
import type { ProxyPool } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

defineProps<{
  label?: string
  hint?: string
}>()

const model = defineModel<number | null>({ default: null })

const { t } = useI18n()
const appStore = useAppStore()
const pools = ref<ProxyPool[]>([])
const creating = ref(false)
const newPoolName = ref('')
const submitting = ref(false)
const NONE = 0
const CREATE = -1

const options = computed(() => [
  { value: NONE, label: t('admin.proxies.pools.none') },
  ...pools.value.map(pool => ({
    value: pool.id,
    label: `${pool.name} (${pool.stats.available}/${pool.stats.total})`
  })),
  { value: CREATE, label: t('admin.proxies.pools.createInline') }
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
  if (!name || submitting.value) return
  submitting.value = true
  try {
    const created = await adminAPI.proxyPools.create({ name })
    pools.value = [...pools.value, created]
    creating.value = false
    newPoolName.value = ''
    model.value = created.id
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.proxies.pools.saveFailed')))
  } finally {
    submitting.value = false
  }
}

const loadPools = async () => {
  pools.value = []
  creating.value = false
  try {
    pools.value = await adminAPI.proxyPools.list()
  } catch (error) {
    // This is an optional assignment field; retain the none/create options.
    console.error('Failed to load proxy pools:', error)
  }
  if (model.value != null && !pools.value.some(pool => pool.id === model.value)) {
    model.value = null
  }
}

onMounted(loadPools)
</script>
