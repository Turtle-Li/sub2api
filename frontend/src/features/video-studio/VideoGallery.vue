<template>
  <section class="vs-gallery" aria-labelledby="vs-gallery-title">
    <div v-if="outstanding.length" class="vs-notice vs-outstanding" aria-live="polite">
      <strong>尚未确认的提交 · 请勿重发</strong><p>原任务号已保留。查询只读取状态，不会再次生成。</p>
      <div v-for="id in outstanding" :key="id" class="vs-lookup-row"><code>{{ id }}</code><button type="button" class="vs-button" :disabled="checking && tracked === id" @click="lookup(id)">查询原任务</button></div>
    </div>
    <div v-if="tracked" ref="trackingPanel" class="vs-follow" tabindex="-1" aria-live="polite">
      <div role="status"><strong>{{ auto ? '正在跟踪' : '原任务' }}</strong><span> · {{ trackedJob ? states[trackedJob.status] : '核对提交结果' }}</span>
        <code>{{ tracked }}</code><p v-if="checkError" class="vs-error">{{ checkError }}</p></div>
      <button type="button" class="vs-text" :disabled="checking" @click="readTracked">{{ checking ? '查询中…' : '更新状态' }}</button>
      <button type="button" class="vs-icon" aria-label="关闭任务跟踪" @click="stopTracking"><Icon name="x" size="xs" aria-hidden="true" /></button>
    </div>
    <VideoDelivery ref="delivery" :mobile="mobile" :cache="posterCache" :durations="measuredDurations" :jobs="visibleJobs" :canvas-enabled="canvasEnabled" @expired="markExpired" @focus-card="focusCard" @reuse="(job, opener) => emit('reuse', job, opener)" @canvas="job => emit('canvas', job)" />
    <div class="vs-gallery-head">
      <div class="vs-title-line"><h2 id="vs-gallery-title" ref="galleryHeading" tabindex="-1">我的视频</h2><span class="vs-count">已加载 {{ jobs.length }} 条</span></div>
      <div v-if="jobs.length" class="vs-filters" role="group" aria-label="筛选已加载的视频">
        <button v-for="option in filters" :key="option.value" type="button" :aria-pressed="filter === option.value" @click="filter = option.value">{{ option.label }}</button>
      </div>
      <button class="vs-button vs-refresh" type="button" title="仅读取本地索引，不拉取媒体" :disabled="listing" @click="load()"><Icon name="refresh" size="sm" aria-hidden="true" />刷新索引</button>
      <p v-if="jobs.length" class="vs-filter-note">仅筛选已加载的 {{ jobs.length }} 条记录，不查询全部分页。</p>
    </div>
    <div v-if="listError" class="vs-notice" role="alert">无法读取视频列表。已显示的记录不会被清空。
      <button class="vs-text" type="button" :disabled="listing" @click="load(retryMore)">重新读取</button></div>
    <div v-if="listing && !jobs.length" class="vs-loading" role="status"><div aria-hidden="true"></div>正在读取视频记录…</div>
    <div v-else-if="!jobs.length && !listError" class="vs-empty">
      <svg class="vs-film" width="96" height="64" viewBox="0 0 96 64" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
        <rect x="5" y="8" width="86" height="48" rx="7"/><path d="M6 19h84M6 45h84M23 19v26m50-26v26"/>
        <path stroke-linecap="round" stroke-width="2.8" d="M14 14h4m9 0h4m9 0h4m9 0h4m9 0h4m9 0h4M14 50h4m9 0h4m9 0h4m9 0h4m9 0h4m9 0h4"/>
        <path stroke-linejoin="round" d="m44 25 11 7-11 7z"/>
      </svg>
      <h3>还没有视频作品</h3><p>从一段镜头描述开始，也可以带入图片。</p>
      <div class="vs-example"><p>“{{ example }}”</p><button type="button" class="vs-text" @click="emit('example', $event.currentTarget as HTMLElement)">使用示例描述 <Icon name="arrowRight" size="xs" aria-hidden="true" /></button></div>
    </div>
    <ul v-else-if="visibleJobs.length" ref="cards" class="vs-cards">
      <li v-for="job in visibleJobs" :key="job.id" :id="`vs-job-${job.id}`" tabindex="-1" :aria-label="`${modelLabel(job.model)}，${states[job.status]}`" class="vs-card" :class="{ 'is-tracked': tracked === job.id, 'is-failed': job.status === 'failed' }">
        <button type="button" class="vs-card-stage" :class="stageClass(job.size)" :aria-label="job.status === 'completed' ? '加载播放' : `查看 ${modelLabel(job.model)} 作品详情`" @click="delivery?.open(job, job.status === 'completed' ? 'play' : 'details', $event.currentTarget as HTMLElement)">
          <VideoPoster :job="job" :cache="posterCache" :revision="posterRevision" />
          <span class="vs-stage-top"><span class="vs-status" :data-state="job.status"><i aria-hidden="true"></i>{{ states[job.status] }}</span></span>
          <span v-if="job.status === 'completed'" class="vs-stage-play" aria-hidden="true"><Icon name="play" size="sm" /></span>
          <span class="vs-stage-bottom"><span v-if="durationBadge(job)">{{ durationBadge(job) }}</span><span>{{ aspectLabel(job.size) }}</span></span>
        </button>
        <div class="vs-card-body" @click="delivery?.open(job, 'details', ($event.currentTarget as HTMLElement).querySelector('.vs-details-button') as HTMLElement)">
          <div class="vs-card-head">
            <h3><span class="vs-model-mark" aria-hidden="true">{{ modelLabel(job.model).slice(0, 1) }}</span>{{ modelLabel(job.model) }}</h3>
            <time :datetime="String(job.created_at)">{{ shortDate(job.created_at) }}</time>
            <div class="vs-card-actions">
              <button v-if="canvasEnabled && job.status === 'completed'" type="button" class="vs-icon vs-canvas-button" aria-label="加入画布" title="加入画布" @click.stop="emit('canvas', job)"><Icon name="grid" size="xs" aria-hidden="true" /></button>
              <button v-if="job.status === 'completed'" type="button" class="vs-icon" aria-label="下载" title="下载" @click.stop="delivery?.open(job, 'download', $event.currentTarget as HTMLElement)"><Icon name="download" size="xs" aria-hidden="true" /></button>
              <button type="button" class="vs-icon vs-details-button" aria-label="详情" title="详情" @click.stop="delivery?.open(job, 'details', $event.currentTarget as HTMLElement)"><Icon name="infoCircle" size="xs" aria-hidden="true" /></button>
              <button v-if="!['completed', 'expired', 'failed'].includes(job.status)" type="button" class="vs-text vs-query-button" :disabled="checking && tracked === job.id" @click.stop="lookup(job.id)">{{ job.status === 'uncertain' ? '核对原任务' : '查询状态' }}</button>
            </div>
          </div>
          <div v-if="job.parameters?.material_mode === 'references' || job.billing_review" class="vs-card-flags">
            <span v-if="job.parameters?.material_mode === 'references'">{{ referenceFlag(job) }}</span><span v-if="job.billing_review" class="vs-billing-tag">账务待核对</span>
          </div>
        </div>
      </li>
    </ul>
    <p v-else-if="jobs.length" class="vs-filter-empty">已加载的记录中没有这种状态。<button type="button" class="vs-text" @click="filter = 'all'">显示全部</button></p>
    <div v-if="cursor" class="vs-more"><button type="button" class="vs-button" :disabled="listing" @click="load(true)">{{ listing ? '正在读取…' : '加载更多记录' }}</button></div>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { videoStudio, type VideoJob } from '@/features/video-studio/api'
