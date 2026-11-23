<template>
  <dialog ref="dialog" class="vs-model-dialog mp-dialog" aria-labelledby="vs-model-picker-title" @close="onDialogClose" @click="onDialogClick" @keydown="onKeydown">
    <div class="vs-modal-head mp-head">
      <h2 id="vs-model-picker-title" tabindex="-1">选择视频模型</h2>
      <button type="button" class="mp-close vs-modal-close" aria-label="关闭模型选择" @click="close">
        <Icon name="x" size="sm" aria-hidden="true" />
      </button>
    </div>
    <div class="vs-picker-search mp-toolbar">
      <input
        v-model="query"
        class="mp-search-input"
        type="search"
        aria-label="搜索视频模型"
        placeholder="搜索模型名称或能力…"
      />
    </div>
    <div class="vs-picker-layout mp-layout">
      <template v-if="filteredModels.length">
        <div class="vs-picker-list mp-list" :class="{ 'is-stacked-price': stackPrices }" role="listbox" aria-label="视频模型列表">
          <div
            v-for="(item, index) in filteredModels"
            :key="item.id"
            class="mp-option-row"
            role="presentation"
            :data-model-theme="modelTheme(item)"
            :data-selected="draft?.id === item.id ? 'true' : 'false'"
            :data-info-open="infoModel?.id === item.id ? 'true' : 'false'"
          >
            <button
              type="button"
              class="vs-model-option mp-option"
              role="option"
              :aria-selected="draft?.id === item.id"
              :aria-label="optionAccessibleLabel(item)"
              :data-model-index="index"
              :data-model-theme="modelTheme(item)"
              :title="displayLabel(item)"
              @click="select(item)"
            >
              <span class="mp-option-head">
                <span class="mp-option-mark" aria-hidden="true">
                  <svg :viewBox="themeMeta(item).viewBox" fill="currentColor" focusable="false">
                    <path v-for="(d, pathIdx) in themeMeta(item).paths" :key="pathIdx" :d="d" />
                  </svg>
                </span>
                <strong class="mp-option-name">{{ displayLabel(item) }}</strong>
                <span class="mp-option-status">
                  <span v-if="activeModelId === item.id" class="vs-model-current-pill mp-current-tag">当前</span>
                  <span v-if="!isAutomatic(item) && !durationBounds(item)" class="mp-unavailable-tag">时长不可用</span>
                </span>
              </span>
              <span class="mp-option-meta">
                <span class="vs-model-option-price mp-option-price" :data-price-state="optionPriceValue(item) === '价格待配置' ? 'unavailable' : 'ready'">
                  <strong>{{ optionPriceValue(item) }}</strong>
                  <small>{{ optionPriceSub(item) }}</small>
                </span>
              </span>
            </button>
            <div
              class="mp-info-wrap"
              @pointerenter="onInfoEnter(item, $event)"
              @pointerleave="onInfoPointerLeave(item.id, $event)"
            >
              <button
                :ref="el => setInfoBtnRef(item.id, el)"
                type="button"
                class="mp-info-btn"
                :data-model-id="item.id"
                :aria-label="`查看 ${displayLabel(item)} 详情`"
                :aria-expanded="infoModel?.id === item.id"
                :aria-controls="infoModel?.id === item.id ? popoverId : undefined"
                @focus="onInfoFocus(item, $event)"
                @blur="onInfoLeave(item.id, $event)"
                @click.stop="toggleInfoPin(item, $event.currentTarget as HTMLElement | null)"
              >
                ?
              </button>
            </div>
          </div>
        </div>
        <div
          v-if="infoModel"
          :id="popoverId"
          ref="popoverEl"
          role="region"
          :aria-label="`${displayLabel(infoModel)} 模型详情`"
          class="vs-picker-detail mp-detail mp-detail-popover"
          :data-model-theme="modelTheme(infoModel)"
          :style="{ top: `${popoverPos.top}px`, left: `${popoverPos.left}px` }"
          @pointerenter="onPopoverEnter(infoModel, $event)"
          @pointerleave="onInfoPointerLeave(infoModel.id, $event)"
          @blur.capture="onPopoverBlur(infoModel.id, $event)"
        >
          <div class="mp-detail-head">
            <div class="mp-detail-title-row">
              <h3>{{ displayLabel(infoModel) }}</h3>
              <div class="mp-detail-head-actions">
                <span class="vs-model-state-pill mp-state-label" :class="{ 'is-current': activeModelId === infoModel.id }">
                  {{ activeModelId === infoModel.id ? '当前模型' : (draft?.id === infoModel.id ? '候选预览' : '模型详情') }}
                </span>
                <button type="button" class="mp-detail-close" aria-label="收起模型详情" @click="closeInfo(true)">
                  <Icon name="x" size="xs" aria-hidden="true" />
                </button>
              </div>
            </div>
            <p class="vs-picker-subtitle mp-detail-sub">{{ detailSubtitle(infoModel) }}</p>
          </div>
          <dl class="vs-capability-grid mp-spec-list">
            <div class="mp-spec-row">
              <dt>{{ isAutomatic(infoModel) ? '时长模式' : '时长范围' }}</dt>
              <dd>{{ durationCapability(infoModel) }}</dd>
            </div>
            <div class="mp-spec-row">
              <dt>{{ isAspectSizes(infoModel) ? '画幅比例' : '提交尺寸' }}</dt>
              <dd>{{ sizeCapability(infoModel) }}</dd>
            </div>
            <div class="mp-spec-row">
              <dt>素材能力</dt>
              <dd>{{ materialCapability(infoModel) }}</dd>
            </div>
            <div class="mp-spec-row">
              <dt>可选参数</dt>
              <dd>{{ optionCapability(infoModel) }}</dd>
            </div>
            <div class="mp-spec-row">
              <dt>计价参考</dt>
              <dd>{{ priceCapability(infoModel) }}</dd>
            </div>
          </dl>
        </div>
      </template>
      <div v-else class="vs-picker-empty mp-empty" role="status">
        <strong>{{ models.length ? '没有匹配的视频模型' : '当前暂无可用视频模型' }}</strong>
        <p>{{ models.length ? '请尝试调整或清空搜索关键词。' : '请联系管理员确认视频工作台权限与有效报价配置。' }}</p>
      </div>
    </div>
    <div class="vs-modal-foot mp-foot">
      <span v-if="!mobile" class="vs-picker-kbd mp-kbd" aria-hidden="true">
        <kbd>↑</kbd><kbd>↓</kbd> 预览 · <kbd>Enter</kbd> 应用 · <kbd>Esc</kbd> 取消
      </span>
      <div class="vs-model-dialog-actions mp-foot-actions">
        <button type="button" class="vs-button mp-btn-cancel" @click="close">取消</button>
        <button type="button" class="vs-primary mp-btn-confirm" aria-label="使用此模型" :disabled="!draft" @click="confirm">使用此模型</button>
      </div>
    </div>
  </dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, shallowRef, watch } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import '@/styles/model-picker.css'
