<template>
  <article
    class="cc-node"
    :class="{ 'is-selected': selected, 'is-unavailable': !available }"
    :aria-label="node.title || '视频作品'"
  >
    <div class="cc-node-head">
      <span class="cc-pill is-vid">视频作品</span>
      <span class="cc-meta">{{ statusLabel }}</span>
    </div>

    <div class="cc-node-body">
      <p class="cc-node-title">{{ node.title || modelLabel }}</p>

      <div class="cc-thumb-box">
        <template v-if="available && job">
          <VideoPoster :job="job" :cache="posterCache" :revision="posterRevision" />
          <div class="cc-thumb-overlay">
            <span v-if="durationLabel" class="cc-media-chip">{{ durationLabel }}</span>
            <span v-if="job.size" class="cc-media-chip">{{ job.size }}</span>
          </div>
        </template>
        <div v-else class="cc-thumb-placeholder" role="status">
          <template v-if="asset?.available === false">
            <strong>素材已失效，可重新选择</strong>
            <span>原位置已保留，不自动拉取视频</span>
          </template>
          <template v-else>
            <strong>正在核验视频权限…</strong>
            <span>等待服务端确认后加载</span>
          </template>
        </div>
      </div>

      <p class="cc-meta">
        {{ metaLine }}
      </p>

      <div class="cc-node-foot nodrag nopan">
        <button
          v-if="available && job"
          type="button"
          class="cc-btn is-primary is-sm"
          @click.stop="emit('previewJob', job, $event.currentTarget as HTMLElement)"
        >
          {{ job.status === 'completed' ? '播放 / 详情' : '查看详情' }}
        </button>
      </div>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { VideoJob } from '@/features/video-studio/api'
import VideoPoster from '@/features/video-studio/VideoPoster.vue'
import type { VideoPosterCache } from '@/features/video-studio/posterCache'
import { formatVideoDuration, measuredDurationMs, type WorkspaceDurationStore } from '@/features/video-studio/mediaDuration'
import type { CanvasAssetStatus, CanvasNodeDoc } from '../types'

const props = defineProps<{
  node: CanvasNodeDoc
  asset?: CanvasAssetStatus
  selected: boolean
  posterCache: VideoPosterCache
  posterRevision: number
  durations: WorkspaceDurationStore
}>()

const emit = defineEmits<{
  previewJob: [job: VideoJob, trigger: HTMLElement]
}>()

const job = computed(() => props.asset?.job ?? null)
const available = computed(() => props.asset?.available === true && Boolean(job.value))

const modelLabel = computed(() => {
  const id = job.value?.model || ''
  return ({ 'minimax/h3': 'H3', 'minimax/h3-fast': 'H3 Fast', 'muse-video': 'Muse' } as Record<string, string>)[id] || id || '视频作品'
})

const durationLabel = computed(() => {
  if (!available.value || !job.value) return ''
  return formatVideoDuration(measuredDurationMs(job.value, props.durations.get(job.value.id)))
})

const statusLabel = computed(() => {
  if (props.asset?.available === false) return '已失效'
  if (!available.value || !job.value) return '待核验'
  const map: Record<VideoJob['status'], string> = {
    submitting: '提交中',
    queued: '排队中',
    running: '生成中',
    completed: '已完成',
    failed: '失败',
    uncertain: '待核实',
    expired: '已过期',
  }
  return map[job.value.status] || job.value.status
})

const metaLine = computed(() => {
  if (props.asset?.available === false) return '该视频记录已过期或无权限读取。'
  if (!available.value || !job.value) return '正在核验视频访问权限…'
  const parts = [modelLabel.value]
  if (durationLabel.value) parts.push(durationLabel.value)
  if (job.value.size) parts.push(job.value.size)
  return parts.join(' · ')
})
</script>