import VideoDelivery from './VideoDelivery.vue'
import VideoPoster from './VideoPoster.vue'
import { useGalleryLayout } from './galleryLayout'
import { createWorkspaceDurationStore, formatVideoDuration, measuredDurationMs } from './mediaDuration'
import { createVideoPosterCache } from './posterCache'

withDefaults(defineProps<{ outstanding: string[]; example: string; mobile: boolean; canvasEnabled?: boolean }>(), {
  canvasEnabled: false,
})
const emit = defineEmits<{ resolved: [job: VideoJob]; example: [opener: HTMLElement]; reuse: [job: VideoJob, opener?: HTMLElement]; canvas: [job: VideoJob] }>()
const states: Record<VideoJob['status'], string> = { submitting: '提交中', queued: '排队中', running: '生成中', completed: '已完成', failed: '生成失败', uncertain: '状态待核实', expired: '已过期' }
const modelLabel = (id: string) => ({ 'minimax/h3': 'H3', 'minimax/h3-fast': 'H3 Fast', 'muse-video': 'Muse' })[id] || id
const measuredDurations = createWorkspaceDurationStore()
function durationBadge(job: VideoJob) {
  return formatVideoDuration(measuredDurationMs(job, measuredDurations.get(job.id)))
}
function referenceFlag(job: VideoJob) {
  const params = job.parameters
  if (!params || params.material_mode !== 'references') return ''
  const count = params.reference_count
  const kinds = params.reference_kinds
  if (Array.isArray(kinds) && kinds.length === count && count > 0 && kinds.every(k => k === 'image')) {
    return `${count} 张参考图`
  }
  return `${count} 个参考素材`
}
function shortDate(value: VideoJob['created_at']) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '时间未知'
  const year = date.getFullYear() === new Date().getFullYear() ? '' : `${date.getFullYear()}-`
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${year}${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}
function aspectRatio(size: string) {
  const dimensions = /^(\d+)x(\d+)$/i.exec(size)
  if (dimensions) return Number(dimensions[1]) / Number(dimensions[2])
  const ratio = /^(\d+(?:\.\d+)?):(\d+(?:\.\d+)?)$/.exec(size)
  return ratio ? Number(ratio[1]) / Number(ratio[2]) : 0
}
function aspectLabel(size: string) {
  if (!size) return '未指定'
  const dimensions = /^(\d+)x(\d+)$/i.exec(size)
  if (!dimensions) return size
  let [width, height] = [Number(dimensions[1]), Number(dimensions[2])]
  if (!width || !height) return size.replace('x', ' × ')
  const gcd = (a: number, b: number): number => b ? gcd(b, a % b) : a
  const divisor = gcd(width, height)
  width /= divisor; height /= divisor
  return `${width}:${height}`
}
function stageClass(size: string) {
  const ratio = aspectRatio(size)
  if (ratio && ratio < .85) return 'is-portrait'
  if (ratio && ratio < 1.2) return 'is-square'
  return 'is-landscape'
}
const jobs = shallowRef<VideoJob[]>([])
const posterCache = createVideoPosterCache()
const posterRevision = ref(0)
const cursor = ref('')
const listing = ref(false)
const listError = ref(false)
const retryMore = ref(false)
type Filter = 'all' | 'active' | 'completed' | 'issue'
const filters: { value: Filter; label: string }[] = [{ value: 'all', label: '全部' }, { value: 'active', label: '进行中' }, { value: 'completed', label: '已完成' }, { value: 'issue', label: '异常与过期' }]
const filter = ref<Filter>('all')
const visibleJobs = computed(() => jobs.value.filter(job => {
  if (filter.value === 'active') return ['submitting', 'queued', 'running'].includes(job.status)
  if (filter.value === 'completed') return job.status === 'completed'
  if (filter.value === 'issue') return ['failed', 'uncertain', 'expired'].includes(job.status)
  return true
}))
const cards = ref<HTMLElement>()
useGalleryLayout(cards, visibleJobs)
const tracked = ref('')
const trackedJob = computed(() => jobs.value.find(j => j.id === tracked.value))
const auto = ref(false)
const checking = ref(false)
const checkError = ref('')
const galleryHeading = ref<HTMLElement>()
const trackingPanel = ref<HTMLElement>()
const delivery = ref<InstanceType<typeof VideoDelivery>>()
const lifetime = new AbortController()
let lookupController: AbortController | undefined
let timer: ReturnType<typeof setTimeout> | undefined
const pending = (j: VideoJob) => ['submitting', 'queued', 'running'].includes(j.status)
const changes = new Map<string, number>()
let revision = 0
function put(job: VideoJob) {
  changes.set(job.id, ++revision)
  jobs.value = jobs.value.some(j => j.id === job.id) ? jobs.value.map(j => j.id === job.id ? job : j) : [job, ...jobs.value]
}
async function load(more = false) {
  if (listing.value) return
  const start = revision
  listing.value = true; listError.value = false
  try {
    const page = await videoStudio.list(more ? cursor.value : undefined, lifetime.signal)
    if (lifetime.signal.aborted) return
    const fresh = jobs.value.filter(j => (changes.get(j.id) || 0) > start)
    const unique = new Map<string, VideoJob>()
    for (const j of [...(more ? jobs.value : fresh), ...(page.items || [])]) if (!unique.has(j.id)) unique.set(j.id, j)
    jobs.value = [...unique.values()]; cursor.value = page.next_cursor || ''
    if (!more) posterRevision.value += 1
  } catch { if (!lifetime.signal.aborted) { listError.value = true; retryMore.value = more } }
  finally { listing.value = false }
}
function cancelRead() {
  clearTimeout(timer); lookupController?.abort(); lookupController = undefined; checking.value = false
}
function stopTracking() { cancelRead(); tracked.value = ''; auto.value = false; checkError.value = '' }
function schedule() {
  clearTimeout(timer)
  if (auto.value && tracked.value && document.visibilityState === 'visible') timer = setTimeout(readTracked, 6000)
}
async function readTracked() {
  cancelRead()
  const id = tracked.value
  if (!id || document.visibilityState !== 'visible' || lifetime.signal.aborted) return
  const controller = new AbortController(); lookupController = controller
  checking.value = true; checkError.value = ''
  try {
    const job = await videoStudio.get(id, controller.signal)
    if (controller.signal.aborted) return
    if (job.id !== id) throw new Error('原任务号不匹配')
    put(job); emit('resolved', job); auto.value = pending(job); schedule()
  } catch (e) {
    if (!controller.signal.aborted) {
      const error = e as { status?: number; response?: { status?: number } } | null
      checkError.value = (error?.status || error?.response?.status) === 404 ? '暂未找到原任务，请保留任务号稍后核对，不要重复生成。' : '无法读取原任务，请稍后再查询。不会重新提交。'
      auto.value = false
    }
  } finally { if (lookupController === controller) { checking.value = false; lookupController = undefined } }
}
function lookup(id: string) {
  stopTracking(); tracked.value = id; auto.value = true; void readTracked()
  void nextTick(() => trackingPanel.value?.focus())
}
function adopt(job: VideoJob) {
  stopTracking(); put(job); filter.value = 'all'; tracked.value = job.id; auto.value = pending(job); schedule()
}
function focusCard(id: string) { (document.getElementById(`vs-job-${id}`) || galleryHeading.value)?.focus() }
function markExpired(id: string) {
  if (tracked.value === id) { cancelRead(); auto.value = false }
  const job = jobs.value.find(j => j.id === id)
  if (job) put({ ...job, status: 'expired' })
}
function visibility() { if (document.visibilityState !== 'visible') cancelRead(); else if (auto.value) void readTracked() }
onMounted(() => { document.addEventListener('visibilitychange', visibility); void load() })
onBeforeUnmount(() => { lifetime.abort(); stopTracking(); posterCache.dispose(); measuredDurations.clear(); document.removeEventListener('visibilitychange', visibility) })
defineExpose({ lookup, adopt })
</script>