import { getModelThemeMeta, resolveVideoModelTheme } from '@/features/studio-ui/modelThemes'
import { videoDurationMode, videoModelDisplayName, type VideoModel, type VideoReferenceKind } from './api'

const props = defineProps<{ models: VideoModel[]; mobile?: boolean }>()
const emit = defineEmits<{ choose: [model: VideoModel] }>()
const dialog = ref<HTMLDialogElement>()
const popoverEl = ref<HTMLElement>()
const draft = shallowRef<VideoModel | null>(null)
const infoId = ref<string | null>(null)
const infoPinned = ref(false)
const popoverPos = ref<{ top: number; left: number }>({ top: 72, left: 16 })
const infoBtns = new Map<string, HTMLButtonElement>()
const popoverId = `vs-model-detail-${Math.random().toString(36).slice(2, 8)}`
const activeModelId = ref('')
const query = ref('')
let opener: HTMLElement | undefined
let suppressFocusOpen = false

const infoModel = computed(() =>
  infoId.value ? filteredModels.value.find(item => item.id === infoId.value) ?? null : null,
)

function setInfoBtnRef(id: string, el: unknown) {
  if (el instanceof HTMLButtonElement) infoBtns.set(id, el)
  else infoBtns.delete(id)
}

function computePopoverPos(anchor: HTMLElement | null) {
  if (!anchor || typeof window === 'undefined') return { top: 72, left: 16 }
  const rect = anchor.getBoundingClientRect()
  if (rect.width === 0 && rect.height === 0) return { top: 72, left: 16 }
  const margin = 12
  const popWidth = Math.min(352, window.innerWidth - margin * 2)
  const popHeight = 320
  const preferRight = rect.right + 8 + popWidth <= window.innerWidth - margin
  const rawLeft = preferRight ? rect.right + 8 : rect.right - popWidth
  const left = Math.max(margin, Math.min(rawLeft, window.innerWidth - popWidth - margin))
  const rawTop = rect.bottom + 6 + popHeight <= window.innerHeight - margin
    ? rect.bottom + 6
    : Math.max(margin, rect.top - popHeight - 6)
  const top = Math.max(margin, Math.min(rawTop, window.innerHeight - Math.min(popHeight, window.innerHeight - margin * 2) - margin))
  return { top, left }
}

