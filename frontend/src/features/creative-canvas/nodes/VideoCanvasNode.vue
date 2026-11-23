<template>
  <article
    class="cc-node is-video"
    :class="{ 'is-selected': selected, 'is-unavailable': !available }"
    :aria-label="node.title || '视频作品'"
  >
    <div class="cc-node-head">
      <span class="cc-pill is-vid">视频作品</span>
      <span class="cc-meta">{{ branchCount > 0 ? `${branchCount} 条续镜` : statusLabel }}</span>
    </div>

    <div class="cc-node-body">
      <p class="cc-node-title">{{ node.title || modelLabel }}</p>

      <button
        v-if="available && job"
        type="button"
        class="cc-thumb-box is-interactive nodrag nopan"
        :aria-label="`在画布内查看「${node.title || modelLabel}」成片详情`"
        @click.stop="emit('previewJob', job, $event.currentTarget as HTMLElement, node.id)"
      >
        <VideoPoster :job="job" :cache="posterCache" :revision="posterRevision" />
        <span class="cc-thumb-play-badge" aria-hidden="true">
          <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor"><path d="M5.5 3.8 12 8l-6.5 4.2V3.8Z" /></svg>
        </span>
        <div class="cc-thumb-overlay">
          <span v-if="durationLabel" class="cc-media-chip">{{ durationLabel }}</span>
          <span v-if="job.size" class="cc-media-chip">{{ job.size }}</span>
        </div>
      </button>
      <div v-else class="cc-thumb-box">
        <div class="cc-thumb-placeholder" role="status">
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

      <p class="cc-meta">{{ metaLine }}</p>

      <div class="cc-node-foot nodrag nopan">
        <button
          v-if="available && job"
          type="button"
          class="cc-btn is-primary is-sm"
          @click.stop="emit('previewJob', job, $event.currentTarget as HTMLElement, node.id)"
        >
          查看成片
        </button>
        <button
          v-if="canBranch"
          type="button"
          class="cc-btn is-sm"
          @click.stop="emit('createDraft', node.id)"
        >
          + 尾帧续镜
        </button>
        <button
          v-if="branchCount > 0"
          type="button"
          class="cc-btn is-sm"
          @click.stop="emit('compare', node.id)"
        >
          对比续镜 ({{ branchCount }})
        </button>
      </div>
    </div>

    <Handle type="source" :position="Position.Right" class="cc-port-source" />
    <span v-if="canBranch" class="cc-port-stem" aria-hidden="true"></span>
    <button
      v-if="canBranch"
      type="button"
      class="cc-quick-branch nodrag nopan"
      title="用此视频成片尾帧续接下一镜"
      aria-label="用此视频成片尾帧续接下一镜"
      @pointerdown.stop
      @mousedown.stop
      @touchstart.stop
      @click.stop="emit('createDraft', node.id)"
    >
      <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
        <path d="M8 3.5v9M3.5 8h9" />
      </svg>
    </button>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Handle, Position } from '@xyflow/vue'
import { videoModelDisplayName, type VideoJob } from '@/features/video-studio/api'
import VideoPoster from '@/features/video-studio/VideoPoster.vue'
import type { VideoPosterCache } from '@/features/video-studio/posterCache'
import { formatVideoDuration, measuredDurationMs, type WorkspaceDurationStore } from '@/features/video-studio/mediaDuration'
import type { CanvasAssetStatus, CanvasNodeDoc } from '../types'

const props = defineProps<{
  node: CanvasNodeDoc
  asset?: CanvasAssetStatus
  selected: boolean
  branchCount: number
  posterCache: VideoPosterCache
  posterRevision: number
  durations: WorkspaceDurationStore
}>()

const emit = defineEmits<{
  previewJob: [job: VideoJob, trigger: HTMLElement, nodeId: string]
  createDraft: [sourceNodeId: string]
  compare: [parentNodeId: string]
}>()

const job = computed(() => props.asset?.job ?? null)
const available = computed(() => props.asset?.available === true && Boolean(job.value))
const canBranch = computed(() => available.value && job.value?.status === 'completed')

const modelLabel = computed(() => videoModelDisplayName(job.value?.model || '') || '视频作品')

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
