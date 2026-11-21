<template>
  <div ref="root" class="vs-poster" :class="{ 'has-frame': url }">
    <img v-if="url && failure !== 'decode'" :src="url" alt="已生成视频的真实封面帧" @error="decodeFailed">
    <div v-else class="vs-poster-placeholder" :class="{ 'is-error': failure && failure !== 'empty' }" role="status">
      <Icon :name="failure && failure !== 'empty' ? 'exclamationCircle' : 'play'" size="lg" aria-hidden="true" />
      <strong>{{ placeholderTitle }}</strong>
      <span>{{ placeholderDetail }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { videoStudio, type VideoJob } from '@/features/video-studio/api'
import type { VideoPosterCache } from './posterCache'

type Failure = '' | 'decode' | 'unauthorized' | 'expired' | 'empty' | 'error'
const props = defineProps<{ job: VideoJob; cache: VideoPosterCache; revision: number }>()
const root = ref<HTMLElement>()
const failure = ref<Failure>('')
const loading = ref(false)
let observer: IntersectionObserver | undefined
let controller: AbortController | undefined
let entered = false
let release: () => void = () => {}

const expiryValid = computed(() => {
  if (props.job.poster_expires_at === undefined) return true
  const expiresAt = Date.parse(props.job.poster_expires_at)
  return !Number.isNaN(expiresAt) && expiresAt > Date.now()
})
const available = computed(() => props.job.status === 'completed' && props.job.has_poster === true && expiryValid.value)
const url = computed(() => props.cache.url(props.job.id))
const placeholderTitle = computed(() => {
  if (failure.value === 'unauthorized') return '封面授权不可用'
  if (failure.value === 'expired') return '封面已过期'
  if (failure.value === 'decode') return '封面图片无法解码'
  if (failure.value === 'error') return '封面帧读取失败'
  if (failure.value === 'empty') return '暂无可用封面帧'
  if (url.value) return ''
  if (loading.value) return '正在读取封面帧…'
  if (props.job.status === 'completed') return '历史成片暂无封面'
  return ({ submitting: '正在提交原任务', queued: '任务正在排队', running: '正在生成视频', failed: '本次未生成成片', uncertain: '提交结果待核实', expired: '成片已经过期' } as Partial<Record<VideoJob['status'], string>>)[props.job.status] || '暂无封面帧'
})
const placeholderDetail = computed(() => {
  if (failure.value === 'unauthorized') return '当前登录或原任务授权已失效'
  if (failure.value === 'expired') return '封面有效期已结束，可手动尝试播放'
  if (failure.value === 'decode' || failure.value === 'error') return '可手动刷新重试，不会自动重复请求'
  if (props.job.status === 'completed') return '仍可手动加载播放'
  if (['submitting', 'queued', 'running'].includes(props.job.status)) return '真实封面将在成片后提供'
  if (props.job.status === 'uncertain') return '请按原任务号查询，不要重发'
  return '这里不会使用参考图或虚构封面'
})

function resetRead() {
  observer?.disconnect(); observer = undefined
  controller?.abort(); controller = undefined
  loading.value = false; failure.value = ''; entered = false
}
async function load(force = false) {
  if (!available.value || controller || (!force && url.value)) return
  const job = props.job
  const read = new AbortController()
  controller = read; loading.value = true; failure.value = ''
  try {
    const blob = await videoStudio.poster(job.id, read.signal)
    if (read.signal.aborted) return
    const objectUrl = URL.createObjectURL(blob)
    if (!props.cache.put(job.id, objectUrl, job.poster_expires_at)) failure.value = 'expired'
  } catch (cause) {
    if (read.signal.aborted) return
    const error = cause as { status?: number; response?: { status?: number } } | null
    const status = error?.status || error?.response?.status
    if (status === 401 || status === 403) {
      failure.value = 'unauthorized'; props.cache.invalidate(job.id)
    } else if (status === 410) {
      failure.value = 'expired'; props.cache.invalidate(job.id)
    } else if (status === 404) {
      failure.value = 'empty'; props.cache.invalidate(job.id)
    } else failure.value = 'error'
  } finally {
    if (controller === read) { controller = undefined; loading.value = false }
  }
}
function observe() {
  if (!root.value) return
  if (props.job.status === 'completed' && props.job.has_poster === true && !expiryValid.value) {
    failure.value = 'expired'; props.cache.invalidate(props.job.id); return
  }
  if (!available.value) return
  if (typeof IntersectionObserver === 'undefined') { entered = true; void load(); return }
  observer = new IntersectionObserver(entries => {
    if (!entries.some(entry => entry.isIntersecting)) return
    observer?.disconnect(); observer = undefined
    entered = true
    void load()
  }, { rootMargin: '240px 0px', threshold: 0.01 })
  observer.observe(root.value)
}
function decodeFailed() {
  if (!url.value) return
  failure.value = 'decode'
  props.cache.invalidate(props.job.id)
}

watch(url, (current, previous) => {
  const releasePrevious = release
  release = current ? props.cache.retain(props.job.id) : () => {}
  releasePrevious()
  if (current) failure.value = ''
  else if (previous && props.job.poster_expires_at && Date.parse(props.job.poster_expires_at) <= Date.now()) failure.value = 'expired'
}, { flush: 'post', immediate: true })
watch(() => [props.job.id, props.job.status, props.job.has_poster, props.job.poster_expires_at] as const, (current, previous) => {
  if (previous && current.every((value, index) => value === previous[index])) return
  if (previous?.[0] && previous[0] !== current[0]) props.cache.invalidate(previous[0])
  if (previous && previous[0] === current[0] && previous.slice(1).some((value, index) => value !== current[index + 1])) props.cache.invalidate(current[0])
  resetRead()
  observe()
})
watch(() => props.revision, () => {
  if (!entered || !available.value) return
  controller?.abort(); controller = undefined
  void load(true)
})
onMounted(observe)
onBeforeUnmount(() => { resetRead(); props.cache.invalidate(props.job.id); release() })
</script>