function closeInfo(restore = false) {
  const prevId = infoId.value
  infoId.value = null
  infoPinned.value = false
  if (restore && prevId) {
    suppressFocusOpen = true
    infoBtns.get(prevId)?.focus()
    suppressFocusOpen = false
  }
}

function openInfo(model: VideoModel, anchor: HTMLElement | null) {
  popoverPos.value = computePopoverPos(anchor)
  infoId.value = model.id
}

function onInfoFocus(model: VideoModel, event: FocusEvent) {
  const anchor = event.currentTarget
  if (suppressFocusOpen || !(anchor instanceof HTMLElement) || !anchor.matches(':focus-visible')) return
  openInfo(model, anchor)
}

function onInfoEnter(model: VideoModel, event: PointerEvent) {
  if (event.pointerType !== 'mouse') return
  if (infoPinned.value && infoId.value !== model.id) return
  openInfo(model, infoBtns.get(model.id) ?? null)
}

function onPopoverEnter(model: VideoModel, event: PointerEvent) {
  if (event.pointerType === 'mouse' && !infoPinned.value) infoId.value = model.id
}

function onInfoPointerLeave(id: string, event: PointerEvent) {
  if (event.pointerType === 'mouse') onInfoLeave(id, event)
}

function toggleInfoPin(model: VideoModel, anchor: HTMLElement | null) {
  if (infoPinned.value && infoId.value === model.id) {
    closeInfo()
    return
  }
  popoverPos.value = computePopoverPos(anchor)
  infoId.value = model.id
  infoPinned.value = true
}

function onInfoLeave(id: string, event: MouseEvent | FocusEvent) {
  if (infoPinned.value) return
  const next = event.relatedTarget instanceof Node ? event.relatedTarget : null
  if (next && (popoverEl.value?.contains(next) || infoBtns.get(id)?.contains(next))) return
  if (infoId.value === id) infoId.value = null
}

function onPopoverBlur(id: string, event: FocusEvent) {
  if (infoPinned.value) return
  const next = event.relatedTarget instanceof Node ? event.relatedTarget : null
  if (next && (popoverEl.value?.contains(next) || infoBtns.get(id)?.contains(next))) return
  if (infoId.value === id) infoId.value = null
}

const isAutomatic = (model: VideoModel) => videoDurationMode(model) === 'automatic'
const isAspectSizes = (model: VideoModel) =>
  model.sizes?.length ? model.sizes.every(value => value.includes(':')) : isAutomatic(model)
