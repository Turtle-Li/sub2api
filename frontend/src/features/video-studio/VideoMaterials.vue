<template>
  <fieldset class="vs-materials" :disabled="disabled">
    <legend>{{ mode === 'frames' ? '首尾帧素材' : refNoun() }} <small>选填</small></legend>
    <div v-if="canFrame(model)" class="vs-segment" role="group" aria-label="素材用途">
      <button v-for="choice in modes" :key="choice.value" type="button" :aria-pressed="mode === choice.value"
        :disabled="!!change || inspecting" @click="requestChange(model, choice.value)">{{ choice.label }}</button>
    </div>
    <p class="vs-hint">{{ mode === 'frames' ? '用首帧确定开场；想指定结尾时，再加尾帧。' : '按当前顺序参考素材内容，不锁定首帧或尾帧。' }}</p>
    <div v-if="change" class="vs-notice" role="group" aria-label="确认更改素材用途">
      <strong>更改素材用途？</strong>
      <p>{{ conversionText }}。素材不会被静默丢弃。</p>
      <div class="vs-actions">
        <button ref="confirmButton" type="button" class="vs-button" @click="apply(change.model, change.mode)">确认改作{{ change.mode === 'frames' ? '首尾帧' : refNoun(change.model) }}</button>
        <button v-if="change.keepReferences" type="button" class="vs-text" @click="apply(change.model, 'refs')">沿用{{ refNoun(change.model) }}</button>
        <button type="button" class="vs-text" @click="closeChange">取消切换</button>
      </div>
    </div>
    <div class="vs-slots" :class="{ 'vs-single-slot': slotCount === 1 }">
      <div v-for="(_, index) in slotCount" :key="index" class="vs-slot">
        <label class="vs-drop" :class="{ 'is-filled': slots[index], 'is-disabled': !canPick(index) }">
          <input type="file" class="vs-sr" :accept="accept" :aria-label="`${slots[index] ? '替换' : '上传'}${role(index)}`"
            :disabled="!canPick(index)" @change="pick(index, $event)" />
          <img v-if="slots[index]?.preview" :src="slots[index]?.preview" alt="" />
          <Icon v-else :name="slots[index] ? 'link' : 'plus'" size="sm" aria-hidden="true" />
          <strong>{{ role(index) }}</strong>
          <span :title="slots[index]?.label">{{ slots[index]?.label || (index && !slots[0] ? `先添加${role(0)}` : mode === 'frames' ? '选择图片' : '选择本地文件') }}</span>
          <small v-if="slots[index]">{{ typeLabel(slots[index]!) }}</small>
        </label>
        <button v-if="slots[index]" type="button" class="vs-remove" :disabled="!!change || inspecting" :aria-label="`移除${role(index)}`"
          @click="remove(index)"><Icon name="x" size="xs" aria-hidden="true" /></button>
        <div v-if="mode === 'refs' && slots[index] && assets.length > 1" class="vs-reference-order">
          <button type="button" class="vs-order-btn" :disabled="index === 0 || !!change || inspecting" :aria-label="`前移${role(index)}`" @click="move(index, -1)">↑</button>
          <button type="button" class="vs-order-btn" :disabled="index === assets.length - 1 || !!change || inspecting" :aria-label="`后移${role(index)}`" @click="move(index, 1)">↓</button>
        </div>
      </div>
    </div>
    <button v-if="mode === 'refs' && assets.length === 2" type="button" class="vs-text" :disabled="!!change || inspecting" @click="swap">
      <Icon name="swap" size="xs" aria-hidden="true" /> 交换{{ refNoun() }}顺序
    </button>
    <p class="vs-hint">{{ formatHint }}，每个不超过 8 MiB；最多 {{ mode === 'frames' ? 2 : refLimit(model) }} 个。<template v-if="model.provider === 'goodput'">H3 编码后的整份请求限 8 MiB。</template></p>
    <p v-if="inspecting" role="status" class="vs-hint">正在检查素材…</p>
    <div v-if="canvasSourceBrokenReason" role="alert" class="vs-notice vs-canvas-source-alert">
      <strong>画布分支约定来源已变动 · 已暂停按原分支提交</strong>
      <p>{{ canvasSourceBrokenReason }}</p>
      <div class="vs-actions">
        <button v-if="canRestoreCanvasSource" type="button" class="vs-button" @click="restoreCanvasSource">恢复约定素材</button>
        <button type="button" class="vs-text" @click="emit('detachCanvasDraft')">脱离画布另建任务</button>
      </div>
    </div>
    <p v-if="invalidRoles" role="alert" class="vs-error">{{ invalidRoles }}</p>
    <div v-if="fileError" role="alert" class="vs-error">{{ fileError }} <button type="button" class="vs-text" @click="fileError = ''">撤销这次选择</button></div>
  </fieldset>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { clearImage, readImage, pendingImage } from '@/features/video-studio/source'
