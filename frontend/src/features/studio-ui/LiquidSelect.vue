<template>
  <div class="lg-select" :class="[{ 'is-compact': compact }, wrapperClass]">
    <select
      class="lg-select-native"
      :class="nativeClass"
      :aria-label="ariaLabel"
      tabindex="-1"
      aria-hidden="true"
      :disabled="disabled"
      :value="modelValue"
      @change="onNativeChange"
    >
      <option v-for="option in options" :key="option.value" :value="option.value" :disabled="option.disabled">
        {{ option.label }}
      </option>
    </select>

    <button
      ref="triggerRef"
      type="button"
      class="lg-select-trigger"
      :disabled="disabled"
      :aria-label="ariaLabel"
      aria-haspopup="listbox"
      :aria-expanded="open"
      :aria-controls="open ? listboxId : undefined"
      :title="selectedOption?.label || placeholder"
      @click="toggle"
      @keydown="onTriggerKeydown"
    >
      <span class="lg-select-value">
        <span v-if="prefix" class="lg-select-prefix">{{ prefix }}</span>
        <span class="lg-select-label">{{ selectedOption?.label ?? placeholder }}</span>
      </span>
      <svg class="lg-select-chevron" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="m7 10 5 5 5-5" />
      </svg>
    </button>

    <Teleport :to="teleportTarget">
      <div
        v-if="open && coords"
        :id="listboxId"
        ref="menuRef"
        class="lg-select-menu"
        role="listbox"
        :aria-label="ariaLabel"
        :data-placement="coords.placement"
        :style="{
          top: `${coords.top}px`,
          left: `${coords.left}px`,
          width: `${coords.width}px`,
          maxHeight: `${coords.maxHeight}px`,
        }"
      >
        <button
          v-for="(option, index) in options"
          :key="option.value"
          type="button"
          role="option"
          class="lg-select-option"
          :class="{ 'is-highlighted': index === highlightedIndex }"
          :aria-selected="option.value === modelValue"
          :disabled="option.disabled"
          @mouseenter="highlightedIndex = index"
          @mousedown.prevent
          @click="choose(option)"
        >
          <span class="lg-select-option-main">
            <span class="lg-select-option-text">{{ option.label }}</span>
          </span>
          <span v-if="option.meta" class="lg-select-option-meta">{{ option.meta }}</span>
          <svg
            v-if="option.value === modelValue"
            class="lg-select-check"
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2.2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M5 13l4 4L19 7" />
          </svg>
        </button>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import '@/styles/liquid-glass.css'

export interface LiquidSelectOption {
  readonly value: string
  readonly label: string
  readonly meta?: string
  readonly disabled?: boolean
}

interface MenuCoords {
  readonly top: number
  readonly left: number
  readonly width: number
  readonly maxHeight: number
  readonly placement: 'bottom' | 'top'
}

const props = withDefaults(defineProps<{
  modelValue: string
  options: readonly LiquidSelectOption[]
  ariaLabel: string
  wrapperClass?: string
  nativeClass?: string
  compact?: boolean
  disabled?: boolean
  placeholder?: string
  prefix?: string
  minMenuWidth?: number
}>(), {
  wrapperClass: '',
  nativeClass: '',
  compact: false,
  disabled: false,
  placeholder: '请选择',
  prefix: '',
  minMenuWidth: 0,
})

const emit = defineEmits<{
  'update:modelValue': [value: string]
  select: [value: string]
}>()

const listboxId = `lg-select-${useId()}`
const open = ref(false)
const highlightedIndex = ref(0)
const coords = ref<MenuCoords | null>(null)
const triggerRef = ref<HTMLButtonElement>()
const menuRef = ref<HTMLElement>()

const selectedOption = computed(() => props.options.find(opt => opt.value === props.modelValue))
const teleportTarget = computed<string | HTMLElement>(() => {
  if (typeof document === 'undefined') return 'body'
  return (triggerRef.value?.closest?.('dialog[open]') as HTMLElement | null) || 'body'
})

const triggerEl = () => triggerRef.value ?? null

