<template>
  <section class="cc-empty-state" aria-label="画布列表与新建">
    <div class="cc-empty-card">
      <h2>{{ boards.length ? '选择或新建创作画布' : '开始你的第一张创作画布' }}</h2>
      <p class="cc-meta">
        把生图原图、视频作品与灵感便签摆在同一张画布上，从同一张原图派生多条视频草稿并对比结果。画布编辑与保存均不扣费。
      </p>

      <div v-if="busy && !boards.length" class="cc-kv" role="status">
        正在读取画布列表…
      </div>

      <div v-if="boards.length" class="cc-dialog-list" role="list" aria-label="已有画布">
        <button
          v-for="item in boards"
          :key="item.id"
          type="button"
          class="cc-dialog-option"
          :disabled="busy"
          @click="emit('openBoard', item.id)"
        >
          <span>
            <strong>{{ item.title || '未命名画布' }}</strong>
            <span v-if="item.updated_at" class="cc-meta" style="display: block">
              更新于 {{ formatBoardTime(item.updated_at) }}
            </span>
          </span>
          <span class="cc-pill">v{{ item.revision }}</span>
        </button>
      </div>

      <form class="cc-create-form" @submit.prevent="submitCreate">
        <input
          v-model="draftTitle"
          type="text"
          class="cc-note-input"
          maxlength="100"
          placeholder="输入新画布名称（例如：海边短片分镜）"
          aria-label="新画布名称"
          :disabled="busy"
        />
        <div class="cc-empty-actions">
          <button type="submit" class="cc-btn is-primary" :disabled="busy">
            {{ busy ? '处理中…' : '+ 新建空白画布' }}
          </button>
          <button
            v-if="error"
            type="button"
            class="cc-btn"
            :disabled="busy"
            @click="emit('retry')"
          >
            重试加载
          </button>
        </div>
      </form>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { formatBoardTime } from './screenModel'
import type { CanvasSummary } from './types'

const props = defineProps<{
  boards: readonly CanvasSummary[]
  busy: boolean
  error: string
}>()

const emit = defineEmits<{
  openBoard: [id: string]
  createBoard: [title: string]
  retry: []
}>()

const draftTitle = ref('')

function submitCreate() {
  if (props.busy) return
  const title = draftTitle.value.trim() || '未命名画布'
  draftTitle.value = ''
  emit('createBoard', title)
}
</script>
