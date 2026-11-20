<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { getImageStudioWalletGroup, setImageStudioWalletGroup, type ImageStudioWalletConfig } from '@/api/admin/imageModels'
import type { AdminGroup } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ groups: readonly AdminGroup[] }>()
const current = ref<ImageStudioWalletConfig | null>(null)
const draftGroup = ref(0)
const draftAccounts = ref('')
const saving = ref(false)
const error = ref('')
const saved = ref(false)
const candidates = computed(() => props.groups.filter(group => group.platform === 'openai' && group.subscription_type === 'standard' && group.status === 'active' && group.allow_image_generation))
const draft = computed<ImageStudioWalletConfig | null>(() => {
  if (draftGroup.value === 0) return { group_id: 0, account_ids: [] }
  const tokens = draftAccounts.value.trim().split(/[\s,，]+/)
  if (tokens.length > 32 || tokens.some(token => !/^[1-9][0-9]*$/.test(token))) return null
  const ids = tokens.map(Number).sort((a, b) => a - b)
  if (ids.some(id => !Number.isSafeInteger(id)) || new Set(ids).size !== ids.length) return null
  return { group_id: draftGroup.value, account_ids: ids }
})
const canSave = computed(() => current.value !== null && draft.value !== null &&
  (current.value.group_id !== draft.value.group_id || current.value.account_ids.join(',') !== draft.value.account_ids.join(',')))
const controller = new AbortController()

function adopt(config: ImageStudioWalletConfig) {
  current.value = config
  draftGroup.value = config.group_id
  draftAccounts.value = config.account_ids.join(', ')
}

async function load() {
  error.value = ''
  try {
    const config = await getImageStudioWalletGroup(controller.signal)
    if (!controller.signal.aborted) adopt(config)
  } catch (cause) {
    if (!controller.signal.aborted) error.value = extractApiErrorMessage(cause, '读取工作台分组失败。')
  }
}

async function save() {
  if (!canSave.value || !current.value || !draft.value || saving.value) return
  saving.value = true
  error.value = ''
  saved.value = false
  try {
    adopt(await setImageStudioWalletGroup(draft.value, current.value))
    saved.value = true
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, '保存工作台分组失败。请重新读取设置后再试。')
  } finally { saving.value = false }
}

onMounted(load)
onBeforeUnmount(() => controller.abort())
</script>

<template>
  <form class="card space-y-3 p-5" @submit.prevent="save">
    <label for="image-studio-wallet-group" class="block text-sm font-semibold">工作台余额计费分组</label>
    <p id="image-studio-wallet-description" class="text-sm text-gray-600 dark:text-gray-400">开启后，登录用户可按该组的模型和分辨率价格使用余额生图，无需创建 API Key 或加入分组。下方的账号仅供工作台使用，不改变外部 API 的账号池和权限。</p>
    <div class="flex flex-wrap items-center gap-3">
      <select id="image-studio-wallet-group" v-model="draftGroup" class="input w-full max-w-sm" aria-describedby="image-studio-wallet-description" :disabled="current === null || saving" @change="saved = false">
        <option :value="0">未启用余额计费入口</option>
        <option v-if="current?.group_id && !candidates.some(group => group.id === current?.group_id)" :value="current.group_id" disabled>当前分组 #{{ current.group_id }} 不可用</option>
        <option v-for="group in candidates" :key="group.id" :value="group.id">{{ group.name }}</option>
      </select>
    </div>
    <label v-if="draftGroup > 0" class="block max-w-sm space-y-1 text-sm">
      <span>工作台专用上游账号 ID</span>
      <input v-model="draftAccounts" class="input w-full" type="text" inputmode="text" placeholder="用逗号分隔账号 ID" :disabled="saving" @input="saved = false" />
      <span v-if="!draft" class="text-gray-600 dark:text-gray-400">请输入 1–32 个不重复的数字 ID。</span>
    </label>
    <div class="flex flex-wrap items-center gap-3">
      <button type="submit" class="btn btn-primary" :disabled="!canSave || saving">{{ saving ? '保存中…' : '保存工作台分组' }}</button>
      <button v-if="error" type="button" class="btn btn-secondary" :disabled="saving" @click="load">重新读取设置</button>
      <span v-if="saved" role="status" class="text-sm text-gray-600 dark:text-gray-400">已保存</span>
    </div>
    <p v-if="error" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ error }}</p>
  </form>
</template>
