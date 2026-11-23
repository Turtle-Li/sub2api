<template>
  <dialog v-if="job" ref="dialog" class="vs-player vs-delivery-dialog" :class="{ 'vs-player-mobile': mobile, 'vs-details-open': action === 'details' }"
    :data-layout="layoutMode" :style="deliveryStyle"
    aria-labelledby="vs-player-title" @close="closed" @keydown.esc.capture="escapeInline" @keydown="onDialogKeydown">
    <header class="vs-player-head">
      <div class="vs-inspector-title-group">
        <h2 id="vs-player-title" tabindex="-1">{{ displayModelName }}</h2>
        <span class="vs-status" :data-state="job.status"><i aria-hidden="true"></i>{{ states[job.status] }}</span>
      </div>
      <div class="vs-inspector-nav">
        <template v-if="navigableJobs.length > 1">
          <button type="button" class="vs-icon" aria-label="上一条作品" title="上一条作品 (←)" :disabled="!hasPrev" @click="step(-1)">
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 3.5 5.5 8 10 12.5" /></svg>
          </button>
          <button type="button" class="vs-icon" aria-label="下一条作品" title="下一条作品 (→)" :disabled="!hasNext" @click="step(1)">
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 3.5 10.5 8 6 12.5" /></svg>
          </button>
        </template>
        <button ref="closeButton" type="button" class="vs-icon" :aria-label="action === 'details' ? '关闭详情' : '收起审片'" @click="close">
          <Icon name="x" size="sm" aria-hidden="true" />
        </button>
      </div>
    </header>

    <div class="vs-delivery-body">
      <!-- Media Stage -->
      <div class="vs-delivery-stage">
        <div v-if="stageMetaLabel" class="vs-stage-overlay-top">
          <span class="vs-stage-meta-pill" :class="{ 'is-measured': Boolean(currentDurationLabel || currentDimensions) }">{{ stageMetaLabel }}</span>
        </div>

        <div class="vs-stage-canvas">
          <!-- Loaded playable video -->
          <template v-if="url && action === 'play' && previewable && !decodeError">
            <video ref="player" :src="url" controls playsinline preload="metadata"
              aria-label="生成的视频，使用播放控件开始观看" @loadedmetadata="onLoadedMetadata" @play="autoplayBlocked = false" @error="decodeError = true"></video>
            <p v-if="autoplayBlocked" class="vs-autoplay-hint" role="status">浏览器已限制自动播放，请点击播放器开始按钮观看。</p>
          </template>

        <!-- Loading in progress with explicit cancel -->
        <div v-else-if="loading" class="vs-cinema-state" role="status" aria-live="polite">
          <Icon :name="action === 'download' ? 'download' : 'play'" size="lg" aria-hidden="true" />
          <strong>正在读取成片…</strong>
          <p>正在按当前登录授权获取视频文件，可随时取消。</p>
          <div class="vs-cinema-state-actions">
            <button type="button" class="vs-cinema-ghost-btn" @click="cancelLoad">取消读取</button>
          </div>
        </div>

        <!-- Details preview (poster or honest film-slate placeholder, never auto-loads video) -->
        <template v-else-if="action === 'details'">
          <img v-if="posterUrl" class="vs-details-poster" :src="posterUrl" alt="已生成视频的真实封面帧" @load="onPosterLoad" @error="posterFailed">
          <div v-else class="vs-cinema-state vs-details-empty" :class="emptyToneClass">
            <Icon :name="emptyIcon" size="lg" aria-hidden="true" />
            <strong>{{ emptyTitle }}</strong>
            <p>{{ emptySubtitle }}</p>
            <div v-if="job.status === 'completed'" class="vs-cinema-state-actions">
              <button type="button" class="vs-primary vs-stage-play-btn" @click="loadContent('play')">
                <Icon name="play" size="xs" aria-hidden="true" />加载并播放
              </button>
            </div>
            <span v-if="job.status === 'completed'" class="vs-stage-cta-note">点击后按需加载视频流，不查询任务状态</span>
          </div>
          <div v-if="posterUrl && job.status === 'completed'" class="vs-stage-cta-layer">
            <button type="button" class="vs-primary vs-stage-play-btn" @click="loadContent('play')">
              <Icon name="play" size="xs" aria-hidden="true" />加载并播放
            </button>
            <span class="vs-stage-cta-note">点击后按需加载视频流，不查询任务状态</span>
          </div>
        </template>

        <!-- Playback/download terminal state (error, decode failure, oversized, download ready) -->
        <div v-else class="vs-cinema-state" :class="{ 'is-error': Boolean(error || decodeError), 'is-warn': !error && !decodeError && !previewable && action === 'play' }"
          role="status" aria-live="polite">
          <Icon :name="error || decodeError ? 'exclamationCircle' : action === 'download' ? 'download' : 'play'" size="lg" aria-hidden="true" />
          <strong>{{ error || (decodeError ? '浏览器无法解码这段视频' : !previewable && action === 'play' ? '文件较大，请下载后播放' : '视频已就绪，可以保存') }}</strong>
          <p v-if="decodeError">可以下载原文件，使用支持其编码的播放器打开。</p>
          <p v-else-if="!error && !previewable && action === 'play'">为避免占用过多内存，大于 48 MB 的成片请保存至本地播放。</p>
          <div class="vs-cinema-state-actions">
            <button v-if="error && job.status === 'completed'" type="button" class="vs-cinema-ghost-btn" @click="loadContent(action)">重试读取</button>
            <button type="button" class="vs-cinema-ghost-btn" @click="backToDetails">返回封面</button>
          </div>
        </div>
      </div>
    </div>

      <!-- Right / Bottom: Production Inspector -->
      <div class="vs-delivery-inspector">
        <div class="vs-inspector-scroll">
        <div v-if="canvasContext" class="vs-canvas-shot-card" role="note" aria-label="画布分镜上下文">
          <div class="vs-section-head">
            <strong>{{ canvasContext.nodeTitle || '画布分镜成片' }}</strong>
            <span v-if="canvasContext.takeLabel" class="vs-shot-take-pill">{{ canvasContext.takeLabel }}</span>
          </div>
          <p v-if="canvasContext.provenance">{{ canvasContext.provenance }}</p>
        </div>

        <!-- Honest Prompt Notice (never fabricated) -->
        <section class="vs-prompt-card" aria-label="画面与运镜描述">
          <div class="vs-section-head">
            <h3>画面与运镜描述</h3>
            <span>{{ canvasContext?.prompt ? '当前画布草稿记录' : '历史不留存原文' }}</span>
          </div>
          <p v-if="canvasContext?.prompt" class="vs-prompt-box is-recorded">{{ canvasContext.prompt }}</p>
          <p v-else class="vs-prompt-box">历史记录不保存文字描述与补充文本，复用参数后请重新填写提示词并确认当前报价。</p>
        </section>

        <!-- Specifications & Recorded Parameters -->
        <div class="vs-job-details">
          <div class="vs-section-head">
            <h3>视频规格与参数</h3>
            <span>缺省不虚构</span>
          </div>
          <dl class="vs-spec-grid">
            <div>
              <dt>模型</dt>
              <dd>{{ displayModelName }}</dd>
            </div>
            <div>
              <dt>状态</dt>
              <dd>{{ states[job.status] }}</dd>
            </div>
            <div v-if="currentDurationLabel">
              <dt>视频时长</dt>
              <dd>{{ currentDurationLabel }}</dd>
            </div>
            <div>
              <dt>{{ sizeFieldLabel }}</dt>
              <dd>{{ job.size ? formatSize(job.size) : '未指定' }}</dd>
            </div>
            <div v-if="currentDimensions" class="is-measured">
              <dt>视频尺寸</dt>
              <dd>{{ currentDimensions.width }} × {{ currentDimensions.height }}</dd>
            </div>
            <div>
              <dt>参考素材</dt>
              <dd>{{ materialDescription }}</dd>
            </div>
            <div v-if="showSeed">
              <dt>Seed</dt>
              <dd>{{ seedDescription }}</dd>
            </div>
            <div v-if="job.parameters?.native_size">
              <dt>原生画布意图</dt>
              <dd>{{ job.parameters.native_size }}</dd>
            </div>
            <div v-if="job.parameters?.resolution">
              <dt>画质意图</dt>
              <dd>{{ job.parameters.resolution }}</dd>
            </div>
            <div v-if="job.parameters?.timeout !== undefined">
              <dt>等待上限</dt>
              <dd>{{ job.parameters.timeout }} 秒</dd>
            </div>
            <div v-if="job.parameters?.has_extra">
              <dt>补充描述</dt>
              <dd>已附带（不留原文）</dd>
            </div>
            <div class="is-wide">
              <dt>实际扣费</dt>
              <dd>{{ chargeDescription }}</dd>
            </div>
          </dl>

          <p v-if="job.detail" class="vs-detail vs-delivery-alert" :class="{ 'vs-error': job.status === 'failed', 'vs-billing': job.status === 'uncertain' }">{{ job.detail }}</p>
          <p v-if="job.billing_review" class="vs-billing vs-delivery-alert">账务待核对{{ job.status === 'completed' ? '，成片已就绪，不影响播放与下载。' : '。' }}</p>

          <dl class="vs-lifecycle-list">
            <div><dt>创建时间</dt><dd>{{ localDate(job.created_at) }}</dd></div>
            <div><dt>保留有效期</dt><dd>{{ job.expires_at ? localDate(job.expires_at) : '未提供' }}</dd></div>
            <div class="vs-job-id">
              <dt>任务 ID</dt>
              <dd class="vs-job-id-row">
                <code>{{ job.id }}</code>
                <button
                  type="button"
                  class="vs-inline-copy-btn"
                  :class="{ 'is-copied': copiedId }"
                  :aria-label="copiedId ? '已复制任务 ID' : '复制任务 ID'"
                  @click="copyId"
                >
                  <Icon :name="copiedId ? 'check' : 'copy'" size="xs" aria-hidden="true" />
                  <span>{{ copiedId ? '已复制' : '复制 ID' }}</span>
                </button>
              </dd>
            </div>
          </dl>
        </div>
      </div>
    </div>
    </div>

    <footer class="vs-delivery-footer vs-delivery-actions">
      <div class="vs-delivery-footer-secondary">
        <div v-if="useMoreMenu" ref="moreMenuWrap" class="vs-more-wrap">
          <button
            ref="moreButton"
            type="button"
            class="vs-button vs-more-trigger"
            aria-haspopup="menu"
            :aria-expanded="moreOpen"
            aria-controls="vs-delivery-more-menu"
            @click="toggleMoreMenu"
            @keydown="onMoreTriggerKeydown"
          >
            <Icon name="more" size="xs" aria-hidden="true" />
            <span>更多</span>
          </button>
          <div
            v-if="moreOpen"
            id="vs-delivery-more-menu"
            ref="moreMenu"
            class="vs-more-menu"
            role="menu"
            aria-label="更多操作"
            @keydown="onMoreMenuKeydown"
          >
            <button v-if="allowReuse" type="button" class="vs-more-item" role="menuitem" @click="runMoreAction(reuseCurrent)">
              复用参数
            </button>
            <button
              v-if="canAddToCanvas"
              type="button"
              class="vs-more-item vs-delivery-canvas-btn"
              role="menuitem"
              @click="runMoreAction(addToCanvas)"
            >
              <Icon name="grid" size="xs" aria-hidden="true" />
              <span>加入画布</span>
            </button>
            <button v-if="canReleaseVideo" type="button" class="vs-more-item" role="menuitem" @click="runMoreAction(backToDetails)">
              释放视频
            </button>
          </div>
        </div>
        <template v-else>
          <button v-if="allowReuse" type="button" class="vs-button" @click="reuseCurrent">复用参数</button>
          <button v-if="canAddToCanvas" type="button" class="vs-button vs-delivery-canvas-btn" @click="addToCanvas">
            <Icon name="grid" size="xs" aria-hidden="true" />加入画布
          </button>
          <button v-if="canReleaseVideo" type="button" class="vs-text vs-delivery-release-btn" @click="backToDetails">释放视频</button>
        </template>
      </div>

      <div class="vs-delivery-footer-primary">
        <button
          type="button"
          class="vs-primary vs-delivery-download-btn"
          :disabled="job.status !== 'completed' || loading"
          :title="job.status === 'completed' ? '按需下载原视频文件' : '仅已完成且未过期的作品可下载'"
          @click="triggerDownload"
        >
          <Icon name="download" size="xs" aria-hidden="true" />下载成片
        </button>
      </div>

      <a
        v-if="url"
        ref="downloadLink"
        class="vs-download-anchor"
        :href="url"
        :download="`video-${job.id}.${format}`"
        tabindex="-1"
        aria-hidden="true"
        hidden
      ></a>
    </footer>
  </dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { videoModelDisplayName, videoStudio, VIDEO_CONTENT_MAX_BYTES, type VideoJob, type VideoReferenceKind } from '@/features/video-studio/api'
