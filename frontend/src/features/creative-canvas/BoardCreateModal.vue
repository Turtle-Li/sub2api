<template>
  <div
    v-if="open"
    class="cc-modal-backdrop"
    role="dialog"
    aria-modal="true"
    aria-labelledby="cc-create-board-title"
    @click.self="emit('close')"
  >
    <form class="cc-modal-card" style="width: min(440px, 100%)" @submit.prevent="submit">
      <div class="cc-modal-head">
        <h3 id="cc-create-board-title">新建创作画布</h3>
        <button type="button" class="cc-btn is-sm" @click="emit('close')">关闭</button>
      </div>

      <p class="cc-meta">
        新建一张空白画布。你可以从生图或视频工作台添加素材，或在画布内添加灵感便签。
      </p>

      <label class="cc-kv" style="display: grid; gap: 4px">
        <b>画布名称</b>
        <input
          ref="inputRef"
          v-model="title"
          type="text"
          class="cc-note-input"
          maxlength="100"
          placeholder="例如：海边灯塔分镜草稿"
          :disabled="busy"
        />
      </label>

      <div class="cc-node-foot" style="justify-content: flex-end">
        <button type="button" class="cc-btn" :disabled="busy" @click="emit('close')">
          取消
        </button>
        <button type="submit" class="cc-btn is-primary" :disabled="busy">
          {{ busy ? '创建中…' : '创建并打开' }}
        </button>
      </div>
    </form>
  </div>
</template>

<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'

const props = defineProps<{
  open: boolean
  busy: boolean
}>()

const emit = defineEmits<{
  close: []
  confirm: [title: string]
}>()

const title = ref('')
const inputRef = ref<HTMLInputElement>()

watch(
  () => props.open,
  async visible => {
    if (!visible) return
    title.value = ''
    await nextTick()
    inputRef.value?.focus()
  },
)

function submit() {
  if (props.busy) return
  emit('confirm', title.value.trim() || '未命名画布')
}
</script>