import { validateVideoImage, validateVideoReference, videoReferenceTypes, VIDEO_IMAGE_MAX_BYTES as maxFile, type VideoModel, type VideoReferenceKind, type VideoSubmission } from '@/features/video-studio/api'

type Source = NonNullable<ReturnType<typeof pendingImage>>
type Asset = { label: string; file: File; preview: string; kind: VideoReferenceKind } | { label: string; source: Source; preview?: never; kind: 'image' }
type Mode = 'frames' | 'refs'
type Change = { model: VideoModel; mode: Mode; keepReferences: boolean }
const props = withDefaults(defineProps<{ model: VideoModel; userId: number; source: Source | null; disabled: boolean; tailFrame?: boolean; canvasSourceRequired?: boolean }>(), {
  tailFrame: false,
  canvasSourceRequired: false,
})
const emit = defineEmits<{ choose: [model: VideoModel]; detachCanvasDraft: [] }>()
const canFrame = (m: VideoModel) => m.provider === 'goodput' && m.supports_frames
const refLimit = (m: VideoModel) => Math.max(0, Math.min(10, m.max_references))
const modelRefKinds = (m: VideoModel): VideoReferenceKind[] => m.reference_kinds?.length ? [...new Set(m.reference_kinds)] : ['image']
const refNoun = (m: VideoModel = props.model) => {
  const kinds = modelRefKinds(m)
  return kinds.length === 1 && kinds[0] === 'image' ? '参考图' : '参考素材'
}
const referenceKinds = computed(() => modelRefKinds(props.model))
const allowedTypes = computed(() => referenceKinds.value.flatMap(kind => videoReferenceTypes[kind]))
const accept = computed(() => mode.value === 'frames' ? videoReferenceTypes.image.join(',') : allowedTypes.value.join(','))
const formatHint = computed(() => mode.value === 'frames' ? 'PNG、JPEG、WebP' : referenceKinds.value.map(kind => ({ image: 'PNG/JPEG/WebP', video: 'MP4/WebM', audio: 'MP3/WAV', file: 'PDF' } as const)[kind]).join('、'))
const modes = computed(() => [{ value: 'frames', label: '首尾帧' }, { value: 'refs', label: refNoun() }] as const)
const mode = ref<Mode>(canFrame(props.model) ? 'frames' : 'refs')
const role = (i: number, m: Mode = mode.value, target: VideoModel = props.model) => m === 'frames' ? (i === 0 ? '首帧' : '尾帧') : `${refNoun(target)} ${i + 1}`
const slots = shallowRef<(Asset | null)[]>(props.source?.userId === props.userId ? [{ source: props.source, label: props.source.label, kind: 'image' }] : [])
const assets = computed(() => slots.value.filter((x): x is Asset => x !== null))
const slotCount = computed(() => mode.value === 'frames' ? 2 : Math.max(Math.min(2, refLimit(props.model)), Math.min(refLimit(props.model), assets.value.length + 1)))
const change = shallowRef<Change | null>(null)
const confirmButton = ref<HTMLButtonElement>()
let conversionOpener: HTMLElement | null = null
const fileError = ref('')
const spentSource = ref(false)
const inspecting = ref(false)
const lifetime = new AbortController()