import { createWorkspaceDurationStore, durationSecondsToMs, formatVideoDuration, measuredDurationMs, type WorkspaceDurationStore } from './mediaDuration'
import type { VideoPosterCache } from './posterCache'
import './video-delivery.css'

interface MediaDimensions {
  readonly jobId: string
  readonly width: number
  readonly height: number
}

export interface DeliveryCanvasContext {
  readonly nodeTitle?: string
  readonly takeLabel?: string
  readonly prompt?: string
  readonly provenance?: string
}

const props = withDefaults(defineProps<{
  mobile: boolean
  cache: VideoPosterCache
  durations?: WorkspaceDurationStore
  jobs?: readonly VideoJob[]
  modelLabels?: Readonly<Record<string, string>>
  canvasEnabled?: boolean
  allowReuse?: boolean
  canvasContext?: DeliveryCanvasContext | null
}>(), {
  jobs: () => [],
  modelLabels: () => ({}),
  canvasEnabled: false,
  allowReuse: true,
  canvasContext: null,
})

const emit = defineEmits<{
  expired: [id: string]
  focusCard: [id: string]
  reuse: [job: VideoJob, opener?: HTMLElement]
  canvas: [job: VideoJob]
}>()

const states: Record<VideoJob['status'], string> = {
  submitting: '提交中',
  queued: '排队中',
  running: '生成中',
  completed: '已完成',
  failed: '生成失败',
  uncertain: '状态待核实',
  expired: '已过期',
}
const referenceKindLabels: Record<VideoReferenceKind, string> = {
  image: '图片',
  video: '视频',
  audio: '音频',
  file: '文件',
}
const dateFormat = new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })

