<template>
  <div class="video-studio vs-workspace">
    <VideoComposer ref="composer" :source="source" :user-id="userId" :mobile="mobile" :held-id="heldId"
      :failed="heldStatus === 'failed'" :unconfirmed="!!heldId && outstanding.includes(heldId)" :example="example" :reserve="reserve"
      @accepted="accept" @rejected="release" @new-draft="newDraft" @lookup="lookup" />
    <VideoGallery ref="gallery" :example="example" :mobile="mobile" :outstanding="outstanding" @resolved="settle" @example="useExample" @reuse="reuseJob" />
    <div v-if="mobile" class="vs-mobile-create">
      <span>{{ heldId ? '原任务已保留，展开后可查询状态' : '描述画面主体、运镜和节奏…' }}</span>
      <button ref="launcher" type="button" class="vs-primary" aria-haspopup="dialog" aria-controls="vs-composer"
        @click="composer?.open(launcher)">{{ heldId ? '查看提交' : '展开 ↑' }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, shallowRef } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import { pendingImage } from '@/features/video-studio/source'
import type { VideoJob } from '@/features/video-studio/api'
import VideoComposer from './VideoComposer.vue'
import VideoGallery from './VideoGallery.vue'
import './studio.css'

const props = defineProps<{ userId: number }>()
const desktop = useMediaQuery('(min-width: 821px)')
const mobile = computed(() => !desktop.value)
const composer = ref<InstanceType<typeof VideoComposer> | null>(null)
const gallery = ref<InstanceType<typeof VideoGallery> | null>(null)
const launcher = ref<HTMLButtonElement>()
const incoming = pendingImage(props.userId)
const source = shallowRef(incoming?.userId === props.userId ? incoming : null)
const example = '镜头缓慢推近，杯口的蒸汽向窗边飘，杯身的图案保持不变。'

// A reload between POST and its receipt must offer GET by the same ID, not a fresh paid intent.
// Only IDs live in tab storage; the list and every task status still come from the server.
const key = `sub2:video-studio:intents:${props.userId}`
const heldId = ref('')
const heldStatus = ref<VideoJob['status']>()
const outstanding = ref<string[]>([])
const isId = (value: unknown): value is string => typeof value === 'string' && /^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i.test(value)
try {
  const state = JSON.parse(sessionStorage.getItem(key) || '{}')
  heldId.value = isId(state.held) ? state.held : ''
  if (Array.isArray(state.pending)) outstanding.value = state.pending.filter(isId)
} catch { /* Private browsing can make session storage unavailable. */ }
function remember() {
  try { sessionStorage.setItem(key, JSON.stringify({ held: heldId.value, pending: outstanding.value })); return true } catch { return false }
}
// The Image Studio action starts a separate, unpaid draft; any unresolved original stays queryable.
if (source.value && heldId.value) { heldId.value = ''; remember() }
function reserve(id: string) {
  const previous = outstanding.value
  heldId.value = id; heldStatus.value = undefined
  outstanding.value = [id, ...previous.filter(item => item !== id)]
  if (!remember()) {
    heldId.value = ''; outstanding.value = previous
    throw new Error('无法保存原任务号。请允许此站点使用标签页存储后再试；未提交视频。')
  }
}
function resolve(id: string) {
  outstanding.value = outstanding.value.filter(item => item !== id)
  remember()
}
function release(id: string) {
  if (heldId.value === id) heldId.value = ''
  resolve(id)
}
function settle(job: VideoJob) {
  if (heldId.value === job.id) heldStatus.value = job.status
  if (job.status !== 'uncertain') resolve(job.id)
}
function accept(job: VideoJob) {
  settle(job)
  gallery.value?.adopt(job)
  composer.value?.close()
}
function newDraft() {
  heldId.value = ''
  remember()
}
function lookup(id: string) {
  composer.value?.close()
  gallery.value?.lookup(id)
}
function useExample(opener: HTMLElement) { composer.value?.useExample(opener) }
function reuseJob(job: VideoJob, opener?: HTMLElement) { composer.value?.reuse(job, opener) }
onMounted(() => { if (mobile.value && source.value && !heldId.value) composer.value?.open(launcher.value) })
</script>
