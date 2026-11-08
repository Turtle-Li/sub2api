<script setup lang="ts">
import { nextTick } from 'vue'
const props = defineProps<{ modelValue: string; label: string; id: string; options: { value: string; label: string }[] }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
async function navigate(event: KeyboardEvent, index: number) {
  const offsets: Record<string, number> = { ArrowRight: 1, ArrowLeft: -1, Home: -index, End: props.options.length - 1 - index }
  if (!(event.key in offsets)) return
  event.preventDefault()
  const option = props.options[(index + offsets[event.key] + props.options.length) % props.options.length]
  if (!option) return
  emit('update:modelValue', option.value)
  await nextTick()
  document.getElementById(`${props.id}-${option.value}`)?.focus()
}
</script>
<template>
  <div class="max-w-full overflow-x-auto">
    <div role="tablist" :aria-label="label" class="inline-flex whitespace-nowrap rounded-lg bg-gray-100 p-1 dark:bg-dark-800">
      <button v-for="(option, index) in options" :id="`${id}-${option.value}`" :key="option.value" type="button" role="tab" :aria-selected="modelValue === option.value" :aria-controls="`${id}-panel-${option.value}`" :tabindex="modelValue === option.value ? 0 : -1" :class="['min-h-10 rounded-md px-4 py-2 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500', modelValue === option.value ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white' : 'text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white']" @click="emit('update:modelValue', option.value)" @keydown="navigate($event, index)">{{ option.label }}</button>
    </div>
  </div>
</template>
