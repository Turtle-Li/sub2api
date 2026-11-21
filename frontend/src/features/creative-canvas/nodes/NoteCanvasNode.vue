<template>
  <article
    class="cc-node"
    :class="{ 'is-selected': selected }"
    :aria-label="node.title || '灵感便签'"
  >
    <div class="cc-node-head">
      <span class="cc-pill">灵感便签</span>
      <span class="cc-meta">可拖动</span>
    </div>

    <div class="cc-node-body nodrag nopan">
      <input
        class="cc-note-input"
        type="text"
        :value="node.title || ''"
        maxlength="120"
        placeholder="便签标题"
        aria-label="便签标题"
        @input="emit('updateTitle', node.id, ($event.target as HTMLInputElement).value)"
        @blur="emit('commitEdit')"
      />
      <textarea
        class="cc-textarea"
        rows="4"
        :value="node.prompt || ''"
        maxlength="4000"
        placeholder="记录镜头构想、节奏或分支对比笔记…"
        aria-label="灵感文字"
        @input="emit('updatePrompt', node.id, ($event.target as HTMLTextAreaElement).value)"
        @blur="emit('commitEdit')"
      />
    </div>
    <Handle type="source" :position="Position.Right" />
  </article>
</template>

<script setup lang="ts">
import { Handle, Position } from '@xyflow/vue'
import type { CanvasNodeDoc } from '../types'

defineProps<{
  node: CanvasNodeDoc
  selected: boolean
}>()

const emit = defineEmits<{
  updateTitle: [nodeId: string, title: string]
  updatePrompt: [nodeId: string, prompt: string]
  commitEdit: []
}>()
</script>