const dialog = ref<HTMLDialogElement>()
const closeButton = ref<HTMLButtonElement>()
const downloadLink = ref<HTMLAnchorElement>()
const player = ref<HTMLVideoElement>()
const moreMenuWrap = ref<HTMLElement>()
const moreButton = ref<HTMLButtonElement>()
const moreMenu = ref<HTMLElement>()
const job = shallowRef<VideoJob | null>(null)
const action = ref<'details' | 'play' | 'download'>('details')
const url = ref('')
const format = ref('')
const loading = ref(false)
const previewable = ref(false)
const decodeError = ref(false)
const error = ref('')
const copiedId = ref(false)
const moreOpen = ref(false)
const autoplayBlocked = ref(false)
const posterDimensions = shallowRef<MediaDimensions | null>(null)
const mediaDimensions = shallowRef<MediaDimensions | null>(null)
const fallbackDurations = createWorkspaceDurationStore()
const durationStore = computed(() => props.durations ?? fallbackDurations)

const readNarrowViewport = () =>
  typeof window !== 'undefined' && typeof window.innerWidth === 'number' && window.innerWidth > 0 && window.innerWidth <= 560
const isNarrowViewport = ref(readNarrowViewport())
function updateNarrowViewport() {
  isNarrowViewport.value = readNarrowViewport()
}
if (typeof window !== 'undefined') {
  window.addEventListener('resize', updateNarrowViewport, { passive: true })
}

