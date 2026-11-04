<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import ShopOrderSummary from '@/components/shop/ShopOrderSummary.vue'
import ShopDelivery from '@/components/shop/ShopDelivery.vue'
import ShopSessionCheck from '@/components/shop/ShopSessionCheck.vue'
import { shopAPI } from '@/api/shop'
import { isShopDaypass, shopAutomationLabels, type ShopInputCheck, type ShopOrderDetail } from '@/types/shop'
import { sanitizeUrl } from '@/utils/url'
import { previewShopSession } from '@/utils/shopSessionPreview'
const route = useRoute()
const order = ref<ShopOrderDetail | null>(null)
const content = ref('')
const inputCheck = ref<ShopInputCheck | null>(null)
const busy = ref(false)
const error = ref('')
const message = ref('')
let generation = 0
let edit = 0
const inputURL = computed(() => sanitizeUrl(order.value?.product.input_url || (order.value?.product.input_format === 'session_json' ? 'https://chatgpt.com/api/auth/session' : '')))
const localPreview = computed(() => order.value?.product.input_format === 'session_json' ? previewShopSession(content.value) : null)
const displayedCheck = computed(() => inputCheck.value ?? localPreview.value?.check ?? null)
async function load() {
  const current = ++generation
  busy.value = true
  error.value = ''
  try { const result = await shopAPI.order(Number(route.params.id)); if (current === generation) order.value = result.data }
  catch { if (current === generation) error.value = '无法加载订单，请重试。' }
  finally { if (current === generation) busy.value = false }
}
async function submit() {
  if (!order.value?.can_submit || busy.value || !content.value.trim()) return
  busy.value = true
  error.value = ''
  const current = generation
  const { order_id: id, revision } = order.value
  const input = { content: content.value, revision }
  const currentEdit = edit
  const matches = () => current === generation && currentEdit === edit && order.value?.order_id === id && order.value.revision === revision && content.value === input.content
  try {
    if (order.value.product.input_format === 'session_json') {
      const result = await shopAPI.checkInput(id, input)
      if (!matches()) return
      inputCheck.value = result.data
    }
    const result = await shopAPI.submitInput(id, input)
    if (!matches()) return
    order.value = result.data
    content.value = ''
    message.value = '资料已提交，等待管理员处理。'
  } catch (err: unknown) { if (matches()) error.value = typeof err === 'object' && err !== null && 'message' in err && typeof err.message === 'string' ? err.message : '提交失败，请检查资料并刷新订单。' }
  finally { if (current === generation) busy.value = false }
}
watch(content, () => { edit++; inputCheck.value = null; error.value = '' }, { flush: 'sync' })
watch(() => route.params.id, () => { order.value = null; content.value = ''; inputCheck.value = null; message.value = ''; void load() }, { immediate: true })
onBeforeUnmount(() => { generation++; content.value = ''; order.value = null })
</script>
<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-4xl py-2 sm:px-4 sm:py-4">
      <header class="mb-5 flex flex-wrap items-center justify-between gap-3">
        <RouterLink to="/orders" class="inline-flex min-h-11 items-center rounded-md text-sm font-medium text-gray-600 hover:text-gray-900 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:text-gray-400 dark:hover:text-gray-100 dark:focus:ring-offset-gray-950">返回我的订单</RouterLink>
        <button type="button" class="btn btn-secondary min-h-11" :disabled="busy" @click="load">刷新状态</button>
      </header>

      <div v-if="!order && busy" class="space-y-5" aria-busy="true" aria-label="正在加载订单">
        <div class="h-56 animate-pulse rounded-xl border border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-900"></div>
        <div class="h-64 animate-pulse rounded-xl border border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-900"></div>
      </div>

      <section v-else-if="!order" class="rounded-xl border border-red-200 bg-white p-6 dark:border-red-900 dark:bg-gray-900" role="alert">
        <h1 class="text-lg font-semibold text-gray-900 dark:text-gray-100">无法显示订单</h1>
        <p class="mt-2 text-sm text-red-700 dark:text-red-300">{{ error }}</p>
        <button type="button" class="mt-5 inline-flex min-h-11 items-center justify-center rounded-md bg-primary-700 px-5 text-sm font-medium text-white hover:bg-primary-800 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:bg-primary-400 dark:text-gray-950 dark:hover:bg-primary-300 dark:focus:ring-offset-gray-900" @click="load">重新加载</button>
      </section>

      <template v-else-if="order">
        <ShopOrderSummary :order="order" :show-guide="order.product.fulfillment_mode !== 'account_delivery'" />

        <div class="mt-4 space-y-3" aria-live="polite">
          <p v-if="message" class="rounded-lg border border-green-200 bg-green-50 px-4 py-3 text-sm text-green-800 dark:border-green-900 dark:bg-green-950 dark:text-green-200">{{ message }}</p>
          <p v-if="error" class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200" role="alert">{{ error }}</p>
        </div>

        <section v-if="order.fulfillment_status === 'WAITING_PAYMENT'" class="mt-4 rounded-xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-gray-900 sm:p-6">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100">订单尚未付款</h2>
          <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-gray-400">前往我的订单，继续完成本次付款。</p>
          <RouterLink to="/orders" class="mt-5 inline-flex min-h-11 items-center justify-center rounded-md bg-primary-700 px-5 text-sm font-semibold text-white hover:bg-primary-800 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:bg-primary-400 dark:text-gray-950 dark:hover:bg-primary-300 dark:focus:ring-offset-gray-900">继续付款</RouterLink>
        </section>

        <section v-else-if="order.fulfillment_status === 'CLOSED'" class="mt-4 rounded-xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-gray-900 sm:p-6">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100">订单已关闭</h2>
          <p class="mt-2 whitespace-pre-wrap break-words text-sm leading-6 text-gray-600 dark:text-gray-400">此订单已关闭，不能再提交资料。</p>
        </section>

        <section v-else-if="order.product.fulfillment_mode === 'session_topup' && (order.fulfillment_status === 'WAITING_INPUT' || order.fulfillment_status === 'NEEDS_INPUT' || (order.fulfillment_status === 'QUEUED' && order.can_submit))" class="mt-4 rounded-xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-gray-900 sm:p-6">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100">{{ order.fulfillment_status === 'QUEUED' ? '资料已提交，等待人工处理' : order.fulfillment_status === 'NEEDS_INPUT' ? '请补充资料' : '提交充值资料' }}</h2>
          <p v-if="order.fulfillment_status === 'QUEUED'" class="mt-2 text-sm leading-6 text-gray-600 dark:text-gray-400">客服开始处理前，你仍可以更新资料。</p>
          <dl v-if="order.fulfillment_status === 'QUEUED' && (order.input_account || order.input_submitted_at)" class="mt-5 grid gap-3 border-t border-gray-200 pt-5 text-sm dark:border-gray-800 sm:grid-cols-2">
            <div v-if="order.input_account">
              <dt class="text-gray-500 dark:text-gray-400">目标账号</dt>
              <dd class="mt-1 break-all text-gray-800 dark:text-gray-200">{{ order.input_account }}</dd>
            </div>
            <div v-if="order.input_submitted_at">
              <dt class="text-gray-500 dark:text-gray-400">提交时间</dt>
              <dd class="mt-1 text-gray-800 dark:text-gray-200">{{ order.input_submitted_at }}</dd>
            </div>
          </dl>

          <component :is="order.fulfillment_status === 'QUEUED' ? 'details' : 'div'">
          <summary v-if="order.fulfillment_status === 'QUEUED'" class="mt-3 flex min-h-11 cursor-pointer items-center text-sm font-medium text-primary-700 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500 dark:text-primary-300">更新充值资料</summary>
          <template v-if="order.can_submit">
            <div class="mt-3 rounded-lg bg-gray-50 p-4 text-sm leading-6 text-gray-700 dark:bg-gray-800 dark:text-gray-300">
              <p v-if="!order.product.input_instructions">登录要充值的 ChatGPT 账号，打开会话页面，复制页面的全部内容。</p>
              <p v-if="order.product.input_instructions" class="whitespace-pre-wrap break-words">{{ order.product.input_instructions }}</p>
              <a v-if="inputURL" :href="inputURL" target="_blank" rel="noopener noreferrer" class="btn btn-secondary mt-3 min-h-11">打开 Session 页面 ↗</a>
            </div>

            <form class="mt-3" @submit.prevent="submit">
              <label for="shop-session-content" class="block text-sm font-medium text-gray-900 dark:text-gray-100">全部原始内容</label>
              <textarea id="shop-session-content" v-model="content" name="content" wrap="soft" rows="5" maxlength="262144" autocomplete="off" autocapitalize="off" spellcheck="false" :disabled="busy" class="input mt-2 block min-h-32 w-full resize-y whitespace-pre-wrap [overflow-wrap:anywhere] font-mono text-xs leading-5 disabled:cursor-not-allowed disabled:opacity-60" placeholder="在此粘贴全部原始内容"></textarea>

              <div aria-live="polite">
                <p v-if="localPreview?.error && !inputCheck" class="mt-3 text-sm leading-6 text-amber-800 dark:text-amber-200" role="status">{{ localPreview.error }}</p>
                <ShopSessionCheck v-if="displayedCheck" :check="displayedCheck" :account-name="localPreview?.check?.account === displayedCheck.account ? localPreview.accountName : undefined" :local="!inputCheck" class="mt-4" />
              </div>

              <button type="submit" class="mt-5 inline-flex min-h-11 w-full items-center justify-center rounded-md bg-primary-700 px-5 text-sm font-semibold text-white hover:bg-primary-800 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-primary-400 dark:text-gray-950 dark:hover:bg-primary-300 dark:focus:ring-offset-gray-900 sm:w-auto" :disabled="busy || !content.trim()" :aria-busy="busy">
                {{ busy ? '正在处理…' : order.product.input_format === 'session_json' ? '提交' : '提交资料' }}
              </button>
            </form>
          </template>
          <p v-else class="mt-5 text-sm leading-6 text-gray-600 dark:text-gray-400">当前无法提交资料，请稍后刷新订单。</p>
          </component>
        </section>

        <section v-else-if="order.product.fulfillment_mode === 'session_topup' && (order.fulfillment_status === 'QUEUED' || order.fulfillment_status === 'PROCESSING')" class="mt-4 rounded-xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-gray-900 sm:p-6">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100">{{ order.fulfillment_status === 'QUEUED' ? '资料已提交，等待人工处理' : '订单正在人工处理' }}</h2>
          <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-gray-400">资料已收到，无需重复提交。处理进度会更新在这里。</p>
          <dl v-if="order.input_account || order.input_submitted_at" class="mt-5 grid gap-3 border-t border-gray-200 pt-5 text-sm dark:border-gray-800 sm:grid-cols-2">
            <div v-if="order.input_account">
              <dt class="text-gray-500 dark:text-gray-400">目标账号</dt>
              <dd class="mt-1 break-all text-gray-800 dark:text-gray-200">{{ order.input_account }}</dd>
            </div>
            <div v-if="order.input_submitted_at">
              <dt class="text-gray-500 dark:text-gray-400">提交时间</dt>
              <dd class="mt-1 text-gray-800 dark:text-gray-200">{{ order.input_submitted_at }}</dd>
            </div>
          </dl>
        </section>

        <section v-else-if="order.fulfillment_status === 'COMPLETED' && order.product.fulfillment_mode === 'account_delivery' && order.delivery_kind !== 'none'" class="mt-4 space-y-4">
          <div v-if="order.product.guide_key" class="rounded-xl border border-primary-200 bg-primary-50 p-5 dark:border-primary-800 dark:bg-primary-950 sm:p-6">
            <h2 class="text-lg font-semibold text-primary-900 dark:text-primary-100">{{ isShopDaypass(order.product) ? 'API Key 使用指南' : '必看：成品号使用指南' }}</h2>
            <p class="mt-2 text-sm leading-6 text-primary-800 dark:text-primary-200">{{ isShopDaypass(order.product) ? '查看客户端配置、模型名称、并发与 IP 限制。' : '查看交付内容前，请先了解登录、账号安全和验证设置步骤。' }}</p>
            <RouterLink :to="`/shop/help/${order.product.guide_key}`" :aria-label="isShopDaypass(order.product) ? 'API Key 使用指南' : '必看：成品号使用指南'" class="mt-4 inline-flex min-h-11 items-center justify-center rounded-md bg-primary-700 px-5 text-sm font-semibold text-white hover:bg-primary-800 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:bg-primary-400 dark:text-gray-950 dark:hover:bg-primary-300 dark:focus:ring-offset-gray-900">阅读使用指南</RouterLink>
          </div>
          <ShopDelivery :content="order.delivery_content || ''" :api-key="isShopDaypass(order.product)" />
        </section>

        <section v-else-if="order.fulfillment_status === 'COMPLETED'" class="mt-4 rounded-xl border border-green-200 bg-white p-5 dark:border-green-900 dark:bg-gray-900 sm:p-6">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100">订单已完成</h2>
          <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-gray-400">{{ order.delivery_kind === 'none' ? '服务已处理完成，无需领取账号或密钥。' : '充值已完成，可前往 ChatGPT 查看订阅。' }}</p>
        </section>

        <section v-else class="mt-4 rounded-xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-gray-900 sm:p-6">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100">{{ order.automation?.status === 'REVIEW_REQUIRED' ? '交付待复核' : order.product.automatic_fulfillment && isShopDaypass(order.product) ? '正在自动发放 API Key' : order.automation ? shopAutomationLabels[order.automation.status] : order.product.automatic_fulfillment ? '等待自动处理' : '等待人工处理' }}</h2>
          <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-gray-400">{{ order.automation?.status === 'REVIEW_REQUIRED' ? '付款已确认，交付需要客服核对。请勿重复购买，可联系客服处理。' : order.product.automatic_fulfillment && isShopDaypass(order.product) ? '付款已确认。请稍后刷新状态，Key 和调用方式会显示在这里。有效期从付款时间开始计算。' : order.product.automatic_fulfillment ? '付款已确认。请稍后刷新，交付结果会显示在这里。' : '客服正在处理此订单，完成后可在这里查看结果。' }}</p>
        </section>
      </template>
    </div>
  </AppLayout>
</template>
