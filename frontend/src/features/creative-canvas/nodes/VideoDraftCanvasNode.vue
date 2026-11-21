<template>
  <article
    class="cc-node is-draft"
    :class="[
      branchClass,
      {
        'is-selected': selected,
        'is-completed': isCompleted,
        'is-unavailable': activeTake?.unavailable,
      },
    ]"
    :aria-label="node.title || '视频草稿'"
  >
    <Handle type="target" :position="Position.Left" />

    <div class="cc-node-head">
      <div class="cc-head-badges">
        <span class="cc-pill" :class="statusPillClass">{{ statusLabel }}</span>
        <span v-if="activeTake" class="cc-version-tag">v{{ activeTake.takeNumber }}</span>
      </div>
      <span class="cc-branch-tag">{{ branchLabel }}</span>
    </div>

    <div class="cc-node-body">
      <p class="cc-node-title">{{ node.title || '视频草稿' }}</p>

      <div class="cc-thumb-box">
        <template v-if="activeJob && !activeTake?.unavailable">
          <VideoPoster :job="activeJob" :cache="posterCache" :revision="posterRevision" />
          <div class="cc-thumb-overlay">
            <span v-if="durationLabel" class="cc-media-chip">{{ durationLabel }}</span>
            <span v-if="activeJob.size" class="cc-media-chip">{{ activeJob.size }}</span>
          </div>
        </template>
        <img
          v-else-if="!activeTake?.unavailable && sourceAvailable && sourceThumb.status === 'ready' && sourceThumb.url"
          :src="sourceThumb.url"
          :alt="sourceNode?.title || '来源原图'"
          draggable="false"
        />
        <div v-else class="cc-thumb-placeholder" role="status">
          <template v-if="activeTake?.unavailable">
            <strong>素材授权已失效</strong>
            <span>原视频记录已过期或失去访问权限</span>
          </template>
          <template v-else-if="activeTake && !activeTake.job">
            <strong>尚无已确认回执</strong>
            <span>已记录本次提交，请按原任务核对</span>
          </template>
          <template v-else-if="sourceAuthUnknown">
            <strong>正在核验来源原图权限…</strong>
            <span>等待服务端确认后操作</span>
          </template>
          <template v-else-if="sourceNode && !sourceAvailable">
            <strong>来源原图已失效</strong>
            <span>可重新选择原图或直接文字生成</span>
          </template>
          <template v-else-if="sourceNode && sourceThumb.status === 'non-first-index'">
            <strong>源自第 {{ (sourceNode.image?.index ?? 0) + 1 }} 张原图</strong>
            <span>确认后在工作台读取原图</span>
          </template>
          <template v-else>
            <strong>视频草稿 · 未提交</strong>
            <span>确认报价后在视频工作台生成</span>
          </template>
        </div>
      </div>

      <p v-if="node.prompt" class="cc-prompt-preview">{{ node.prompt }}</p>
      <p v-else class="cc-meta">暂未填写运镜描述，可在右侧面板或工作台编辑。</p>

      <div v-if="takes.length > 1" class="cc-takes-strip nodrag nopan" role="group" aria-label="成片版本切换">
        <button
          v-for="take in takes"
          :key="take.requestId"
          type="button"
          class="cc-take-chip"
          :class="{ 'is-active': take.isActive }"
          @click.stop="emit('pinTake', node.id, take.requestId)"
        >
          v{{ take.takeNumber }}{{ !take.unavailable && take.job?.status === 'completed' ? ' ✓' : take.unavailable ? ' !' : '' }}
        </button>
      </div>

      <div class="cc-node-foot nodrag nopan">
        <button
          type="button"
          class="cc-btn is-primary is-sm"
          :disabled="sourceAuthUnknown"
          @click.stop="emit('openInVideoStudio', node.id)"
        >
          去工作台确认
        </button>
        <button
          v-if="activeJob && !activeTake?.unavailable && activeJob.status === 'completed'"
          type="button"
          class="cc-btn is-sm"
          @click.stop="emit('previewJob', activeJob, $event.currentTarget as HTMLElement)"
        >
          播放成片
        </button>
        <button
          v-if="sourceNode"
          type="button"
          class="cc-btn is-sm"
          :disabled="sourceAuthUnknown"
          @click.stop="emit('compare', sourceNode.id)"
        >
          同源对比
        </button>
      </div>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { Handle, Position } from '@xyflow/vue'
import type { VideoJob } from '@/features/video-studio/api'
import VideoPoster from '@/features/video-studio/VideoPoster.vue'
import type { VideoPosterCache } from '@/features/video-studio/posterCache'
import { formatVideoDuration, measuredDurationMs, type WorkspaceDurationStore } from '@/features/video-studio/mediaDuration'
import type { CanvasImageThumbCache } from '../imageThumbCache'
import type { CanvasAssetStatus, CanvasNodeDoc, CanvasTake } from '../types'

const props = defineProps<{
  node: CanvasNodeDoc
  sourceNode: CanvasNodeDoc | null
  sourceAsset?: CanvasAssetStatus
  takes: readonly CanvasTake[]
  activeTake: CanvasTake | null
  branchLabel: string
  branchIndex?: number
  selected: boolean
  posterCache: VideoPosterCache
  posterRevision: number
  durations: WorkspaceDurationStore
  thumbCache: CanvasImageThumbCache
}>()

const emit = defineEmits<{
  openInVideoStudio: [draftNodeId: string]
  compare: [imageNodeId: string]
  pinTake: [draftNodeId: string, requestId: string]
  previewJob: [job: VideoJob, trigger: HTMLElement]
}>()

const activeJob = computed(() => props.activeTake?.job ?? null)
const isCompleted = computed(
  () => activeJob.value?.status === 'completed' && !props.activeTake?.unavailable,
)
const branchClass = computed(() =>
  typeof props.branchIndex === 'number' ? `cc-branch-${props.branchIndex % 4}` : '',
)
const sourceAvailable = computed(() => props.sourceAsset?.available === true && Boolean(props.sourceNode?.image))
const sourceAuthUnknown = computed(() => Boolean(props.sourceNode) && props.sourceAsset?.available === undefined)
const sourceThumb = computed(() => props.thumbCache.get(props.sourceNode?.image))

const durationLabel = computed(() => {
  if (!activeJob.value) return ''
  return formatVideoDuration(measuredDurationMs(activeJob.value, props.durations.get(activeJob.value.id)))
})

const statusLabel = computed(() => {
  if (props.activeTake?.unavailable) return '已失效'
  const job = activeJob.value
  if (!job) {
    return props.activeTake ? '尚无回执' : '视频草稿'
  }
  const map: Record<VideoJob['status'], string> = {
    submitting: '提交中',
    queued: '排队中',
    running: '生成中',
    completed: '已成片',
    failed: '生成失败',
    uncertain: '待核实',
    expired: '已过期',
  }
  return map[job.status] || '视频草稿'
})

const statusPillClass = computed(() => {
  if (props.activeTake?.unavailable) return 'is-error'
  const status = activeJob.value?.status
  if (status === 'completed') return 'is-img'
  if (status === 'failed' || status === 'expired') return 'is-error'
  if (status === 'running' || status === 'queued' || status === 'submitting') return 'is-vid'
  return 'is-draft'
})

function ensureSourceThumb() {
  if (props.sourceNode?.image) {
    void props.thumbCache.ensure(props.sourceNode.image, sourceAvailable.value)
  }
}

onMounted(ensureSourceThumb)
watch(() => [props.sourceNode?.id, sourceAvailable.value], ensureSourceThumb)
</script>