let controller: AbortController | undefined
let opener: HTMLElement | undefined
let copyTimer: ReturnType<typeof setTimeout> | undefined
let releasePoster: () => void = () => {}

const posterUrl = computed(() => job.value ? props.cache.url(job.value.id) : '')
const displayModelName = computed(() => job.value ? videoModelDisplayName(job.value.model, props.modelLabels) : '')
const canAddToCanvas = computed(() => Boolean(props.canvasEnabled && job.value?.status === 'completed'))
const canReleaseVideo = computed(() => Boolean(url.value && action.value === 'play'))
const secondaryActionCount = computed(() =>
  (props.allowReuse ? 1 : 0) + (canAddToCanvas.value ? 1 : 0) + (canReleaseVideo.value ? 1 : 0),
)
const useMoreMenu = computed(() => (props.mobile || isNarrowViewport.value) && secondaryActionCount.value > 1)

const currentDurationMs = computed(() => job.value ? measuredDurationMs(job.value, durationStore.value.get(job.value.id)) : null)
const currentDurationLabel = computed(() => formatVideoDuration(currentDurationMs.value))

// Strictly bind measured pixel dimensions to the current job id so switching jobs can never cross-contaminate.
const currentDimensions = computed(() => {
  if (!job.value || !mediaDimensions.value || mediaDimensions.value.jobId !== job.value.id) return null
  return mediaDimensions.value
})
const currentPosterDimensions = computed(() => {
  if (!job.value || !posterDimensions.value || posterDimensions.value.jobId !== job.value.id) return null
  return posterDimensions.value
})
const measuredFrame = computed(() => currentDimensions.value ?? currentPosterDimensions.value)
const measuredAspect = computed(() => {
  const frame = measuredFrame.value
  if (!frame || frame.width <= 0 || frame.height <= 0) return null
  const ratio = frame.width / frame.height
  return Number.isFinite(ratio) && ratio > 0 ? ratio : null
})
const layoutMode = computed<'landscape' | 'portrait' | 'record'>(() => {
  if (!job.value || job.value.status !== 'completed') return 'record'
  if (error.value || decodeError.value) return 'record'
  if (action.value === 'play' && url.value && !previewable.value) return 'record'
  const aspect = measuredAspect.value
  if (aspect === null) return 'record'
  return aspect < 1 ? 'portrait' : 'landscape'
})
const deliveryStyle = computed(() => ({
  '--delivery-aspect': String(measuredAspect.value ?? 16 / 9),
}))

