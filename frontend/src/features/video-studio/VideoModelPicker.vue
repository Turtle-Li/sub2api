<template>
  <dialog ref="dialog" class="vs-model-dialog" aria-labelledby="vs-model-picker-title" @close="restoreFocus" @keydown="onKeydown">
    <div class="vs-modal-head">
      <div><h2 id="vs-model-picker-title" tabindex="-1">选择视频模型</h2><p>能力与报价来自当前登录身份的可用目录（非 API Key 或分组选择）</p></div>
      <button type="button" class="vs-icon" aria-label="关闭模型选择" @click="close"><Icon name="x" size="sm" aria-hidden="true" /></button>
    </div>
    <div class="vs-picker-layout">
      <div class="vs-picker-list" role="listbox" aria-label="视频模型列表">
        <button v-for="(item, index) in models" :key="item.id" type="button" class="vs-model-option" role="option" :aria-selected="draft?.id === item.id"
          :data-model-index="index" @click="select(item)">
          <span class="vs-picker-badge" aria-hidden="true">{{ item.label.slice(0, 1) }}</span>
          <span class="vs-model-option-copy">
            <strong>{{ item.label }}</strong>
            <small>{{ providerLabel(item) }} · {{ durationBadge(item) }}</small>
          </span>
        </button>
      </div>
      <div v-if="draft" class="vs-picker-detail">
        <div class="vs-picker-title">
          <span class="vs-picker-badge" aria-hidden="true">{{ draft.label.slice(0, 1) }}</span>
          <div><h3>{{ draft.label }}</h3><p>{{ providerLabel(draft) }} · 服务渠道由当前登录身份路由</p></div>
        </div>
        <dl class="vs-capability-grid">
          <div><dt>{{ isAutomatic(draft) ? '时长模式' : '时长范围' }}</dt><dd>{{ durationCapability(draft) }}</dd></div>
          <div><dt>{{ draft.provider === 'muse' ? '画幅比例' : '提交尺寸' }}</dt><dd>{{ sizeCapability(draft) }}</dd></div>
          <div class="is-wide"><dt>素材能力</dt><dd>{{ materialCapability(draft) }}</dd></div>
          <div class="is-wide"><dt>可配扩展参数</dt><dd>{{ optionCapability(draft) }}</dd></div>
          <div class="is-wide"><dt>计价参考</dt><dd>{{ priceCapability(draft) }}</dd></div>
          <div class="is-wide"><dt>路由身份</dt><dd>当前登录会话 · 非 API Key / 分组选择</dd></div>
        </dl>
      </div>
    </div>
    <div class="vs-modal-foot">
      <span v-if="!mobile" class="vs-picker-kbd" aria-hidden="true">
        <kbd>↑</kbd><kbd>↓</kbd> 切换 · <kbd>Enter</kbd> 确认 · <kbd>Esc</kbd> 关闭
      </span>
      <div class="vs-actions">
        <button type="button" class="vs-button" @click="close">取消</button>
        <button type="button" class="vs-primary" :disabled="!draft" @click="confirm">使用此模型与渠道</button>
      </div>
    </div>
  </dialog>
</template>

<script setup lang="ts">
import { nextTick, ref, shallowRef } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { videoDurationMode, type VideoModel, type VideoReferenceKind } from './api'

const props = defineProps<{ models: VideoModel[]; mobile?: boolean }>()
const emit = defineEmits<{ choose: [model: VideoModel] }>()
const dialog = ref<HTMLDialogElement>()
const draft = shallowRef<VideoModel | null>(null)
let opener: HTMLElement | undefined

const isAutomatic = (model: VideoModel) => videoDurationMode(model) === 'automatic'
const providerLabel = (model: VideoModel) => model.provider === 'muse' ? 'Muse' : 'GoodPut H3'
const formatSize = (value: string) => value.replace(/x/i, ' × ')

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
function durationCapability(model: VideoModel) {
  if (isAutomatic(model)) return '自动（最终长度以成片为准）'
  const bounds = durationBounds(model)
  return bounds ? `${bounds.min}–${bounds.max} 秒（可调）` : '未提供有效时长范围'
}
function sizeCapability(model: VideoModel) {
  if (!model.sizes?.length) return '渠道自动'
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
  return options.join('、') || '无额外可配参数'
}
function priceCapability(model: VideoModel) {
  const value = model.quotes?.[model.default_seconds]
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) return '未下发有效报价'
  if (isAutomatic(model)) return `${value} ${model.currency}（按当前渠道默认档报价，最终长度以成片为准）`
  return `默认 ${model.default_seconds} 秒：${value} ${model.currency}（按请求秒数报价）`
}

function select(model: VideoModel) { draft.value = model }
function close() { if (dialog.value?.open) dialog.value.close() }
function restoreFocus() {
  const trigger = opener; opener = undefined
  if (trigger?.isConnected) trigger.focus()
}
async function open(model: VideoModel | null, trigger: HTMLElement) {
  opener = trigger
  draft.value = props.models.find(item => item.id === model?.id) || props.models[0] || null
  if (!dialog.value?.open) dialog.value?.showModal()
  await nextTick()
  dialog.value?.querySelector<HTMLElement>('[aria-selected=true]')?.focus()
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
    close()
    return
  }
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    if (!props.models.length) return
    event.preventDefault()
    const index = props.models.findIndex(item => item.id === draft.value?.id)
    const offset = event.key === 'ArrowDown' ? 1 : -1
    const next = props.models[(index + offset + props.models.length) % props.models.length]
    if (next) {
      select(next)
      void nextTick(() => dialog.value?.querySelector<HTMLElement>('[aria-selected=true]')?.focus())
    }
    return
  }
  if (event.key === 'Enter') {
    const target = event.target instanceof HTMLElement ? event.target : null
    // Only confirm on Enter when focused on a listbox option or non-button container;
    // never hijack Enter on Cancel, Close, or other action buttons.
    if (target?.getAttribute('role') === 'option') {
      event.preventDefault()
      confirm()
    }
  }
}
defineExpose({ open, close })
</script>
