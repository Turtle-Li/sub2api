<template>
  <article
    class="cc-node is-draft"
    :class="[
      branchClass,
      {
        'is-selected': selected,
        'is-completed': isCompleted,
        'is-unavailable': activeTake?.unavailable || inputUnavailable,
      },
    ]"
    :aria-label="node.title || '视频草稿'"
  >
    <Handle type="target" :position="Position.Left" class="cc-port-target" />

    <div class="cc-node-head">
      <div class="cc-head-badges">
        <span class="cc-pill" :class="statusPillClass">{{ statusLabel }}</span>
        <span v-if="activeTake" class="cc-version-tag">v{{ activeTake.takeNumber }}</span>
      </div>
      <span class="cc-branch-tag">{{ branchLabel }}</span>
    </div>

    <div class="cc-node-body">
      <div class="cc-draft-title-row">
        <p class="cc-node-title">{{ node.title || '视频草稿' }}</p>
        <span class="cc-provenance-chip" :class="{ 'is-warn': inputUnavailable }">{{ provenanceBadge }}</span>
      </div>

      <button
        v-if="isCompleted && activeJob"
        type="button"
        class="cc-thumb-box is-interactive nodrag nopan"
        :aria-label="`在画布内查看「${node.title || '视频草稿'}」v${activeTake?.takeNumber ?? 1} 成片详情`"
        @click.stop="emit('previewJob', activeJob, $event.currentTarget as HTMLElement, node.id)"
      >
        <VideoPoster :job="activeJob" :cache="posterCache" :revision="posterRevision" />
        <span class="cc-thumb-play-badge" aria-hidden="true">
          <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor"><path d="M5.5 3.8 12 8l-6.5 4.2V3.8Z" /></svg>
        </span>
        <div class="cc-thumb-overlay">
          <span v-if="durationLabel" class="cc-media-chip">{{ durationLabel }}</span>
          <span v-if="activeJob.size" class="cc-media-chip">{{ activeJob.size }}</span>
        </div>
      </button>

      <button
        v-else
        type="button"
        class="cc-thumb-box is-draft-stage nodrag nopan"
        :aria-label="`查看「${node.title || '视频草稿'}」草稿详情与来源`"
        @click.stop="emit('inspectDraft', node.id, $event.currentTarget as HTMLElement)"
      >
        <template v-if="activeJob && !activeTake?.unavailable">
          <VideoPoster :job="activeJob" :cache="posterCache" :revision="posterRevision" />
          <div class="cc-thumb-overlay">
            <span v-if="durationLabel" class="cc-media-chip">{{ durationLabel }}</span>
            <span v-if="activeJob.size" class="cc-media-chip">{{ activeJob.size }}</span>
          </div>
        </template>
        <img
          v-else-if="!activeTake?.unavailable && !inputUnavailable && sourceAvailable && sourceThumb.status === 'ready' && sourceThumb.url"
          :src="sourceThumb.url"
          :alt="sourceNode?.title || '来源原图'"
          draggable="false"
        />
        <div v-else class="cc-thumb-placeholder" role="status">
          <template v-if="activeTake?.unavailable">
            <strong>成片授权已失效</strong>
            <span>原视频记录已过期或失去访问权限</span>
          </template>
          <template v-else-if="inputUnavailable">
            <strong>上游来源已失效</strong>
            <span>关联原图或固定成片不可用，已阻止无图提交</span>
          </template>
          <template v-else-if="activeTake && !activeTake.job">
            <strong>尚无已确认回执</strong>
            <span>已记录本次提交，请按原任务核对</span>
          </template>
          <template v-else-if="sourceAuthUnknown">
            <strong>正在核验来源权限…</strong>
            <span>等待服务端确认后操作</span>
          </template>
          <template v-else-if="sourceNode && sourceThumb.status === 'non-first-index'">
            <strong>源自第 {{ (sourceNode.image?.index ?? 0) + 1 }} 张原图</strong>
            <span>确认生成时按需读取原图</span>
          </template>
          <template v-else>
            <strong>视频草稿 · 未提交</strong>
            <span>点击查看草稿与来源，去工作台确认后生成</span>
          </template>
        </div>
      </button>

      <p v-if="node.prompt" class="cc-prompt-preview">{{ node.prompt }}</p>
      <p v-else class="cc-meta">暂未填写运镜描述，点击查看草稿或前往工作台编辑。</p>

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
          v-if="isCompleted && activeJob"
          type="button"
          class="cc-btn is-primary is-sm"
          @click.stop="emit('previewJob', activeJob, $event.currentTarget as HTMLElement, node.id)"
        >
          查看成片
        </button>
        <button
          v-else-if="activeJob && !activeTake?.unavailable"
          type="button"
          class="cc-btn is-sm"
          @click.stop="emit('previewJob', activeJob, $event.currentTarget as HTMLElement, node.id)"
        >
          任务状态
        </button>
        <button
          v-else
          type="button"
          class="cc-btn is-sm"
          @click.stop="emit('inspectDraft', node.id, $event.currentTarget as HTMLElement)"
        >
          {{ node.prompt ? '编辑草稿' : '编辑描述' }}
        </button>

        <button
          type="button"
          class="cc-btn is-sm"
          :class="{ 'is-primary': !isCompleted }"
          :disabled="sourceAuthUnknown || inputUnavailable"
          @click.stop="emit('openInVideoStudio', node.id)"
        >
          前往工作台确认生成
        </button>

        <button
          v-if="compareParentId"
          type="button"
          class="cc-btn is-sm"
          :disabled="sourceAuthUnknown"
          @click.stop="emit('compare', compareParentId)"
        >
          同源对比
        </button>
      </div>
    </div>

    <Handle type="source" :position="Position.Right" class="cc-port-source" />
    <span v-if="canBranch" class="cc-port-stem" aria-hidden="true"></span>
    <button
      v-if="canBranch"
      type="button"
      class="cc-quick-branch nodrag nopan"
      :title="quickBranchLabel"
      :aria-label="quickBranchLabel"
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
import { computed, onMounted, watch } from 'vue'
import { Handle, Position } from '@xyflow/vue'
import type { VideoJob } from '@/features/video-studio/api'
import VideoPoster from '@/features/video-studio/VideoPoster.vue'
import type { VideoPosterCache } from '@/features/video-studio/posterCache'
import { formatVideoDuration, measuredDurationMs, type WorkspaceDurationStore } from '@/features/video-studio/mediaDuration'
import type { CanvasImageThumbCache } from '../imageThumbCache'
import type { CanvasAssetStatus, CanvasNodeDoc, CanvasResolvedInput, CanvasTake } from '../types'

const props = defineProps<{
  node: CanvasNodeDoc
  sourceNode: CanvasNodeDoc | null
  sourceAsset?: CanvasAssetStatus
  resolvedInput?: CanvasResolvedInput
  provenanceBadge: string
  compareParentId: string | null
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
  compare: [parentNodeId: string]
  pinTake: [draftNodeId: string, requestId: string]
  previewJob: [job: VideoJob, trigger: HTMLElement, nodeId: string]
  inspectDraft: [draftNodeId: string, trigger: HTMLElement]
  createDraft: [sourceNodeId: string]
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
const inputUnavailable = computed(() =>
  props.resolvedInput?.available === false ||
  (Boolean(props.sourceNode) && props.sourceAsset?.available === false),
)
const canBranch = computed(() => isCompleted.value || !inputUnavailable.value)
const quickBranchLabel = computed(() =>
  isCompleted.value
    ? `用这版成片（v${props.activeTake?.takeNumber ?? 1}）尾帧续接下一镜`
    : '沿用此镜素材续接下一镜',
)
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
  if (props.activeTake?.unavailable || inputUnavailable.value) return 'is-error'
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