const stageMetaLabel = computed(() => {
  const current = job.value
  if (!current) return ''
  const parts: string[] = []
  if (currentDurationLabel.value) parts.push(currentDurationLabel.value)
  if (currentDimensions.value) parts.push(`${currentDimensions.value.width} × ${currentDimensions.value.height}`)
  else if (current.size) parts.push(formatSize(current.size))
  return parts.join(' · ')
})

const emptyToneClass = computed(() => {
  const status = job.value?.status
  if (status === 'failed') return 'is-error'
  if (status === 'expired' || status === 'uncertain') return 'is-warn'
  return ''
})

const emptyIcon = computed(() => {
  const status = job.value?.status
  if (status === 'failed' || status === 'expired' || status === 'uncertain') return 'exclamationCircle'
  return 'play'
})

const emptyTitle = computed(() => {
  const status = job.value?.status
  if (status === 'completed') return '历史成片暂无封面'
  if (status === 'expired') return '成片已过期'
  if (status === 'failed') return '本次未生成成片'
  if (status === 'uncertain') return '提交结果待核实'
  if (status === 'running') return '正在生成视频'
  if (status === 'queued') return '任务正在排队'
  if (status === 'submitting') return '正在提交原任务'
  return '这条记录没有封面帧'
})

const emptySubtitle = computed(() => {
  const status = job.value?.status
  if (status === 'completed') return '仍可手动加载播放，不会使用参考图或虚构封面。'
  if (status === 'expired') return '保留有效期已过，无法加载播放或下载原文件。'
  if (status === 'failed') return '本次未产出成片，可复用已记录参数重新创作。'
  if (status === 'uncertain') return '原任务号已保留，请返回画廊核对原任务，不要重发。'
  return '详情页不自动轮询上游；完成后将提供真实封面与成片。'
})

const sizeFieldLabel = computed(() => {
  const current = job.value
  if (!current) return '提交尺寸'
  const trimmed = current.size.trim()
  if (/^\d+(?:\.\d+)?:\d+(?:\.\d+)?$/.test(trimmed)) return '提交画幅'
  if (current.model === 'muse-video' && !/^\d+x\d+$/i.test(trimmed)) return '提交画幅'
  return '提交尺寸'
})

const materialDescription = computed(() => {
  const parameters = job.value?.parameters
  if (!parameters) return '未记录'
  if (parameters.material_mode === 'references') {
    const count = parameters.reference_count
    const kinds = parameters.reference_kinds
    if (Array.isArray(kinds) && kinds.length === count && count > 0 && kinds.every(k => k === 'image')) {
      return `${count} 张参考图`
    }
    const kindDetail = Array.isArray(kinds) && kinds.length > 0
      ? `（${kinds.map(k => referenceKindLabels[k] || k).join('、')}）`
      : ''
    return `${count} 个参考素材${kindDetail}`
  }
  return ({
    text: '纯文字（无参考素材）',
    first_frame: '首帧',
    first_last_frame: '首尾帧',
  } as const)[parameters.material_mode]
})

const showSeed = computed(() => {
  const current = job.value
  if (!current) return false
  return current.parameters?.seed !== undefined || current.model === 'minimax/h3-fast'
})

const seedDescription = computed(() => {
  const parameters = job.value?.parameters
  if (!parameters) return '未记录'
  return parameters.seed === undefined ? '未返回' : String(parameters.seed)
})

