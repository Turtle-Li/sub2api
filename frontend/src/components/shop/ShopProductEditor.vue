<script setup lang="ts">
import { reactive, watch } from 'vue'
import type { ShopProduct, ShopProductInput } from '@/types/shop'
const props = defineProps<{ product: ShopProduct | null; busy: boolean }>()
const emit = defineEmits<{ save: [input: ShopProductInput] }>()
const defaults = (): ShopProductInput => ({ name: '', category: '', summary: '', description: '', price: 0, duration_value: 1, duration_unit: 'month', fulfillment_mode: 'session_topup', input_format: 'session_json', input_url: '', input_instructions: '', guide_key: '', enabled: false, sort_order: 0 })
const draft = reactive(defaults())
watch(() => props.product, product => { Object.assign(draft, defaults(), product ?? {}); draft.revision = product?.revision }, { immediate: true })
function save() {
  emit('save', { name: draft.name, category: draft.category, summary: draft.summary, description: draft.description, price: draft.price, duration_value: draft.duration_value, duration_unit: draft.duration_unit, fulfillment_mode: draft.fulfillment_mode, input_format: draft.input_format, input_url: draft.input_url, input_instructions: draft.input_instructions, guide_key: draft.guide_key, enabled: draft.enabled, sort_order: draft.sort_order, revision: draft.revision })
}
</script>
<template>
  <form class="space-y-4" @submit.prevent="save">
    <div class="grid gap-4 sm:grid-cols-2">
      <label class="text-sm font-medium">商品名称<input v-model="draft.name" class="input mt-1" maxlength="160" required></label>
      <label class="text-sm font-medium">分类<input v-model="draft.category" class="input mt-1" maxlength="80" placeholder="自由填写分类"></label>
      <label class="text-sm font-medium">售价（人民币元）<input v-model.number="draft.price" type="number" :min="draft.enabled ? 0.01 : 0" step="0.01" class="input mt-1" required></label>
      <label class="text-sm font-medium">交付模式<select v-model="draft.fulfillment_mode" class="input mt-1"><option value="session_topup">用户提交资料，人工代充</option><option value="account_delivery">管理员填写账号发货</option></select></label>
      <label class="text-sm font-medium">期限<input v-model.number="draft.duration_value" type="number" min="1" step="1" class="input mt-1" required></label>
      <label class="text-sm font-medium">期限单位<select v-model="draft.duration_unit" class="input mt-1"><option value="month">个月</option><option value="day">天</option><option value="year">年</option></select></label>
    </div>
    <label class="block text-sm font-medium">卡片简介<textarea v-model="draft.summary" class="input mt-1" rows="2" maxlength="1000" /></label>
    <label class="block text-sm font-medium">商品详情（Markdown）<textarea v-model="draft.description" class="input mt-1" rows="7" maxlength="50000" /></label>
    <fieldset v-if="draft.fulfillment_mode === 'session_topup'" class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600"><legend class="px-1 text-sm font-medium">用户资料</legend>
      <label class="block text-sm">资料格式<select v-model="draft.input_format" class="input mt-1"><option value="session_json">完整 Session JSON</option><option value="text">文本资料</option></select></label>
      <label class="block text-sm">获取资料页面（HTTPS）<input v-model="draft.input_url" type="url" pattern="https://.*" class="input mt-1" :placeholder="draft.input_format === 'session_json' ? '留空使用 ChatGPT Session 页面' : '可留空'"></label>
      <label class="block text-sm">提交说明<textarea v-model="draft.input_instructions" class="input mt-1" rows="3" maxlength="5000" /></label>
    </fieldset>
    <div class="grid gap-4 sm:grid-cols-2"><label class="text-sm font-medium">适用教程<select v-model="draft.guide_key" class="input mt-1"><option value="">无</option><option value="chatgpt-plus-account">ChatGPT Plus 成品账号教程</option></select></label><label class="text-sm font-medium">排序<input v-model.number="draft.sort_order" type="number" step="1" class="input mt-1"></label></div>
    <label class="flex items-center gap-2 text-sm"><input v-model="draft.enabled" type="checkbox">上架商品（需填写有效价格）</label>
    <div class="flex justify-end"><button class="btn btn-primary" :disabled="busy">{{ busy ? '保存中…' : '保存商品' }}</button></div>
  </form>
</template>
