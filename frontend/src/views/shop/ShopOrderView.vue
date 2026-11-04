<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import ShopOrderSummary from '@/components/shop/ShopOrderSummary.vue'
import ShopDelivery from '@/components/shop/ShopDelivery.vue'
import ShopSessionCheck from '@/components/shop/ShopSessionCheck.vue'
import { shopAPI } from '@/api/shop'
import type { ShopInputCheck, ShopOrderDetail } from '@/types/shop'
import { sanitizeUrl } from '@/utils/url'
import { useAppStore } from '@/stores/app'
const app = useAppStore()
const route = useRoute()
const order = ref<ShopOrderDetail | null>(null)
const content = ref('')
const inputCheck = ref<ShopInputCheck | null>(null)
const busy = ref(false)
const error = ref('')
const message = ref('')
let generation = 0
const inputURL = computed(() => sanitizeUrl(order.value?.product.input_url || (order.value?.product.input_format === 'session_json' ? 'https://chatgpt.com/api/auth/session' : '')))
async function load() {
  const current = ++generation
  busy.value = true
  error.value = ''
  try { const result = await shopAPI.order(Number(route.params.id)); if (current === generation) order.value = result.data }
  catch { if (current === generation) error.value = '无法加载订单，请重试。' }
  finally { if (current === generation) busy.value = false }
}
async function submit() {
  if (!order.value || busy.value || !content.value.trim()) return
  busy.value = true
  error.value = ''
  const current = generation
  const checkedContent = content.value
  try {
    if (order.value.product.input_format === 'session_json' && !inputCheck.value) {
      const result = await shopAPI.checkInput(order.value.order_id, { content: checkedContent, revision: order.value.revision })
      if (current === generation && content.value === checkedContent) inputCheck.value = result.data
      return
    }
    const result = await shopAPI.submitInput(order.value.order_id, { content: checkedContent, revision: order.value.revision })
    if (current !== generation) return
    order.value = result.data
    content.value = ''
    message.value = '资料已提交，等待管理员处理。'
  } catch (err: unknown) { error.value = typeof err === 'object' && err !== null && 'message' in err && typeof err.message === 'string' ? err.message : '提交失败，请检查资料并刷新订单。' }
  finally { busy.value = false }
}
watch(content, () => { inputCheck.value = null; error.value = '' })
watch(() => route.params.id, () => { order.value = null; content.value = ''; inputCheck.value = null; message.value = ''; void load() }, { immediate: true })
onBeforeUnmount(() => { generation++; content.value = ''; order.value = null })
</script>
<template>
  <AppLayout><div class="mx-auto max-w-3xl space-y-5">
    <header class="flex flex-wrap items-center justify-between gap-3"><RouterLink to="/orders" class="text-sm text-primary-700 dark:text-primary-300">← 我的订单</RouterLink><button class="btn btn-secondary btn-sm" :disabled="busy" @click="load">刷新状态</button></header>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-4 text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
    <p v-if="busy && !order" role="status">正在加载订单…</p>
    <template v-if="order">
      <ShopOrderSummary :order="order" :show-guide="order.product.fulfillment_mode !== 'account_delivery'" />
      <RouterLink v-if="order.product.fulfillment_mode === 'account_delivery' && order.product.guide_key" :to="`/shop/help/${order.product.guide_key}`" aria-label="必看：成品号使用指南" class="group flex flex-col gap-5 rounded-2xl border border-primary-200 bg-primary-50 p-5 transition-colors hover:bg-primary-100 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-primary-600 sm:flex-row sm:items-center sm:justify-between sm:p-6 dark:border-primary-800 dark:bg-primary-950/40 dark:hover:bg-primary-900/40">
        <div class="min-w-0">
          <div class="flex flex-wrap items-center gap-3"><span class="rounded-md bg-primary-700 px-2 py-1 text-xs font-bold text-white dark:bg-primary-300 dark:text-primary-950">必看</span><h2 class="text-lg font-bold text-gray-900 dark:text-white">成品号使用指南</h2></div>
          <p class="mt-3 text-sm leading-6 text-primary-900 dark:text-primary-100">使用账号前，请按顺序完成邮箱登录、ChatGPT 登录和安全设置。</p>
        </div>
        <span class="btn btn-primary shrink-0 gap-2">打开使用指南 <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="M5 12h14m-6-6 6 6-6 6" stroke-linecap="round" stroke-linejoin="round" /></svg></span>
      </RouterLink>
      <ShopDelivery v-if="order.delivery_content" :content="order.delivery_content" />
      <div v-if="order.fulfillment_status === 'WAITING_PAYMENT'" class="card p-6"><h2 class="font-semibold">等待付款</h2><p class="my-3 text-sm text-gray-500 dark:text-gray-400">请从我的订单继续付款或取消订单，付款完成后再提交资料。</p><RouterLink to="/orders" class="btn btn-primary">继续付款</RouterLink></div>
      <form v-if="order.can_submit" class="card space-y-4 p-6" @submit.prevent="submit">
        <h2 class="text-lg font-bold">{{ order.fulfillment_status === 'NEEDS_INPUT' ? '补充资料' : '提交代充资料' }}</h2>
        <p v-if="order.product.input_instructions" class="whitespace-pre-wrap break-words text-sm leading-6">{{ order.product.input_instructions }}</p>
        <p v-else-if="order.product.input_format === 'session_json'" class="text-sm leading-6 text-gray-500 dark:text-gray-400">先登录要充值的账号，打开资料页面，将全部内容复制到下方。</p>
        <a v-if="inputURL" :href="inputURL" target="_blank" rel="noopener noreferrer" class="inline-block text-sm text-primary-700 underline dark:text-primary-300">打开资料页面 ↗</a>
        <label class="block text-sm font-medium">{{ order.product.input_format === 'session_json' ? '完整 Session 内容' : '所需资料' }}<textarea v-model="content" class="input mt-2 min-h-48 font-mono text-xs" autocomplete="off" autocapitalize="off" :spellcheck="false" maxlength="262144" :disabled="busy" required /></label>
        <ShopSessionCheck v-if="inputCheck" :check="inputCheck" />
        <p class="text-xs text-gray-500 dark:text-gray-400">提交后不回显原文，开始处理前可更新资料。</p>
        <button class="btn btn-primary" :disabled="busy || !content.trim()">{{ busy ? '处理中…' : order.product.input_format !== 'session_json' ? '提交资料' : inputCheck ? '确认账号无误，提交资料' : '检查资料' }}</button>
      </form>
      <p v-if="message" role="status" class="text-sm text-primary-700 dark:text-primary-300">{{ message }}</p>
      <div v-if="order.input_submitted_at && !inputCheck" class="card space-y-3 p-5 text-sm"><p>资料提交时间：{{ new Date(order.input_submitted_at).toLocaleString() }}</p><ShopSessionCheck v-if="order.input_check" :check="order.input_check" /><p v-else-if="order.input_account" class="break-all">目标账号：{{ order.input_account }}</p></div>
      <p v-if="app.contactInfo" class="whitespace-pre-wrap break-words text-xs leading-6 text-gray-500 dark:text-gray-400">联系客服：{{ app.contactInfo }}</p>
    </template>
  </div></AppLayout>
</template>
