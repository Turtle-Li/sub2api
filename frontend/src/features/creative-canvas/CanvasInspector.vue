<template>
  <aside v-if="node" class="cc-inspector" aria-live="polite" aria-label="节点属性面板">
    <div class="cc-inspector-head">
      <h3>{{ node.title || kindLabel }}</h3>
      <button type="button" class="cc-btn is-sm" aria-label="收起属性面板" @click="emit('close')">
        收起
      </button>
    </div>

    <div class="cc-kv"><b>类型：</b>{{ kindLabel }}</div>

    <label class="cc-kv" style="display: grid; gap: 4px">
      <b>标题</b>
      <input
        class="cc-note-input"
        type="text"
        maxlength="120"
        :value="node.title || ''"
        @input="emit('updateTitle', node.id, ($event.target as HTMLInputElement).value)"
        @blur="emit('commitEdit')"
      />
    </label>

    <template v-if="node.kind === 'image'">
      <div class="cc-kv">
        <b>原图序号：</b>第 {{ (node.image?.index ?? 0) + 1 }} 张
      </div>
      <div class="cc-kv">
        <b>可用状态：</b>{{ imageAvailable ? '可派生视频草稿' : asset?.available === false || !node.image ? '素材已失效（位置保留）' : '正在核验素材权限…' }}
      </div>
      <div class="cc-kv"><b>已派生草稿：</b>{{ branchCount }} 条分支</div>
      <div class="cc-node-foot">
        <button
          type="button"
          class="cc-btn is-primary is-sm"
          :disabled="!imageAvailable"
          @click="emit('createDraft', node.id)"
        >
          + 新建视频草稿
        </button>
        <button
          v-if="branchCount > 0"
          type="button"
          class="cc-btn is-sm"
          @click="emit('compare', node.id)"
        >
          对比分支 ({{ branchCount }})
        </button>
      </div>
    </template>

    <template v-else-if="node.kind === 'video-draft'">
      <div class="cc-kv">
        <b>来源原图：</b>{{ sourceNode ? `${sourceNode.title || '图片原图'}（原图保留）` : '无（纯文字草稿）' }}
      </div>
      <label class="cc-kv" style="display: grid; gap: 4px">
        <b>运镜与画面描述</b>
        <textarea
          class="cc-textarea"
          rows="4"
          maxlength="4000"
          placeholder="描述镜头推进、机位、节奏与氛围…"
          :value="node.prompt || ''"
          @input="emit('updatePrompt', node.id, ($event.target as HTMLTextAreaElement).value)"
          @blur="emit('commitEdit')"
        />
      </label>

      <div v-if="takes.length" style="display: grid; gap: 6px">
        <div class="cc-kv"><b>本分支提交记录（{{ takes.length }}）</b></div>
        <div class="cc-takes-strip">
          <button
            v-for="take in takes"
            :key="take.requestId"
            type="button"
            class="cc-take-chip"
            :class="{ 'is-active': take.isActive }"
            @click="emit('pinTake', node.id, take.requestId)"
          >
            v{{ take.takeNumber }} · {{ takeStatusLabel(take) }}
          </button>
        </div>
      </div>

      <div class="cc-node-foot">
        <button
          type="button"
          class="cc-btn is-primary is-sm"
          @click="emit('openInVideoStudio', node.id)"
        >
          去视频工作台确认
        </button>
        <button
          v-if="sourceNode"
          type="button"
          class="cc-btn is-sm"
          @click="emit('compare', sourceNode.id)"
        >
          同源对比
        </button>
      </div>
    </template>

    <template v-else-if="node.kind === 'video'">
      <div class="cc-kv">
        <b>状态：</b>{{ videoAvailable && asset?.job ? asset.job.status : asset?.available === false ? '素材已失效（位置保留）' : '正在核验素材权限…' }}
      </div>
      <div v-if="videoDuration" class="cc-kv"><b>实际时长：</b>{{ videoDuration }}</div>
      <div v-if="videoAvailable && asset?.job" class="cc-node-foot">
        <button
          type="button"
          class="cc-btn is-primary is-sm"
          @click="emit('previewJob', asset.job, $event.currentTarget as HTMLElement)"
        >
          播放 / 详情
        </button>
      </div>
    </template>

    <template v-else-if="node.kind === 'note'">
      <label class="cc-kv" style="display: grid; gap: 4px">
        <b>便签内容</b>
        <textarea
          class="cc-textarea"
          rows="4"
          maxlength="4000"
          :value="node.prompt || ''"
          @input="emit('updatePrompt', node.id, ($event.target as HTMLTextAreaElement).value)"
          @blur="emit('commitEdit')"
        />
      </label>
    </template>

    <div class="cc-node-foot" style="padding-top: 6px; border-top: 1px solid var(--cc-line)">
      <button
        type="button"
        class="cc-btn is-danger is-sm"
        title="仅从画布移除引用，不会删除原作品或取消任务"
        @click="emit('removeNode', node.id)"
      >
        从画布移除
      </button>
    </div>

    <div class="cc-hint-box">
      <b>安全提示：</b>画布移动、连线与保存均不产生费用；只有在「视频工作台」核对实时报价并手动确认后才会提交生成。
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { VideoJob } from '@/features/video-studio/api'
import { formatVideoDuration, measuredDurationMs, type WorkspaceDurationStore } from '@/features/video-studio/mediaDuration'
import type { CanvasAssetStatus, CanvasNodeDoc, CanvasTake } from './types'

const props = defineProps<{
  node: CanvasNodeDoc | null
  asset?: CanvasAssetStatus
  sourceNode: CanvasNodeDoc | null
  branchCount: number
  takes: readonly CanvasTake[]
  durations: WorkspaceDurationStore
}>()

const emit = defineEmits<{
  close: []
  updateTitle: [nodeId: string, title: string]
  updatePrompt: [nodeId: string, prompt: string]
  commitEdit: []
  createDraft: [imageNodeId: string]
  compare: [imageNodeId: string]
  openInVideoStudio: [draftNodeId: string]
  pinTake: [draftNodeId: string, requestId: string]
  previewJob: [job: VideoJob, trigger: HTMLElement]
  removeNode: [nodeId: string]
}>()

const kindLabel = computed(() => {
  const kind = props.node?.kind
  if (kind === 'image') return '图片原图'
  if (kind === 'video') return '视频作品'
  if (kind === 'video-draft') return '视频草稿'
  return '灵感便签'
})

const imageAvailable = computed(() => props.asset?.available === true && Boolean(props.node?.image))
const videoAvailable = computed(() => props.asset?.available === true && Boolean(props.asset?.job))

const videoDuration = computed(() => {
  const job = props.asset?.job
  if (!videoAvailable.value || !job) return ''
  return formatVideoDuration(measuredDurationMs(job, props.durations.get(job.id)))
})

function takeStatusLabel(take: CanvasTake): string {
  if (take.unavailable) return '已失效'
  if (!take.job) return '尚无回执'
  const map: Record<VideoJob['status'], string> = {
    submitting: '提交中',
    queued: '排队中',
    running: '生成中',
    completed: '已成片',
    failed: '失败',
    uncertain: '待核实',
    expired: '已过期',
  }
  return map[take.job.status] || take.job.status
}
</script>
