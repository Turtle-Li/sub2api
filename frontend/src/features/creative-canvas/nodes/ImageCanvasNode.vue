<template>
  <article
    class="cc-node is-image"
    :class="{ 'is-selected': selected, 'is-unavailable': !available }"
    :aria-label="node.title || '图片原图'"
  >
    <Handle type="target" :position="Position.Left" class="cc-port-target" />
    <div class="cc-node-head">
      <span class="cc-pill is-img">图片原图</span>
      <span class="cc-meta">{{ branchCount > 0 ? `${branchCount} 条分支` : '原图保留' }}</span>
    </div>

    <div class="cc-node-body">
      <p class="cc-node-title">{{ node.title || '生图作品' }}</p>

      <div class="cc-thumb-box">
        <img
          v-if="available && thumb.status === 'ready' && thumb.url"
          :src="thumb.url"
          :alt="node.title || '图片缩略图'"
          draggable="false"
        />
        <div v-else class="cc-thumb-placeholder" role="status">
          <template v-if="asset?.available === false || !node.image">
            <strong>素材已失效，可重新选择</strong>
            <span>原位置已保留，不会自动替换</span>
          </template>
          <template v-else-if="!available">
            <strong>正在核验素材权限…</strong>
            <span>等待服务端确认后读取缩略图</span>
          </template>
          <template v-else-if="thumb.status === 'non-first-index'">
            <strong>第 {{ (node.image?.index ?? 0) + 1 }} 张原图</strong>
            <span>进入视频工作台时按需读取真实原图</span>
          </template>
          <template v-else-if="thumb.status === 'loading'">
            <strong>正在读取缩略图…</strong>
          </template>
          <template v-else-if="thumb.status === 'unavailable'">
            <strong>素材已失效，可重新选择</strong>
            <span>缩略图不可用或已过期</span>
          </template>
          <template v-else>
            <strong>暂无可用缩略图</strong>
            <span>提交前将显式读取原图</span>
          </template>
        </div>
        <div v-if="node.image" class="cc-thumb-overlay">
          <span class="cc-media-chip">第 {{ node.image.index + 1 }} 张</span>
        </div>
      </div>

      <p class="cc-meta">
        {{ available ? '可派生多条视频草稿，原图保持不动' : asset?.available === false || !node.image ? '原图当前不可用，无法直接带入工作台' : '正在核验原图权限…' }}
      </p>

      <div class="cc-node-foot nodrag nopan">
        <button
          type="button"
          class="cc-btn is-primary is-sm"
          :disabled="!available"
          @click.stop="emit('createDraft', node.id)"
        >
          + 视频草稿
        </button>
        <button
          v-if="branchCount > 0"
          type="button"
          class="cc-btn is-sm"
          @click.stop="emit('compare', node.id)"
        >
          对比分支 ({{ branchCount }})
        </button>
      </div>
    </div>

    <Handle type="source" :position="Position.Right" class="cc-port-source" />
    <span v-if="available" class="cc-port-stem" aria-hidden="true"></span>
    <button
      v-if="available"
      type="button"
      class="cc-quick-branch nodrag nopan"
      title="从此图新建一条视频草稿分支"
      aria-label="从此图新建一条视频草稿分支"
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
import type { CanvasImageThumbCache } from '../imageThumbCache'
import type { CanvasAssetStatus, CanvasNodeDoc } from '../types'

const props = defineProps<{
  node: CanvasNodeDoc
  asset?: CanvasAssetStatus
  selected: boolean
  branchCount: number
  thumbCache: CanvasImageThumbCache
}>()

const emit = defineEmits<{
  createDraft: [sourceNodeId: string]
  compare: [parentNodeId: string]
}>()

const available = computed(() => props.asset?.available === true && Boolean(props.node.image))
const thumb = computed(() => props.thumbCache.get(props.node.image))

function ensureThumb() {
  void props.thumbCache.ensure(props.node.image, available.value)
}

onMounted(ensureThumb)
watch(
  () => [props.node.image?.route, props.node.image?.batchId, props.node.image?.itemId, props.node.image?.index, available.value],
  ensureThumb,
)
</script>