function computeCoords() {
  const trigger = triggerEl()
  if (!trigger || typeof window === 'undefined') return
  const rect = trigger.getBoundingClientRect()
  const margin = 8
  const gap = 6
  const maxDesiredHeight = 280
  const roomBelow = Math.max(0, window.innerHeight - rect.bottom - gap - margin)
  const roomAbove = Math.max(0, rect.top - gap - margin)
  const placement: 'bottom' | 'top' = roomBelow < 140 && roomAbove > roomBelow ? 'top' : 'bottom'
  const maxHeight = Math.max(96, Math.min(maxDesiredHeight, Math.floor(placement === 'top' ? roomAbove : roomBelow)))
  const width = Math.max(rect.width, props.minMenuWidth)
  const maxLeft = Math.max(margin, window.innerWidth - width - margin)
  const left = Math.max(margin, Math.min(rect.left, maxLeft))
  const estimatedHeight = Math.min(maxHeight, props.options.length * 36 + 12)
  const top = placement === 'top'
    ? Math.max(margin, rect.top - estimatedHeight - gap)
    : Math.min(window.innerHeight - margin - 96, rect.bottom + gap)
  coords.value = { top, left, width, maxHeight, placement }
}

function close(restoreFocus = false) {
  if (!open.value) return
  open.value = false
  if (restoreFocus) {
    triggerEl()?.focus({ preventScroll: true })
  }
}

function openMenu() {
  if (props.disabled || !props.options.length) return
  const idx = props.options.findIndex(opt => opt.value === props.modelValue)
  highlightedIndex.value = idx >= 0 ? idx : 0
  open.value = true
  computeCoords()
  void nextTick(computeCoords)
}

function toggle() {
  if (open.value) close(false)
  else openMenu()
}

function choose(option: LiquidSelectOption) {
  if (option.disabled) return
  open.value = false
  emit('select', option.value)
  if (option.value !== props.modelValue) {
    emit('update:modelValue', option.value)
  }
  triggerEl()?.focus({ preventScroll: true })
}

function onNativeChange(event: Event) {
  const select = event.target as HTMLSelectElement
  const next = select.value
  // Keep controlled value synced until parent accepts (e.g. if user cancels dirty switch).
  select.value = props.modelValue
  emit('select', next)
  if (next !== props.modelValue) {
    emit('update:modelValue', next)
  }
}

function moveHighlight(delta: number) {
  const len = props.options.length
  if (!len) return
  let next = highlightedIndex.value
  for (let i = 0; i < len; i++) {
    next = (next + delta + len) % len
    if (!props.options[next]?.disabled) {
      highlightedIndex.value = next
      return
    }
  }
}

function onTriggerKeydown(event: KeyboardEvent) {
  if (props.disabled) return
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    if (!open.value) openMenu()
    else moveHighlight(1)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    if (!open.value) openMenu()
    else moveHighlight(-1)
  } else if (event.key === 'Home' && open.value) {
    event.preventDefault()
    highlightedIndex.value = 0
  } else if (event.key === 'End' && open.value) {
    event.preventDefault()
    highlightedIndex.value = Math.max(0, props.options.length - 1)
  } else if (event.key === 'Enter' || event.key === ' ') {
    if (open.value) {
      event.preventDefault()
      event.stopPropagation()
      const picked = props.options[highlightedIndex.value]
      if (picked) choose(picked)
    }
  } else if (event.key === 'Escape' && open.value) {
    event.preventDefault()
    event.stopPropagation()
    close(true)
  }
}

function onOutsidePointerDown(event: PointerEvent) {
  if (!open.value) return
  const target = event.target as Node | null
  if (triggerEl()?.contains(target) || menuRef.value?.contains(target)) return
  close(false)
}

function onGlobalKeydown(event: KeyboardEvent) {
  if (!open.value || event.key !== 'Escape') return
  event.preventDefault()
  event.stopPropagation()
  close(true)
}

function onReposition() {
  if (open.value) computeCoords()
}

watch(open, isOpen => {
  if (typeof window === 'undefined' || typeof document === 'undefined') return
  if (isOpen) {
    document.addEventListener('pointerdown', onOutsidePointerDown, true)
    document.addEventListener('keydown', onGlobalKeydown, true)
    window.addEventListener('resize', onReposition)
    window.addEventListener('scroll', onReposition, true)
  } else {
    document.removeEventListener('pointerdown', onOutsidePointerDown, true)
    document.removeEventListener('keydown', onGlobalKeydown, true)
    window.removeEventListener('resize', onReposition)
    window.removeEventListener('scroll', onReposition, true)
  }
})

// Only close if explicitly disabled; never close when options metadata updates in background (e.g. canvas autosave).
watch(() => props.disabled, isDisabled => {
  if (isDisabled) close(false)
})

onBeforeUnmount(() => {
  if (typeof window === 'undefined' || typeof document === 'undefined') return
  document.removeEventListener('pointerdown', onOutsidePointerDown, true)
  document.removeEventListener('keydown', onGlobalKeydown, true)
  window.removeEventListener('resize', onReposition)
  window.removeEventListener('scroll', onReposition, true)
})

defineExpose({ close, isOpen: open })
</script>
