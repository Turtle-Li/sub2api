<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getImageModels, type AdminImageModel } from '@/api/admin/imageModels'
import type { AdminGroup } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ groups: AdminGroup[]; revision: number }>()
const emit = defineEmits<{ configure: [groupId: number, model: string] }>()
const { t, te } = useI18n()
type Binding = { group: AdminGroup; ready: boolean }
type Row = { model: AdminImageModel; bindings: Binding[] }
const rows = ref<Row[]>([])
const search = ref('')
const loading = ref(false)
const error = ref('')
let version = 0
const visible = computed(() => rows.value.filter(row => `${row.model.id} ${row.model.provider}`.toLowerCase().includes(search.value.toLowerCase())))
function providerName(provider: string) {
  const key = `admin.imageModels.providers.${provider}`
  return te(key) ? t(key) : provider
}
function price(row: Row) {
  const quotes = Object.values(row.model.prices || {})
  if (quotes.length) {
    const minimum = Math.min(...quotes.map(quote => quote.min)), maximum = Math.max(...quotes.map(quote => quote.max))
    return minimum === maximum ? String(Number(minimum.toFixed(4))) : `${Number(minimum.toFixed(4))} – ${Number(maximum.toFixed(4))}`
  }
  const group = row.bindings[0].group
  const tiers = row.model.capabilities?.image_sizes || ['1K', '2K', '4K']
  const prices: Record<string, number | null> = { '1K': group.image_price_1k, '2K': group.image_price_2k, '4K': group.image_price_4k }
  const values = tiers.map(tier => prices[tier]).filter((value): value is number => value !== null && value !== undefined)
  if (!values.length) return t('admin.imageModels.defaultPrice')
  const minimum = Math.min(...values), maximum = Math.max(...values)
  return minimum === maximum ? String(minimum) : `${minimum} – ${maximum}`
}
watch(() => [props.groups, props.revision] as const, async () => {
  const current = ++version
  const configured = props.groups.filter(group => (group.platform === 'openai' || group.platform === 'gemini') && (group.allow_batch_image_generation || group.allow_image_generation))
  loading.value = true; error.value = ''
  const collected = new Map<string, Row>()
  for (let start = 0; start < configured.length; start += 3) {
    await Promise.all(configured.slice(start, start + 3).map(async group => {
      try {
        const catalogue = await getImageModels(group.id)
        if (current !== version) return
        const available = new Set(catalogue.models.map(model => `${model.provider}:${model.id}`))
        for (const model of catalogue.candidates) {
          const identity = `${model.provider}:${model.id}`
          const quoted = catalogue.models.find(item => `${item.provider}:${item.id}` === identity)
          const row = collected.get(identity) || { model: { ...model, prices: {} }, bindings: [] }
          const prices = { ...row.model.prices }
          for (const [size, quote] of Object.entries(quoted?.prices || {})) {
            const previous = prices[size]
            prices[size] = previous ? { min: Math.min(previous.min, quote.min), max: Math.max(previous.max, quote.max) } : quote
          }
          row.model = { ...row.model, prices }
          row.bindings.push({ group, ready: available.has(identity) }); collected.set(identity, row)
        }
      } catch (cause) { if (current === version) error.value = extractApiErrorMessage(cause) }
    }))
    if (current !== version) return
  }
  rows.value = [...collected.values()].map(row => ({ ...row, bindings: row.bindings.sort((a, b) => Number(b.ready) - Number(a.ready) || Number(b.group.allow_batch_image_generation) - Number(a.group.allow_batch_image_generation) || a.group.id - b.group.id) }))
    .sort((a, b) => Number(b.bindings.some(item => item.ready)) - Number(a.bindings.some(item => item.ready)) || a.model.id.localeCompare(b.model.id))
  loading.value = false
}, { immediate: true })
onBeforeUnmount(() => { version++ })
</script>

<template>
  <section class="card overflow-hidden" aria-label="生图模型目录">
    <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 p-5 dark:border-dark-600">
      <div><h2 class="font-semibold">{{ t('admin.imageModels.models') }}</h2><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">直接选择模型管理价格与可用性，分组和账号沿用现有配置。</p></div>
      <input v-model="search" type="search" class="input w-full sm:w-64" :placeholder="t('admin.imageModels.searchModels')" :aria-label="t('admin.imageModels.searchModels')" />
    </div>
    <p v-if="error" role="alert" class="m-5 rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
    <p v-if="loading" role="status" class="p-10 text-center text-sm text-gray-500">{{ t('common.loading') }}</p>
    <div v-else-if="visible.length" class="divide-y divide-gray-200 dark:divide-dark-600">
      <article v-for="row in visible" :key="`${row.model.provider}:${row.model.id}`" class="grid gap-4 p-5 sm:grid-cols-[minmax(0,1fr)_10rem_auto] sm:items-center">
        <div class="min-w-0"><h3 class="break-all text-sm font-semibold">{{ row.model.id }}</h3><p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ providerName(row.model.provider) }}<template v-if="row.model.capabilities"> · {{ row.model.capabilities.image_sizes.join(' / ') }} · {{ t('admin.imageModels.references', { count: row.model.capabilities.max_reference_images }) }}</template></p></div>
        <div><p class="text-sm font-medium">{{ price(row) }}<span class="ml-1 text-xs font-normal text-gray-500">/ 张</span></p><p class="mt-1 text-xs text-gray-500">{{ Object.keys(row.model.prices || {}).length ? '当前分组报价 · 内部额度' : '分组源价 · 叠加现有倍率' }}</p></div>
        <div class="flex items-center gap-3"><span class="whitespace-nowrap rounded-md px-2 py-1 text-xs" :class="row.bindings.some(item => item.ready) ? 'bg-teal-50 text-teal-700 dark:bg-teal-900/20 dark:text-teal-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'">{{ t(row.bindings.some(item => item.ready) ? 'admin.imageModels.ready' : 'admin.imageModels.candidate') }}</span><button type="button" class="btn btn-secondary" :aria-label="`配置 ${row.model.id}`" @click="emit('configure', row.bindings[0].group.id, row.model.id)">配置</button></div>
      </article>
    </div>
    <p v-else class="p-10 text-center text-sm text-gray-500">{{ search ? t('admin.imageModels.noModels') : '尚未配置生图通道。在分组管理中开启生图权限并绑定账号后，模型会显示在这里。' }}</p>
    <p class="border-t border-gray-200 px-5 py-4 text-xs leading-relaxed text-gray-500 dark:border-dark-600 dark:text-gray-400">配置可用不代表上游已通过真实调用。价格和权限随配置保存生效；模型专属报价优先于分组默认价，具体来源可在模型配置中查看。</p>
  </section>
</template>