const chargeDescription = computed(() => {
  const current = job.value
  if (!current) return '未知'
  const amount = current.charged_amount
  const currency = typeof current.charged_currency === 'string' ? current.charged_currency.trim() : ''
  if (typeof amount === 'number' && Number.isFinite(amount) && currency) {
    return `${formatAmount(amount)} ${currency} · 已结算`
  }
  return '未知（未返回真实结算）'
})

const navigableJobs = computed(() => props.jobs)
const currentIndex = computed(() => job.value ? navigableJobs.value.findIndex(item => item.id === job.value!.id) : -1)
const hasPrev = computed(() => currentIndex.value > 0)
const hasNext = computed(() => currentIndex.value >= 0 && currentIndex.value < navigableJobs.value.length - 1)

function formatAmount(value: number) {
  return Number.isInteger(value) ? String(value) : String(Number(value.toFixed(4)))
}

function localDate(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '时间未知' : dateFormat.format(date)
}

function formatSize(size: string) {
  return size.replace(/x/i, ' × ')
}

watch(posterUrl, current => {
  const releasePrevious = releasePoster
  releasePoster = current && job.value ? props.cache.retain(job.value.id) : () => {}
  releasePrevious()
  if (current && job.value) {
    const img = dialog.value?.querySelector<HTMLImageElement>('.vs-details-poster')
    if (img?.complete && img.naturalWidth > 0 && img.naturalHeight > 0) {
      posterDimensions.value = { jobId: job.value.id, width: img.naturalWidth, height: img.naturalHeight }
    }
  }
}, { flush: 'post', immediate: true })

// Keep currently displayed job synchronized if gallery refreshes metadata (e.g. marked expired).
watch(() => props.jobs, list => {
  if (!job.value) return
  const updated = list.find(item => item.id === job.value!.id)
  if (updated && updated !== job.value) job.value = updated
})

function onPosterLoad(event: Event) {
  const target = event.currentTarget as HTMLImageElement | null
  const current = job.value
  if (!target || !current) return
  if (target.naturalWidth > 0 && target.naturalHeight > 0) {
    posterDimensions.value = { jobId: current.id, width: target.naturalWidth, height: target.naturalHeight }
  }
}

function posterFailed() {
  posterDimensions.value = null
  if (job.value && posterUrl.value) props.cache.invalidate(job.value.id)
}

function onLoadedMetadata(event: Event) {
  const target = event.currentTarget as HTMLVideoElement | null
  const current = job.value
  if (!target || !current || !url.value || target !== player.value) return
  const ms = durationSecondsToMs(target.duration)
  if (ms !== null) durationStore.value.set(current.id, ms)
  if (target.videoWidth > 0 && target.videoHeight > 0) {
    mediaDimensions.value = { jobId: current.id, width: target.videoWidth, height: target.videoHeight }
  }
}

async function container(blob: Blob) {
  const head = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String.fromCharCode(...new Uint8Array(reader.result as ArrayBuffer)))
    reader.onerror = () => reject(new Error('无法识别视频文件。'))
    reader.readAsArrayBuffer(blob.slice(0, 16))
  })
  if (head.startsWith('\x1a\x45\xdf\xa3')) return { mime: 'video/webm', ext: 'webm' }
  if (head.slice(4, 8) === 'ftyp') {
    const brand = head.slice(8, 12)
    if (brand === 'qt  ') return { mime: 'video/quicktime', ext: 'mov' }
    if (brand.startsWith('3gp')) return { mime: 'video/3gpp', ext: '3gp' }
    if (head.length >= 12 && !/^(avif|avis|mif1|heic|heix)$/.test(brand)) return { mime: 'video/mp4', ext: 'mp4' }
  }
  if (blob.type === 'video/quicktime' && ['moov', 'mdat', 'wide'].includes(head.slice(4, 8))) return { mime: blob.type, ext: 'mov' }
  throw new Error('未识别到可播放的视频文件。')
}

function discard() {
  controller?.abort()
  controller = undefined
  if (player.value) player.value.removeAttribute('src')
  if (url.value) URL.revokeObjectURL(url.value)
  url.value = ''
  loading.value = false
  error.value = ''
  decodeError.value = false
  autoplayBlocked.value = false
  format.value = ''
  previewable.value = false
  mediaDimensions.value = null
}

function cancelLoad() {
  discard()
  action.value = 'details'
}

function backToDetails() {
  discard()
  action.value = 'details'
}