const displayLabel = (model: VideoModel) => videoModelDisplayName(model)
const formatSize = (value: string) => value.replace(/x/i, ' × ')
const modelTheme = (model: VideoModel) => resolveVideoModelTheme(model.provider, model.id)
const themeMeta = (model: VideoModel) => getModelThemeMeta(modelTheme(model))

function optionPriceValue(model: VideoModel) {
  const value = model.quotes?.[model.default_seconds]
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) return '价格待配置'
  return `${value}`
}
function optionPriceSub(model: VideoModel) {
  const value = model.quotes?.[model.default_seconds]
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) {
    return isAutomatic(model) ? '自动档' : `${model.default_seconds}秒档`
  }
  return isAutomatic(model) ? `${model.currency} · 默认档` : `${model.currency} · ${model.default_seconds}秒`
}

function estimateVisualUnits(text: string): number {
  let units = 0
  for (const ch of text) {
    units += /[\u2e80-\u9fff\uf900-\ufaff\uff00-\uffef]/.test(ch) ? 1.75 : 1
  }
  return units
}

function durationBounds(model: VideoModel): { min: number; max: number } | null {
  if (isAutomatic(model)) return null
  let min = model.min_seconds
  let max = model.max_seconds
  if (model.id === 'minimax/h3-fast') { min = Math.max(5, min); max = Math.min(14, max) }
  else if (model.id === 'minimax/h3') { min = Math.max(4, min); max = Math.min(15, max) }
  return max >= min ? { min, max } : null
}
function durationBadge(model: VideoModel) {
  if (isAutomatic(model)) return '时长自动'
  const bounds = durationBounds(model)
  return bounds ? `${bounds.min}–${bounds.max} 秒` : '时长不可用'
}
function optionCapabilitySummary(model: VideoModel) {
  const parts = [durationBadge(model)]
  if (model.supports_frames && model.max_references > 0) parts.push('首尾帧/参考')
  else if (model.supports_frames) parts.push('首尾帧')
  else if (model.max_references > 0) parts.push(`最多 ${Math.min(10, model.max_references)} 个素材`)
  else parts.push('纯文字')
  return parts.join(' · ')
}
function detailSubtitle(model: VideoModel) {
  const parts = [durationBadge(model)]
  if (model.supports_frames) parts.push('支持首尾帧')
  if (model.max_references > 0) parts.push(`最多 ${Math.min(10, model.max_references)} 个参考素材`)
  if (model.supports_seed) parts.push('支持 Seed')
  return parts.join(' · ')
}
function optionAccessibleLabel(model: VideoModel) {
  const currentText = activeModelId.value === model.id ? '当前模型，' : ''
  return `${displayLabel(model)}，${currentText}${optionCapabilitySummary(model)}，${optionPriceValue(model)} ${optionPriceSub(model)}`
}
function durationCapability(model: VideoModel) {
  if (isAutomatic(model)) return '自动（最终长度以成片为准）'
  const bounds = durationBounds(model)
  return bounds ? `${bounds.min}–${bounds.max} 秒（可调）` : '未提供有效时长范围'
}
function sizeCapability(model: VideoModel) {
  if (!model.sizes?.length) return '自动'
  return model.sizes.map(formatSize).join(' · ')
}
function materialCapability(model: VideoModel) {
  const capabilities: string[] = []
  if (model.supports_frames) capabilities.push('首帧 / 可选尾帧（图片）')
  if (model.max_references > 0) {
    const kinds: readonly VideoReferenceKind[] = model.reference_kinds?.length ? model.reference_kinds : ['image']
    const names: Record<VideoReferenceKind, string> = { image: '图片', video: '视频', audio: '音频', file: 'PDF' }
    capabilities.push(`有序参考（${kinds.map(kind => names[kind]).join('/')}，最多 ${Math.min(10, model.max_references)} 个）`)
  }
  return capabilities.join('；') || '纯文字生成'
}
function optionCapability(model: VideoModel) {
  const options: string[] = []
  if (model.supports_seed) options.push('Seed')
  if (model.muse_options) options.push('原生画布意图', '画质文字意图', '任务等待上限', '补充描述')
  return options.join('、') || '无额外可选参数'
}
function priceCapability(model: VideoModel) {
  const value = model.quotes?.[model.default_seconds]
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) return '未下发有效报价'
  if (isAutomatic(model)) return `${value} ${model.currency}（按默认档报价，最终长度以成片为准）`
  return `默认 ${model.default_seconds} 秒：${value} ${model.currency}（按请求秒数报价，实际结算以核实秒数为准）`
}

