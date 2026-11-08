<script setup lang="ts">
import { computed } from 'vue'
import type { ShopSKU } from '@/types/shop'
const props = defineProps<{ skus: ShopSKU[]; modelValue: string; disabled: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [id: string] }>()
const available = computed(() => props.skus.filter(sku => sku.enabled))
const selected = computed(() => available.value.find(sku => sku.id === props.modelValue))
const attributes = computed(() => Object.keys(available.value[0]?.attributes ?? {}).map(name => ({ name, values: [...new Set(available.value.map(sku => sku.attributes[name]))] })))
function choose(name: string, value: string) {
  const candidates = available.value.filter(sku => sku.attributes[name] === value)
  const score = (sku: ShopSKU) => Object.entries(selected.value?.attributes ?? {}).filter(([key, entry]) => key !== name && sku.attributes[key] === entry).length
  const next = candidates.sort((a, b) => score(b) - score(a))[0]
  if (next) emit('update:modelValue', next.id)
}
</script>
<template>
  <fieldset v-for="attribute in attributes" :key="attribute.name" class="min-w-0" :disabled="disabled">
    <legend class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ attribute.name }}</legend>
    <div class="mt-2 flex flex-wrap gap-2"><label v-for="value in attribute.values" :key="value" :class="['flex min-h-11 cursor-pointer items-center gap-2 rounded-lg border px-3 py-2 text-sm focus-within:ring-2 focus-within:ring-primary-500', selected?.attributes[attribute.name] === value ? 'border-primary-500 bg-primary-50 dark:border-primary-600 dark:bg-primary-950' : 'border-gray-200 hover:border-gray-400 dark:border-gray-700', disabled && 'cursor-not-allowed opacity-70']"><input type="radio" :name="`sku-${attribute.name}`" :value="value" :checked="selected?.attributes[attribute.name] === value" class="h-4 w-4 accent-primary-600" @change="choose(attribute.name, value)" /><span>{{ value }}</span></label></div>
  </fieldset>
</template>
