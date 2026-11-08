<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { ShopSKU } from '@/types/shop'
import { newShopSKU, skuAttributeKey } from '@/utils/shopSku'
const props = defineProps<{ modelValue: ShopSKU[]; defaultId: string; native: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [skus: ShopSKU[]]; 'update:defaultId': [id: string] }>()
const multiple = ref(false)
const attributes = ref<{ name: string; values: string }[]>([])
const error = ref('')
const attributesExpanded = ref(false)
const rows = computed(() => props.modelValue)
watch(() => props.modelValue, skus => {
  multiple.value = skus.length > 1 || Object.keys(skus[0]?.attributes ?? {}).length > 0
  attributes.value = Object.keys(skus[0]?.attributes ?? {}).map(name => ({ name, values: [...new Set(skus.map(sku => sku.attributes[name]))].join('，') }))
}, { immediate: true })
function update(index: number, patch: Partial<ShopSKU>) {
  emit('update:modelValue', rows.value.map((sku, i) => i === index ? { ...sku, ...patch } : sku))
}
function changeMode(value: boolean) {
  if (value) { multiple.value = true; attributesExpanded.value = true; if (!attributes.value.length) attributes.value.push({ name: '', values: '' }); return }
  const sku = rows.value.find(item => item.id === props.defaultId) ?? rows.value[0]
  if (!sku) return
  emit('update:modelValue', [{ ...sku, attributes: {} }]); emit('update:defaultId', sku.id)
}
function generate() {
  error.value = ''
  const names = attributes.value.map(attribute => attribute.name.trim())
  const values = attributes.value.map(attribute => [...new Set(attribute.values.split(/[,，\n]/).map(value => value.trim()).filter(Boolean))])
  if (!names.length || names.some(name => !name || name.length > 40) || new Set(names).size !== names.length || values.some(value => !value.length || value.some(item => item.length > 80))) { error.value = '请填写不重复的属性名称和属性值，多个值用逗号分隔。'; return }
  if (values.reduce((total, value) => total * value.length, 1) > 100) { error.value = '规格组合最多 100 个，请减少属性值。'; return }
  const combos = values.reduce<Record<string, string>[]>((all, options, index) => all.flatMap(combo => options.map(value => ({ ...combo, [names[index]]: value }))), [{}])
  const existing = new Map(rows.value.map(sku => [skuAttributeKey(sku.attributes), sku]))
  const skus = combos.map(combo => existing.get(skuAttributeKey(combo)) ?? newShopSKU({ price: 0, duration_value: 1, duration_unit: 'month' }, combo))
  emit('update:modelValue', skus)
  if (!skus.some(sku => sku.id === props.defaultId)) emit('update:defaultId', skus[0].id)
}
function daypassTemplate() {
  const skus = [1, 7].flatMap(days => [2, 3].map(concurrency => {
    const previous = rows.value.find(sku => sku.duration_unit === 'day' && sku.duration_value === days && sku.concurrency === concurrency)
    const attributes = { 有效期: days === 1 ? '日卡' : '周卡', 并发: `${concurrency} 路` }
    return { ...(previous ?? newShopSKU({ price: 0, duration_value: days, duration_unit: 'day' })), attributes, concurrency }
  }))
  emit('update:modelValue', skus)
  if (!skus.some(sku => sku.id === props.defaultId)) emit('update:defaultId', skus[0].id)
}
</script>
<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <fieldset class="flex items-center gap-5 text-sm"><legend class="sr-only">规格类型</legend><label class="flex min-h-10 items-center gap-2"><input type="radio" name="sku-mode" :checked="!multiple" @change="changeMode(false)" />单规格</label><label class="flex min-h-10 items-center gap-2"><input type="radio" name="sku-mode" :checked="multiple" @change="changeMode(true)" />多规格</label></fieldset>
      <button v-if="native" type="button" class="btn btn-secondary text-sm" @click="daypassTemplate">载入日卡 / 周卡规格</button>
    </div>
    <details v-if="multiple" :open="attributesExpanded" class="rounded-lg bg-gray-50 p-4 dark:bg-dark-800/60" @toggle="attributesExpanded = ($event.target as HTMLDetailsElement).open">
      <summary class="cursor-pointer text-sm font-medium">商品属性 <span class="ml-2 text-xs font-normal text-gray-500 dark:text-gray-400">{{ attributes.length }} 个属性 · 编辑与生成组合</span></summary>
      <div class="mt-4 space-y-3">
      <div v-for="(attribute, index) in attributes" :key="index" class="flex flex-wrap items-end gap-2">
        <label class="w-28 text-xs text-gray-500 dark:text-gray-400">属性名称<input v-model="attribute.name" :aria-label="`属性 ${index + 1} 名称`" class="input mt-1" maxlength="40" placeholder="如：有效期" /></label>
        <label class="min-w-40 flex-1 text-xs text-gray-500 dark:text-gray-400">属性值（逗号分隔）<input v-model="attribute.values" :aria-label="`属性 ${index + 1} 值`" class="input mt-1" placeholder="如：日卡，周卡" /></label>
        <button type="button" class="btn btn-secondary" :aria-label="`删除属性 ${index + 1}`" @click="attributes.splice(index, 1)">移除</button>
      </div>
      <div class="flex flex-wrap gap-2"><button v-if="attributes.length < 3" type="button" class="btn btn-secondary" @click="attributes.push({ name: '', values: '' })">添加属性</button><button type="button" class="btn btn-secondary" @click="generate">生成规格表</button></div>
      <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">相同属性组合保留原价格。新增组合需填写价格和有效期；移除属性值后重新生成会移除对应规格。</p>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      </div>
    </details>
    <p v-if="native" class="text-sm leading-6 text-gray-500 dark:text-gray-400">DeepSeek 服务支持 1 天或 7 天、2 路或 3 路并发。每种组合的售价独立设置，付款后按所选规格发放 API Key。</p>
    <div class="overflow-hidden rounded-lg border border-gray-200 dark:border-dark-700">
      <div class="hidden grid-cols-[minmax(120px,1.3fr)_1fr_1.4fr_0.8fr_54px_54px] gap-3 bg-gray-50 px-4 py-3 text-xs font-medium text-gray-500 dark:text-gray-400 lg:grid dark:bg-dark-800" :class="!native && 'sku-header-manual'"><span>规格</span><span>售价（元）</span><span>有效期</span><span v-if="native">并发</span><span>默认</span><span>上架</span></div>
      <div v-for="(sku, index) in rows" :key="sku.id" class="grid gap-4 border-t border-gray-100 p-4 first:border-0 sm:grid-cols-2 lg:grid-cols-[minmax(120px,1.3fr)_1fr_1.4fr_0.8fr_54px_54px] lg:items-center lg:gap-3 dark:border-dark-700" :class="!native && 'sku-row-manual'">
        <div class="text-sm font-medium sm:col-span-2 lg:col-span-1"><template v-if="Object.keys(sku.attributes).length"><span v-for="(value, name) in sku.attributes" :key="name" class="mr-2 inline-block"><span class="mb-0.5 block text-xs font-normal text-gray-500 dark:text-gray-400">{{ name }}</span>{{ value }}</span></template><span v-else>默认规格</span></div>
        <label><span class="mb-1 block text-xs text-gray-500 dark:text-gray-400 lg:sr-only">售价（元）</span><input :id="index === 0 ? 'product-price' : undefined" :value="sku.price" :aria-label="`规格 ${index + 1} 售价`" type="number" min="0" max="1000000" step="0.01" required class="input tabular-nums" @input="update(index, { price: Number(($event.target as HTMLInputElement).value) })" /></label>
        <div><span class="mb-1 block text-xs text-gray-500 dark:text-gray-400 lg:sr-only">有效期</span><div class="flex gap-1.5"><input :id="index === 0 ? 'duration-value' : undefined" :value="sku.duration_value" :aria-label="`规格 ${index + 1} 有效期数值`" type="number" min="1" max="3650" step="1" required class="input min-w-0 flex-1 tabular-nums" @input="update(index, { duration_value: Number(($event.target as HTMLInputElement).value) })" /><select :id="index === 0 ? 'duration-unit' : undefined" :value="sku.duration_unit" :aria-label="`规格 ${index + 1} 有效期单位`" class="input w-20 shrink-0" @change="update(index, { duration_unit: ($event.target as HTMLSelectElement).value as ShopSKU['duration_unit'] })"><option value="day">天</option><option value="month">个月</option><option value="year">年</option></select></div></div>
        <label v-if="native"><span class="mb-1 block text-xs text-gray-500 dark:text-gray-400 lg:sr-only">并发</span><select :value="sku.concurrency" :aria-label="`规格 ${index + 1} 并发`" class="input" @change="update(index, { concurrency: Number(($event.target as HTMLSelectElement).value) })"><option :value="0" disabled>请选择</option><option :value="2">2 路</option><option :value="3">3 路</option></select></label>
        <label class="flex min-h-10 items-center gap-2 text-sm"><input type="radio" name="sku-default" :checked="defaultId === sku.id" :aria-label="`规格 ${index + 1} 设为默认`" @change="emit('update:defaultId', sku.id)" /><span class="lg:sr-only">默认</span></label>
        <label class="flex min-h-10 items-center gap-2 text-sm"><input type="checkbox" :checked="sku.enabled" :aria-label="`规格 ${index + 1} 上架`" @change="update(index, { enabled: ($event.target as HTMLInputElement).checked })" /><span class="lg:sr-only">上架</span></label>
      </div>
    </div>
    <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">默认规格用于商品展示。未上架的规格不向买家展示；商品上线时，默认规格必须上架且价格大于 0。</p>
  </div>
</template>
<style scoped>
@media (min-width: 1024px) { .sku-row-manual, .sku-header-manual { grid-template-columns: minmax(120px, 1.3fr) 1fr 1.4fr 54px 54px; } }
</style>