function onDocumentOutside(event: Event) {
  if (!moreOpen.value) return
  const target = event.target as Node | null
  if (target && moreMenuWrap.value?.contains(target)) return
  closeMoreMenu(false)
}

watch(useMoreMenu, enabled => {
  if (!enabled && moreOpen.value) closeMoreMenu(false)
})

watch(moreOpen, openNow => {
  if (typeof document === 'undefined') return
  if (openNow) {
    document.addEventListener('pointerdown', onDocumentOutside, true)
    document.addEventListener('click', onDocumentOutside, true)
  } else {
    document.removeEventListener('pointerdown', onDocumentOutside, true)
    document.removeEventListener('click', onDocumentOutside, true)
  }
})

async function openMoreMenu(focusTarget: 'first' | 'last' = 'first') {
  moreOpen.value = true
  await nextTick()
  const items = moreMenu.value?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')
  if (!items?.length) return
  ;(focusTarget === 'last' ? items[items.length - 1] : items[0]).focus()
}

function closeMoreMenu(restoreFocus = false) {
  if (!moreOpen.value) return
  moreOpen.value = false
  if (restoreFocus) void nextTick(() => moreButton.value?.focus())
}

function toggleMoreMenu() {
  if (moreOpen.value) closeMoreMenu(false)
  else void openMoreMenu('first')
}

function runMoreAction(handler: () => void) {
  closeMoreMenu(false)
  handler()
}

function onMoreTriggerKeydown(event: KeyboardEvent) {
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    void openMoreMenu('first')
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    void openMoreMenu('last')
  }
}

function onMoreMenuKeydown(event: KeyboardEvent) {
  const items = Array.from(moreMenu.value?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') ?? [])
  if (!items.length) return
  const current = items.indexOf(document.activeElement as HTMLButtonElement)
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    items[(current + 1 + items.length) % items.length].focus()
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    items[(current - 1 + items.length) % items.length].focus()
  } else if (event.key === 'Home') {
    event.preventDefault()
    items[0].focus()
  } else if (event.key === 'End') {
    event.preventDefault()
    items[items.length - 1].focus()
  } else if (event.key === 'Tab') {
    closeMoreMenu(false)
  } else if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
    event.stopPropagation()
  }
}

function closed() {
  const id = job.value?.id
  const trigger = opener
  clearTimeout(copyTimer)
  copiedId.value = false
  closeMoreMenu(false)
  posterDimensions.value = null
  discard()
  releasePoster()
  releasePoster = () => {}
  job.value = null
  opener = undefined
  void nextTick(() => {
    if (trigger?.isConnected) trigger.focus()
    else if (id) emit('focusCard', id)
  })
}

function nativeFullscreen() {
  const doc = document as Document & { webkitFullscreenElement?: Element | null }
  const media = player.value as (HTMLVideoElement & { webkitDisplayingFullscreen?: boolean }) | undefined
  return Boolean(doc.fullscreenElement || doc.webkitFullscreenElement || media?.webkitDisplayingFullscreen)
}

function escapeInline(event: KeyboardEvent) {
  if (nativeFullscreen() || !job.value) return
  event.preventDefault()
  event.stopPropagation()
  if (moreOpen.value) {
    closeMoreMenu(true)
    return
  }
  close()
}

function isEditableElement(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  const tag = target.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target.isContentEditable
}

function onDialogKeydown(event: KeyboardEvent) {
  if (!job.value || moreOpen.value || event.ctrlKey || event.metaKey || event.altKey) return
  if (isEditableElement(event.target)) return
  if (event.key === 'ArrowLeft' && hasPrev.value) {
    event.preventDefault()
    step(-1)
    return
  }
  if (event.key === 'ArrowRight' && hasNext.value) {
    event.preventDefault()
    step(1)
    return
  }
  if ((event.key === 'm' || event.key === 'M') && action.value === 'play' && player.value) {
    event.preventDefault()
    player.value.muted = !player.value.muted
    return
  }
  if (event.key === ' ' && action.value === 'play' && player.value) {
    const tag = event.target instanceof HTMLElement ? event.target.tagName : ''
    if (tag === 'BUTTON' || tag === 'A' || tag === 'VIDEO') return
    event.preventDefault()
    if (player.value.paused) void player.value.play().catch(() => {})
    else player.value.pause()
  }
}

