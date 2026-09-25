<template>
  <div v-if="pools.length > 0" data-testid="account-pool-select">
    <label class="input-label">{{ t('admin.accounts.pools.importTarget') }}</label>
    <Select v-model="selected" :options="options" />
    <p class="input-hint">{{ t('admin.accounts.pools.importTargetHint') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import { adminAPI } from '@/api/admin'
import type { AccountPool } from '@/api/admin/accountPools'

const props = defineProps<{
  /** Pools are single-platform; only pools of this platform are offered. */
  platform: string
}>()

const model = defineModel<number | null>({ default: null })

const { t } = useI18n()
const pools = ref<AccountPool[]>([])
const NONE = 0

const options = computed(() => [
  { value: NONE, label: t('admin.accounts.pools.importTargetNone') },
  ...pools.value.map(pool => ({ value: pool.id, label: `${pool.name} (${pool.stats.total})` }))
])

const selected = computed({
  get: () => model.value ?? NONE,
  set: (value: string | number | boolean | null) => {
    const id = Number(value)
    model.value = id > 0 ? id : null
  }
})

let requestSeq = 0
watch(
  () => props.platform,
  async (platform) => {
    const seq = ++requestSeq
    pools.value = []
    if (!platform) {
      model.value = null
      return
    }
    try {
      const loaded = await adminAPI.accountPools.list(platform)
      if (seq !== requestSeq) return
      pools.value = loaded
    } catch (error) {
      // Optional field: without pools the selector simply stays hidden.
      console.error('Failed to load account pools:', error)
    }
    if (seq === requestSeq && model.value != null && !pools.value.some(pool => pool.id === model.value)) {
      model.value = null
    }
  },
  { immediate: true }
)
</script>
