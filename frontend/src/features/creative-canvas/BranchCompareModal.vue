<template>
  <div
    v-if="open"
    ref="backdropRef"
    class="cc-modal-backdrop"
    role="dialog"
    aria-modal="true"
    aria-labelledby="cc-compare-title"
    @click.self="emit('close')"
  >
    <div ref="cardRef" class="cc-modal-card" tabindex="-1">
      <div class="cc-modal-head">
        <div>
          <h3 id="cc-compare-title">分支对比 · {{ heading }}</h3>
          <p class="cc-meta">
            原图保留在画布中，共 {{ branches.length }} 条视频分支。切换版本仅修改本分支的展示钉选，不会触发生成或扣费。
          </p>
        </div>
        <button ref="closeBtnRef" type="button" class="cc-btn is-sm" @click="emit('close')">关闭</button>
      </div>

      <div v-if="branches.length" class="cc-compare-grid">
        <section v-for="item in branches" :key="item.node.id" class="cc-compare-item">
          <div class="cc-thumb-box">
            <template v-if="item.activeTake?.job && !item.activeTake.unavailable">
              <VideoPoster
                :job="item.activeTake.job"
                :cache="posterCache"
                :revision="posterRevision"
              />
              <div class="cc-thumb-overlay">
                <span v-if="durationFor(item.activeTake.job)" class="cc-media-chip">
                  {{ durationFor(item.activeTake.job) }}
                </span>
                <span v-if="item.activeTake.job.size" class="cc-media-chip">
                  {{ item.activeTake.job.size }}
                </span>
              </div>
            </template>
            <img
              v-else-if="!item.activeTake?.unavailable && sourceThumbUrl"
              :src="sourceThumbUrl"
              :alt="item.node.title || '草稿封面'"
              draggable="false"
            />
            <div v-else class="cc-thumb-placeholder">
              <strong>
                {{
                  item.activeTake?.unavailable
                    ? '素材授权已失效'
                    : item.activeTake
                      ? '尚无已确认回执'
                      : '本地草稿 · 未提交'
                }}
              </strong>
              <span>
                {{
                  item.activeTake?.unavailable
                    ? '原记录已过期或失去访问权限'
                    : item.activeTake
                      ? '未收到成片记录'
                      : '去工作台确认报价后生成'
                }}
              </span>
            </div>
          </div>

          <div class="cc-kv"><b>{{ item.node.title || '视频草稿' }}</b></div>
          <p class="cc-meta">{{ item.node.prompt || '未填写运镜描述' }}</p>

          <div class="cc-kv">
            <b>状态：</b>{{ statusText(item.activeTake) }}
          </div>
          <div v-if="item.activeTake?.job?.model && !item.activeTake.unavailable" class="cc-kv">
            <b>模型：</b>{{ item.activeTake.job.model }}
          </div>

          <div v-if="item.takes.length > 1" class="cc-takes-strip">
            <button
              v-for="take in item.takes"
              :key="take.requestId"
              type="button"
              class="cc-take-chip"
              :class="{ 'is-active': take.isActive }"
              @click="emit('pinTake', item.node.id, take.requestId)"
            >
              v{{ take.takeNumber }}{{ !take.unavailable && take.job?.status === 'completed' ? ' ✓' : take.unavailable ? ' !' : '' }}
            </button>
          </div>

          <div class="cc-node-foot">
            <button
              v-if="item.activeTake?.job?.status === 'completed' && !item.activeTake.unavailable"
              type="button"
              class="cc-btn is-primary is-sm"
              @click="emit('previewJob', item.activeTake.job, $event.currentTarget as HTMLElement, item.node.id)"
            >
              查看成片
            </button>
            <button
              type="button"
              class="cc-btn is-sm"
              :class="{ 'is-primary': !(item.activeTake?.job?.status === 'completed' && !item.activeTake.unavailable) }"
              @click="emit('openInVideoStudio', item.node.id)"
            >
              前往工作台确认生成
            </button>
          </div>
        </section>
      </div>

      <p v-else class="cc-meta">
        该原图下暂无派生视频草稿。可在图片节点点击「＋」或拖出连线到空白处创建多条分支。
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import type { VideoJob } from '@/features/video-studio/api'
import VideoPoster from '@/features/video-studio/VideoPoster.vue'
import type { VideoPosterCache } from '@/features/video-studio/posterCache'
import { formatVideoDuration, measuredDurationMs, type WorkspaceDurationStore } from '@/features/video-studio/mediaDuration'
import type { CanvasNodeDoc, CanvasTake } from './types'

export interface CompareBranchEntry {
  readonly node: CanvasNodeDoc
  readonly takes: readonly CanvasTake[]
  readonly activeTake: CanvasTake | null
}

const props = defineProps<{
  open: boolean
  heading: string
  sourceThumbUrl: string
  branches: readonly CompareBranchEntry[]
  posterCache: VideoPosterCache
  posterRevision: number
  durations: WorkspaceDurationStore
}>()

const emit = defineEmits<{
  close: []
  pinTake: [draftNodeId: string, requestId: string]
  openInVideoStudio: [draftNodeId: string]
  previewJob: [job: VideoJob, trigger: HTMLElement, nodeId?: string]
}>()

const backdropRef = ref<HTMLElement>()
const cardRef = ref<HTMLElement>()
const closeBtnRef = ref<HTMLButtonElement>()
let previouslyFocused: HTMLElement | null = null

function isTopmostDialog(): boolean {
  const el = backdropRef.value
  if (!el || typeof document === 'undefined') return true
  const dialogs = Array.from(document.querySelectorAll('dialog[open], [role="dialog"]'))
  return dialogs.length === 0 || dialogs[dialogs.length - 1] === el
}

function onKeydown(event: KeyboardEvent) {
  if (!props.open || event.key !== 'Escape' || event.defaultPrevented) return
  if (!isTopmostDialog()) return
  event.preventDefault()
  event.stopPropagation()
  emit('close')
}

function restoreFocus() {
  const target = previouslyFocused
  previouslyFocused = null
  if (target && target.isConnected) {
    target.focus()
  }
}

watch(
  () => props.open,
  async isOpen => {
    if (typeof window === 'undefined') return
    if (isOpen) {
      previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null
      window.addEventListener('keydown', onKeydown)
      await nextTick()
      ;(closeBtnRef.value ?? cardRef.value)?.focus()
    } else {
      window.removeEventListener('keydown', onKeydown)
      restoreFocus()
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  if (typeof window !== 'undefined') {
    window.removeEventListener('keydown', onKeydown)
  }
  restoreFocus()
})

function durationFor(job: VideoJob): string {
  return formatVideoDuration(measuredDurationMs(job, props.durations.get(job.id)))
}

function statusText(take: CanvasTake | null): string {
  if (!take) return '本地草稿（未提交、不扣费）'
  if (take.unavailable) return '素材授权已失效或已过期'
  if (!take.job) return '已记录尝试（尚无已确认回执）'
  const map: Record<VideoJob['status'], string> = {
    submitting: '提交中',
    queued: '排队中',
    running: '生成中',
    completed: '已完成',
    failed: '生成失败',
    uncertain: '状态待核实',
    expired: '已过期',
  }
  return map[take.job.status] || take.job.status
}
</script>