function step(delta: -1 | 1) {
  const index = currentIndex.value
  const target = navigableJobs.value[index + delta]
  if (!target) return
  clearTimeout(copyTimer)
  copiedId.value = false
  closeMoreMenu(false)
  posterDimensions.value = null
  discard()
  job.value = target
  action.value = 'details'
}

function close() {
  if (dialog.value?.open) dialog.value.close()
}

async function copyId() {
  if (!job.value) return
  try {
    await navigator.clipboard?.writeText(job.value.id)
    copiedId.value = true
    clearTimeout(copyTimer)
    copyTimer = setTimeout(() => { copiedId.value = false }, 1800)
  } catch { /* Clipboard may be unavailable in restricted contexts. */ }
}

function reuseCurrent() {
  const current = job.value
  if (!current) return
  const trigger = opener
  close()
  emit('reuse', current, trigger)
}

function addToCanvas() {
  const current = job.value
  if (!current || current.status !== 'completed' || !props.canvasEnabled) return
  close()
  emit('canvas', current)
}

function triggerDownload() {
  if (!job.value || job.value.status !== 'completed' || loading.value) return
  if (url.value && downloadLink.value) {
    downloadLink.value.click()
    return
  }
  void loadContent('download')
}

async function loadContent(kind: 'play' | 'download') {
  const item = job.value
  if (!item || item.status !== 'completed') return
  discard()
  action.value = kind
  loading.value = true
  const read = new AbortController()
  controller = read
  try {
    const blob = await videoStudio.content(item.id, read.signal)
    read.signal.throwIfAborted()
    if (blob.size > VIDEO_CONTENT_MAX_BYTES) throw new RangeError('成片超过 100 MB 读取上限。')
    const type = await container(blob)
    read.signal.throwIfAborted()
    if (job.value?.id !== item.id) return
    format.value = type.ext
    previewable.value = blob.size <= 48 * 1024 * 1024
    url.value = URL.createObjectURL(blob.type === type.mime ? blob : blob.slice(0, blob.size, type.mime))
    loading.value = false
    await nextTick()
    if (read.signal.aborted) return
    if (kind === 'download') {
      downloadLink.value?.click()
    } else if (kind === 'play' && previewable.value && player.value) {
      try {
        const playResult = player.value.play()
        if (playResult && typeof playResult.catch === 'function') {
          playResult.catch(() => {
            if (!read.signal.aborted) autoplayBlocked.value = true
          })
        }
      } catch {
        autoplayBlocked.value = true
      }
    }
  } catch (e) {
    if (read.signal.aborted) return
    const err = e as { status?: number; response?: { status?: number }; message?: string } | null
    const status = err?.status || err?.response?.status
    if (status === 410) { error.value = '视频文件已过期，无法取回。'; emit('expired', item.id) }
    else if (status === 401 || status === 403) error.value = '当前登录或原任务授权已失效，无法读取此视频。'
    else if (status === 404) error.value = '找不到这个视频文件，请核对原任务。'
    else if (status === 409) error.value = '成片尚不可读取，请查询原任务状态。'
    else if (e instanceof RangeError || /maxContentLength|100 MB|超过.*上限/i.test(err?.message || '')) error.value = '成片超过 100 MB 读取上限，未载入浏览器。'
    else error.value = '无法读取有效的视频文件，请稍后再打开。'
  } finally {
    if (!read.signal.aborted) loading.value = false
  }
}

async function open(item: VideoJob, kind: 'details' | 'play' | 'download', trigger: HTMLElement) {
  if (kind !== 'details' && item.status !== 'completed') return
  clearTimeout(copyTimer)
  copiedId.value = false
  closeMoreMenu(false)
  discard()
  opener = trigger
  job.value = item
  action.value = kind
  loading.value = kind !== 'details'
  await nextTick()
  if (!dialog.value?.open) dialog.value?.showModal()
  closeButton.value?.focus()
  if (kind === 'details') return
  await loadContent(kind)
}

onBeforeUnmount(() => {
  clearTimeout(copyTimer)
  if (typeof document !== 'undefined') {
    document.removeEventListener('pointerdown', onDocumentOutside, true)
    document.removeEventListener('click', onDocumentOutside, true)
  }
  if (typeof window !== 'undefined') {
    window.removeEventListener('resize', updateNarrowViewport)
  }
  opener = undefined
  releasePoster()
  releasePoster = () => {}
  job.value = null
  discard()
  fallbackDurations.clear()
  if (dialog.value?.open) dialog.value.close()
})

defineExpose({ open })
</script>