const invalidRoles = computed(() => {
  const spentAsset = assets.value.find(a => 'source' in a)
  if (spentSource.value && spentAsset && 'source' in spentAsset) {
    return props.tailFrame
      ? '固定成片的尾帧授权已结束或提取未成功。请返回画布重新确认或替换图片，本次不会无图生成。'
      : '原图授权已结束。请替换或移除这张图，或回到生图工作台重新带入。'
  }
  if (mode.value === 'frames' && !canFrame(props.model)) return '此模型已不支持首尾帧，请切换素材用途。'
  if (mode.value === 'frames' && slots.value[1] && !slots.value[0]) return '尾帧需要搭配首帧，请补上首帧或移除尾帧。'
  if (mode.value === 'frames' && (assets.value.length > 2 || assets.value.some(a => a.kind !== 'image'))) return '首尾帧仅支持最多 2 张图片，请先移除非图片或多余素材。'
  if (mode.value === 'refs' && assets.value.length > refLimit(props.model)) return '参考素材超过当前模型上限，请先移除多余素材。'
  if (mode.value === 'refs' && assets.value.some(asset => !referenceKinds.value.includes(asset.kind))) return '当前模型不支持其中一种素材类型，请移除后再提交。'
  return ''
})
const primarySlotAsset = computed(() => (mode.value === 'frames' ? slots.value[0] : assets.value[0]) ?? null)
const canvasSourceIntact = computed(() => {
  if (!props.canvasSourceRequired) return true
  if (!props.source || props.source.userId !== props.userId || spentSource.value) return false
  if (!canFrame(props.model) && refLimit(props.model) < 1) return false
  const first = primarySlotAsset.value
  return Boolean(first && 'source' in first && first.source === props.source)
})
const canRestoreCanvasSource = computed(() =>
  Boolean(
    props.canvasSourceRequired &&
      !canvasSourceIntact.value &&
      props.source &&
      props.source.userId === props.userId &&
      !spentSource.value &&
      pendingImage(props.userId) === props.source &&
      (canFrame(props.model) || refLimit(props.model) >= 1),
  ),
)
const canvasSourceBrokenReason = computed(() => {
  if (!props.canvasSourceRequired || canvasSourceIntact.value) return ''
  const noun = props.tailFrame ? '上一镜真实尾帧 PNG' : '画布关联原图'
  if (!canFrame(props.model) && refLimit(props.model) < 1) {
    return `当前模型不支持携带图片素材，无法按该分支约定的「${noun}」提交。请切换支持图片的模型，或脱离画布另建任务。`
  }
  const stillSomewhere = assets.value.some((a, idx) => idx > 0 && 'source' in a && a.source === props.source)
  if (stillSomewhere) {
    return `该分支约定「${noun}」必须位于第 1 个素材槽位。调换顺序后不能按原续镜身份绑定，请恢复首位或脱离画布另建任务。`
  }
  return `你已移除或替换了该分支约定的「${noun}」。为避免付费生成无图/错源结果却记入原分支，请恢复约定素材、返回画布改源，或脱离画布另建独立任务。`
})
const ready = computed(() => !change.value && !invalidRoles.value && !fileError.value && !inspecting.value && canvasSourceIntact.value)
const conversionText = computed(() => {
  if (!change.value) return ''
  let position = 0
  return slots.value.flatMap((a, i) => a ? [`${role(i)}「${a.label}」→ ${role(position++, change.value!.mode, change.value!.model)}`] : []).join('；')
})
function typeLabel(asset: Asset) {
  if ('source' in asset && props.tailFrame) {
    return '尾帧 PNG · 确认时提取'
  }
  return ({ image: '图片', video: '视频', audio: '音频', file: 'PDF 文档' } as const)[asset.kind]
}
function closeChange() {
  change.value = null
  const trigger = conversionOpener; conversionOpener = null
  if (trigger) void nextTick(() => { if (trigger.isConnected) trigger.focus() })
}
function apply(model: VideoModel, next: Mode) {
  if (mode.value !== next) slots.value = [...assets.value]
  mode.value = next
  fileError.value = ''
  closeChange()
  emit('choose', model)
}
function fitsRefs(target: VideoModel) {
  const kinds = modelRefKinds(target)
  return assets.value.length <= refLimit(target) && assets.value.every(asset => kinds.includes(asset.kind))
}
function fitsFrames(target: VideoModel) {
  return canFrame(target) && assets.value.length <= 2 && assets.value.every(asset => asset.kind === 'image')
}
function openConfirm(target: VideoModel, next: Mode, keepReferences: boolean) {
  if (!change.value) conversionOpener = document.activeElement instanceof HTMLElement ? document.activeElement : null
  change.value = { model: target, mode: next, keepReferences }
  void nextTick(() => confirmButton.value?.focus())
}
function requestChange(target: VideoModel, next: Mode) {
  if (inspecting.value || props.disabled) return
  if (next === 'frames') {
    if (!canFrame(target)) { fileError.value = '目标模型不支持首尾帧，未切换。'; return }
    if (assets.value.some(asset => asset.kind !== 'image')) { fileError.value = '首尾帧仅支持图片，包含非图片素材，请先移除非图片素材。'; return }
    if (assets.value.length > 2) { fileError.value = '首尾帧最多支持 2 张图片，请先移除多余素材。'; return }
  }
  if (next === 'refs') {
    const kinds = modelRefKinds(target)
    if (assets.value.some(asset => !kinds.includes(asset.kind))) { fileError.value = '目标模型不支持当前部分素材类型，未切换。请先移除不兼容素材。'; return }
    if (assets.value.length > refLimit(target)) { fileError.value = '目标模型容纳不了全部参考素材，未切换。请先移除多余素材。'; return }
  }
  if (next === mode.value || !assets.value.length) { apply(target, next); return }
  openConfirm(target, next, target.id !== props.model.id && mode.value === 'refs' && canFrame(target) && fitsRefs(target))
}
function chooseModel(target: VideoModel) {
  if (inspecting.value || props.disabled) return
  if (!assets.value.length) {
    const next: Mode = canFrame(target) ? (canFrame(props.model) ? mode.value : 'frames') : 'refs'
    apply(target, next)
    return
  }
  if (canFrame(target)) {
    if (canFrame(props.model)) {
      requestChange(target, mode.value)
    } else if (fitsFrames(target)) {
      openConfirm(target, 'frames', fitsRefs(target))
    } else if (fitsRefs(target)) {
      apply(target, 'refs')
    } else {
      fileError.value = '目标模型不支持当前全部素材（超出首尾帧 2 张图片限制，或包含不兼容的参考类型），未切换模型。请先移除不兼容素材。'
    }
    return
  }
  requestChange(target, 'refs')
}
function canPick(i: number) { return !change.value && !inspecting.value && (!i || !!slots.value[0]) && (mode.value === 'frames' || i < refLimit(props.model)) }
function dispose(asset: Asset | null | undefined, forget: boolean) {
  if (asset?.preview) URL.revokeObjectURL(asset.preview)
  // While bound to a canvas media draft, keep `props.source` in memory until explicit detach/unmount
  // so accidental slot removal or swap can be restored in one click without silent degradation.
  if (forget && !props.canvasSourceRequired && asset && 'source' in asset && pendingImage(props.userId) === asset.source) {
    clearImage()
  }
}
function restoreCanvasSource() {
  if (!props.source || props.source.userId !== props.userId || pendingImage(props.userId) !== props.source) return
  const keptLocal = assets.value.filter((a): a is Extract<Asset, { file: File }> => 'file' in a)
  const bound: Asset = { source: props.source, label: props.source.label, kind: 'image' }
  if (mode.value === 'frames') {
    const secondImg = keptLocal.find(a => a.kind === 'image') ?? null
    keptLocal.filter(a => a !== secondImg).forEach(a => dispose(a, false))
    slots.value = secondImg ? [bound, secondImg] : [bound]
  } else {
    const maxExtra = Math.max(0, refLimit(props.model) - 1)
    keptLocal.slice(maxExtra).forEach(a => dispose(a, false))
    slots.value = [bound, ...keptLocal.slice(0, maxExtra)]
  }
  fileError.value = ''
}
function remove(i: number) {
  dispose(slots.value[i], true)
  const next = [...slots.value]; next[i] = null
  slots.value = mode.value === 'frames' ? next : next.filter(a => a !== null)
  fileError.value = ''
}
function move(i: number, offset: number) {
  const target = i + offset
  if (mode.value !== 'refs' || target < 0 || target >= assets.value.length) return
  const next = [...assets.value]; [next[i], next[target]] = [next[target], next[i]]
  slots.value = next
}
function swap() { if (assets.value.length === 2) slots.value = [assets.value[1], assets.value[0]] }
function reset() {
  slots.value.forEach(a => dispose(a, true)); slots.value = []; fileError.value = ''; change.value = null
  mode.value = canFrame(props.model) ? 'frames' : 'refs'
}

