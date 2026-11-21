<template>
  <header class="cc-topbar">
    <div class="cc-brand">
      <span class="cc-dot" aria-hidden="true"></span>
      <strong>无限画布</strong>

      <select
        class="cc-board-select"
        aria-label="切换画布"
        :value="board?.id || ''"
        :disabled="busy"
        @change="onSelectBoard"
      >
        <option value="">画布列表 ({{ boards.length }})</option>
        <option v-for="item in boards" :key="item.id" :value="item.id">
          {{ item.title || '未命名画布' }}
        </option>
      </select>

      <input
        v-if="board"
        class="cc-title-input"
        type="text"
        maxlength="100"
        aria-label="当前画布名称"
        placeholder="输入画布名称"
        :value="title"
        :disabled="busy"
        @input="emit('rename', ($event.target as HTMLInputElement).value)"
        @keydown.enter="($event.target as HTMLInputElement).blur()"
      />

      <span
        v-if="board"
        class="cc-status-badge"
        :class="{ 'is-conflict': conflict, 'is-dirty': !conflict && dirty }"
        role="status"
      >
        {{ statusText }}
      </span>
    </div>

    <div class="cc-top-actions">
      <button type="button" class="cc-btn" :disabled="busy" @click="emit('openCreate')">
        + 新建画布
      </button>

      <template v-if="board">
        <button type="button" class="cc-btn" :disabled="busy" @click="emit('addNote')">
          + 灵感便签
        </button>
        <button
          type="button"
          class="cc-btn is-primary"
          :disabled="busy || !canCreateDraft"
          @click="emit('createDraft')"
        >
          + 视频草稿
        </button>
        <button
          type="button"
          class="cc-btn"
          :disabled="busy || !canCompare"
          @click="emit('compare')"
        >
          对比分支
        </button>
        <button
          type="button"
          class="cc-btn"
          title="撤销 (Ctrl/Cmd+Z)"
          :disabled="busy || !canUndo"
          @click="emit('undo')"
        >
          撤销
        </button>
        <button
          type="button"
          class="cc-btn"
          title="重做 (Ctrl/Cmd+Shift+Z)"
          :disabled="busy || !canRedo"
          @click="emit('redo')"
        >
          重做
        </button>
        <button
          type="button"
          class="cc-btn"
          :class="{ 'is-primary': dirty && !conflict }"
          title="保存画布 (Ctrl/Cmd+S)"
          :disabled="busy || !dirty"
          @click="emit('save')"
        >
          {{ busy ? '保存中…' : '保存' }}
        </button>
        <button
          type="button"
          class="cc-btn is-danger"
          :disabled="busy"
          @click="emit('deleteBoard')"
        >
          删除
        </button>
      </template>
    </div>
  </header>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { CanvasSummary, CreativeCanvas } from './types'

const props = defineProps<{
  boards: readonly CanvasSummary[]
  board: CreativeCanvas | null
  title: string
  dirty: boolean
  busy: boolean
  conflict: boolean
  canUndo: boolean
  canRedo: boolean
  canCreateDraft: boolean
  canCompare: boolean
}>()

const emit = defineEmits<{
  selectBoard: [id: string]
  openCreate: []
  rename: [title: string]
  addNote: []
  createDraft: []
  compare: []
  undo: []
  redo: []
  save: []
  deleteBoard: []
}>()

const statusText = computed(() => {
  if (props.conflict) return '版本冲突 · 需刷新'
  if (props.busy) return '同步中…'
  if (props.dirty) return '未保存修改'
  return `已保存 · v${props.board?.revision ?? 0}`
})

function onSelectBoard(event: Event) {
  const select = event.target as HTMLSelectElement
  const nextId = select.value
  // Keep controlled value in sync until parent confirms (in case user cancels unsaved switch).
  select.value = props.board?.id || ''
  emit('selectBoard', nextId)
}
</script>
