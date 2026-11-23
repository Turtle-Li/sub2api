<template>
  <aside v-if="node" class="cc-inspector" aria-live="polite" aria-label="节点属性面板">
    <div class="cc-inspector-head">
      <h3>{{ node.title || kindLabel }}</h3>
      <div class="cc-head-badges">
        <button type="button" class="cc-btn is-sm" title="将视口居中到当前节点" @click="emit('locateNode', node.id)">
          定位
        </button>
        <button type="button" class="cc-btn is-sm" aria-label="收起属性面板" @click="emit('close')">
          收起
        </button>
      </div>
    </div>

    <div class="cc-kv"><b>类型：</b>{{ kindLabel }}</div>

    <div v-if="upstreamChain.length" class="cc-upstream-box" role="navigation" aria-label="上游分镜溯源">
      <div class="cc-kv"><b>上游链路：</b></div>
      <div class="cc-upstream-trail">
        <template v-for="(anc, idx) in upstreamChain" :key="anc.id">
          <button
            type="button"
            class="cc-upstream-chip"
            :title="`定位到上游节点：${anc.title || anc.kind}`"
            @click="emit('locateNode', anc.id)"
          >
            {{ anc.title || fallbackTitle(anc.kind) }}
          </button>
          <span v-if="idx < upstreamChain.length" class="cc-upstream-sep" aria-hidden="true">→</span>
        </template>
        <span class="cc-upstream-current">当前节点</span>
      </div>
    </div>

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
        <b>素材来源：</b>{{ provenanceDetail }}
      </div>
      <div v-if="inputUnavailable" class="cc-hint-box is-warn" role="alert">
        <b>来源不可用：</b>关联原图或固定成片已过期或失去访问权限，不会降级为无图付费提交。
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

      <div class="cc-kv"><b>下游续镜：</b>{{ branchCount }} 条子分镜</div>

      <div class="cc-node-foot">
        <button
          v-if="activeCompletedJob"
          type="button"
          class="cc-btn is-primary is-sm"
          @click="emit('previewJob', activeCompletedJob, $event.currentTarget as HTMLElement, node.id)"
        >
          查看当前成片
        </button>
        <button
          type="button"
          class="cc-btn is-sm"
          :class="{ 'is-primary': !activeCompletedJob }"
          :disabled="inputUnavailable"
          @click="emit('openInVideoStudio', node.id)"
        >
          前往视频工作台确认生成
        </button>
        <button
          type="button"
          class="cc-btn is-sm"
          :disabled="inputUnavailable && !activeCompletedJob"
          @click="emit('createDraft', node.id)"
        >
          {{ activeCompletedJob ? `+ 用 v${activeTakeNumber} 尾帧续镜` : '+ 沿用素材建下一镜' }}
        </button>
        <button
          v-if="compareParentId"
          type="button"
          class="cc-btn is-sm"
          @click="emit('compare', compareParentId)"
        >
          同源对比
        </button>
        <button
          v-if="branchCount > 1"
          type="button"
          class="cc-btn is-sm"
          @click="emit('compare', node.id)"
        >
          对比子续镜 ({{ branchCount }})
        </button>
      </div>
    </template>

    <template v-else-if="node.kind === 'video'">
      <div class="cc-kv">
        <b>状态：</b>{{ videoAvailable && asset?.job ? asset.job.status : asset?.available === false ? '素材已失效（位置保留）' : '正在核验素材权限…' }}
      </div>
      <div v-if="videoDuration" class="cc-kv"><b>实际时长：</b>{{ videoDuration }}</div>
      <div class="cc-kv"><b>已派生续镜：</b>{{ branchCount }} 条子分镜</div>
      <div v-if="videoAvailable && asset?.job" class="cc-node-foot">
        <button
          type="button"
          class="cc-btn is-primary is-sm"
          @click="emit('previewJob', asset.job, $event.currentTarget as HTMLElement, node.id)"
        >
          查看成片
        </button>
        <button
          v-if="asset.job.status === 'completed'"
          type="button"
          class="cc-btn is-sm"
          @click="emit('createDraft', node.id)"
        >
          + 用尾帧续接下一镜
        </button>
        <button
          v-if="branchCount > 0"
          type="button"
          class="cc-btn is-sm"
          @click="emit('compare', node.id)"
        >
          对比续镜 ({{ branchCount }})
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
      <b>安全提示：</b>画布移动、连线、切换版本与保存均不产生费用；只有在「视频工作台」核对实时报价并手动确认后才会提交生成。
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { VideoJob } from '@/features/video-studio/api'
import { formatVideoDuration, measuredDurationMs, type WorkspaceDurationStore } from '@/features/video-studio/mediaDuration'
import type { CanvasAssetStatus, CanvasNodeDoc, CanvasNodeKind, CanvasResolvedInput, CanvasTake } from './types'

const props = withDefaults(defineProps<{
  node: CanvasNodeDoc | null
  asset?: CanvasAssetStatus
  resolvedInput?: CanvasResolvedInput
  sourceNode: CanvasNodeDoc | null
  upstreamChain?: readonly CanvasNodeDoc[]
  provenanceDetail?: string
  compareParentId?: string | null
  branchCount: number
  takes: readonly CanvasTake[]
  durations: WorkspaceDurationStore
}>(), {
  upstreamChain: () => [],
  provenanceDetail: '无（纯文字草稿）',
  compareParentId: null,
})

const emit = defineEmits<{
  close: []
  locateNode: [nodeId: string]
  updateTitle: [nodeId: string, title: string]
  updatePrompt: [nodeId: string, prompt: string]
  commitEdit: []
  createDraft: [sourceNodeId: string]
  compare: [parentNodeId: string]
  openInVideoStudio: [draftNodeId: string]
  pinTake: [draftNodeId: string, requestId: string]
  previewJob: [job: VideoJob, trigger: HTMLElement, nodeId?: string]
  removeNode: [nodeId: string]
}>()

const kindLabel = computed(() => {
  const kind = props.node?.kind
  if (kind === 'image') return '图片原图'
  if (kind === 'video') return '视频作品'
  if (kind === 'video-draft') return '视频分镜草稿'
  return '灵感便签'
})

function fallbackTitle(kind: CanvasNodeKind): string {
  if (kind === 'image') return '图片原图'
  if (kind === 'video') return '视频作品'
  if (kind === 'video-draft') return '上游分镜'
  return '便签'
}

const imageAvailable = computed(() => props.asset?.available === true && Boolean(props.node?.image))
const videoAvailable = computed(() => props.asset?.available === true && Boolean(props.asset?.job))
const inputUnavailable = computed(() =>
  props.resolvedInput?.available === false ||
  (Boolean(props.sourceNode) && props.asset?.available === false),
)

const activeTake = computed(() => props.takes.find(item => item.isActive) ?? null)
const activeTakeNumber = computed(() => activeTake.value?.takeNumber ?? 1)
const activeCompletedJob = computed(() => {
  const cur = activeTake.value
  if (!cur || cur.unavailable || cur.job?.status !== 'completed') return null
  return cur.job
})

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