function fileData(blob: Blob, signal: AbortSignal): Promise<string> {
  return new Promise((resolve, reject) => {
    signal.throwIfAborted()
    const reader = new FileReader()
    const abort = () => reader.abort()
    signal.addEventListener('abort', abort, { once: true })
    reader.onloadend = () => {
      signal.removeEventListener('abort', abort)
      if (signal.aborted || reader.error || typeof reader.result !== 'string') reject(reader.error || new Error('素材读取中断，请重新选择。'))
      else resolve(reader.result)
    }
    reader.readAsDataURL(blob)
  })
}
async function pick(i: number, event: Event) {
  const input = event.currentTarget as HTMLInputElement
  const file = input.files?.[0]; input.value = ''
  if (!file || props.disabled || !canPick(i)) return
  inspecting.value = true
  try {
    const allowed = mode.value === 'frames' ? videoReferenceTypes.image : allowedTypes.value
    if (!allowed.includes(file.type) || !file.size) {
      throw new Error(mode.value === 'frames' ? '请选择非空的 PNG、JPEG 或 WebP 图片。' : `请选择非空的 ${formatHint.value} 素材。`)
    }
    if (file.size > maxFile) {
      throw new Error(mode.value === 'frames' ? '单张图片超过 8 MiB，请压缩后再选择。' : '单个素材超过 8 MiB，请压缩后再选择。')
    }
    const estimate = slots.value.reduce((n, a, index) => n + (index !== i && a && 'file' in a ? 4 * Math.ceil(a.file.size / 3) : 0), 4 * Math.ceil(file.size / 3))
    if (props.model.provider === 'goodput' && estimate >= maxFile) throw new Error('素材编码后已超过 H3 整份请求的 8 MiB 上限，请缩小文件。')
    // Preserve MIME type on the sliced header Blob so DataURL prefix matches the declared file type.
    const head = await fileData(file.slice(0, 32, file.type), lifetime.signal)
    if (videoReferenceTypes.image.includes(file.type)) validateVideoImage(head)
    else validateVideoReference(head)
    lifetime.signal.throwIfAborted()
    const kind = mode.value === 'frames' ? 'image' : referenceKinds.value.find(item => videoReferenceTypes[item].includes(file.type))
    if (!kind) throw new Error('当前模型不支持这种素材类型。')
    const next = [...slots.value]
    const asset: Asset = { label: file.name, file, preview: kind === 'image' ? URL.createObjectURL(file) : '', kind }
    dispose(next[i], true); next[i] = asset; slots.value = next; fileError.value = ''
  } catch (error) { if (!lifetime.signal.aborted) fileError.value = error instanceof Error ? error.message : '无法读取这个素材，请重新选择。' }
  finally { inspecting.value = false }
}
async function prepare(signal: AbortSignal): Promise<Pick<VideoSubmission, 'first_frame' | 'last_frame' | 'references'>> {
  if (!ready.value) throw new Error(invalidRoles.value || fileError.value || '请先确认素材用途。')
  const values: string[] = []
  for (const asset of assets.value) {
    if ('source' in asset && asset.source.userId !== props.userId) throw new Error('无权读取这张原图，请移除或替换。')
    let url: string
    if ('source' in asset) {
      const tailMode = props.tailFrame
      try { url = await readImage(asset.source, signal) }
      catch (e) {
        if (tailMode) {
          const detail = e instanceof Error && e.message ? e.message : ''
          throw new Error(detail || '无法从已固定成片提取真实尾帧 PNG（可能已过期、撤权、超时或超出 8 MiB），未降级无图，未提交视频。')
        }
        if (e instanceof RangeError || (e instanceof TypeError && /MiB|图片格式|原图数据/.test(e.message))) throw new Error('带入的原图须为 PNG、JPEG 或 WebP，且不超过 8 MiB。请替换图片；未提交视频。')
        throw new Error('无法读取带入的原图，可能已过期或权限已变更。请移除或替换；未提交视频。')
      }
      finally { if (!signal.aborted) spentSource.value = pendingImage(props.userId) !== asset.source }
    } else url = await fileData(asset.file, signal)
    signal.throwIfAborted(); validateVideoReference(url); values.push(url)
  }
  if (!values.length) return {}
  return mode.value === 'refs' ? { references: values } : { first_frame: values[0], ...(values[1] ? { last_frame: values[1] } : {}) }
}
onBeforeUnmount(() => { lifetime.abort(); slots.value.forEach(a => dispose(a, false)) })
watch(() => props.source, (nextSource, prevSource) => {
  if (nextSource === prevSource) return
  if (nextSource && nextSource.userId === props.userId) {
    slots.value.forEach(a => dispose(a, false))
    spentSource.value = false
    fileError.value = ''
    change.value = null
    slots.value = [{ source: nextSource, label: nextSource.label, kind: 'image' }]
    return
  }
  const filtered = slots.value.map(asset => {
    if (asset && 'source' in asset) {
      dispose(asset, false)
      return null
    }
    return asset
  })
  slots.value = mode.value === 'frames' ? filtered : filtered.filter(a => a !== null)
  spentSource.value = false
})
defineExpose({ chooseModel, prepare, reset, ready, canvasSourceIntact, restoreCanvasSource })
</script>