const filteredModels = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return props.models
  return props.models.filter(item => {
    const haystack = [
      displayLabel(item),
      item.id,
      durationBadge(item),
      materialCapability(item),
      optionCapability(item),
    ].join(' ').toLowerCase()
    return haystack.includes(q)
  })
})

const stackPrices = computed(() =>
  filteredModels.value.some(item =>
    estimateVisualUnits(`${optionPriceValue(item)} ${optionPriceSub(item)}`) > 19,
  ),
)

watch(filteredModels, list => {
  if (infoId.value && !list.some(item => item.id === infoId.value)) closeInfo()
  if (!list.length) {
    draft.value = null
    return
  }
  if (!draft.value || !list.some(item => item.id === draft.value?.id)) {
    draft.value = list.find(item => item.id === activeModelId.value) || list[0] || null
  }
})

function select(model: VideoModel) {
  draft.value = model
  closeInfo()
}
function close() {
  closeInfo()
  if (dialog.value?.open) dialog.value.close()
}
function onDialogClick(event: MouseEvent) {
  if (event.target === event.currentTarget) {
    close()
    return
  }
  const target = event.target instanceof Node ? event.target : null
  if (infoId.value && target && !popoverEl.value?.contains(target) && !(target instanceof Element && target.closest('.mp-info-wrap'))) {
    closeInfo()
  }
}
function onDialogClose() {
  closeInfo()
  restoreFocus()
}
function restoreFocus() {
  const trigger = opener; opener = undefined
  if (trigger?.isConnected) trigger.focus()
}
async function open(model: VideoModel | null, trigger: HTMLElement) {
  opener = trigger
  query.value = ''
  closeInfo()
  activeModelId.value = model?.id || ''
  draft.value = props.models.find(item => item.id === model?.id) || props.models[0] || null
  if (!dialog.value?.open) dialog.value?.showModal()
  await nextTick()
  dialog.value?.querySelector<HTMLElement>('input[type="search"]')?.focus()
}
function confirm() {
  const chosen = draft.value
  if (!chosen) return
  close()
  emit('choose', chosen)
}
function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    if (infoId.value) {
      closeInfo(true)
      return
    }
    close()
    return
  }
  const target = event.target instanceof HTMLElement ? event.target : null
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    if (!filteredModels.value.length) return
    if (target && target.tagName !== 'INPUT' && target.getAttribute('role') !== 'option') return
    event.preventDefault()
    const index = filteredModels.value.findIndex(item => item.id === draft.value?.id)
    const offset = event.key === 'ArrowDown' ? 1 : -1
    const next = filteredModels.value[(index + offset + filteredModels.value.length) % filteredModels.value.length]
    if (next) {
      select(next)
      void nextTick(() => dialog.value?.querySelector<HTMLElement>('[aria-selected=true]')?.focus())
    }
    return
  }
  if ((event.key === 'Home' || event.key === 'End') && target?.getAttribute('role') === 'option') {
    if (!filteredModels.value.length) return
    event.preventDefault()
    const next = event.key === 'Home' ? filteredModels.value[0] : filteredModels.value[filteredModels.value.length - 1]
    if (next) {
      select(next)
      void nextTick(() => dialog.value?.querySelector<HTMLElement>('[aria-selected=true]')?.focus())
    }
    return
  }
  if (event.key === 'Enter') {
    if (target?.getAttribute('role') === 'option') {
      event.preventDefault()
      event.stopPropagation()
      confirm()
    }
  }
}
defineExpose({ open, close })
</script>
