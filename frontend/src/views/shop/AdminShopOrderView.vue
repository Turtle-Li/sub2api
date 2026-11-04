<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import ShopOrderSummary from '@/components/shop/ShopOrderSummary.vue'
import ShopSessionCheck from '@/components/shop/ShopSessionCheck.vue'
import { useStepUp, isStepUpCancelled } from '@/composables/useStepUp'
import { shopAPI } from '@/api/shop'
import { shopNotificationLabels, type ShopAction, type ShopOrderDetail } from '@/types/shop'
const route = useRoute()
const stepUp = useStepUp()
const order = ref<ShopOrderDetail | null>(null)
const note = ref('')
const delivery = ref('')
const preview = ref(false)
const busy = ref(false)
const error = ref('')
const message = ref('')
const copied = ref(false)
watch(() => [order.value?.order_id, order.value?.revision, order.value?.input_revision] as const, (next, prev) => {
  if (next.some((v, i) => v !== prev[i])) copied.value = false
}, { flush: 'sync' })
let generation = 0
async function load() {
  const current = ++generation
  busy.value = true
  error.value = ''
  try { const result = await shopAPI.adminOrder(Number(route.params.id)); if (current === generation) order.value = result.data }
  catch { if (current === generation) error.value = '订单加载失败，请重试。' }
  finally { if (current === generation) busy.value = false }
}
async function copyInput() {
  if (!order.value || busy.value) return
  busy.value = true
  error.value = ''
  copied.value = false
  const current = generation
  const { order_id: id, revision, input_revision } = order.value
  const matches = () => current === generation && order.value?.order_id === id && order.value.revision === revision && order.value.input_revision === input_revision
  try {
    const revealed = await stepUp.run(() => shopAPI.revealInput(id, revision))
    if (!matches()) return
    if (revealed.data.input_revision !== input_revision) throw new Error('stale input')
    await navigator.clipboard.writeText(revealed.data.content)
    if (matches()) copied.value = true
  } catch (err) { if (matches() && !isStepUpCancelled(err)) error.value = '无法复制资料。请检查剪贴板权限或刷新订单后重试。' }
  finally { if (current === generation) busy.value = false }
}
async function act(action: ShopAction['action']) {
  if (!order.value || busy.value) return
  busy.value = true
  error.value = ''
  const id = order.value.order_id
  const input: ShopAction = { action, revision: order.value.revision, input_revision: order.value.input_revision }
  if (action === 'request_input') input.note = note.value
  if (action === 'complete' && order.value.product.fulfillment_mode === 'account_delivery') input.delivery_content = delivery.value
  try {
    order.value = (await stepUp.run(() => shopAPI.action(id, input))).data
    delivery.value = ''; note.value = ''; preview.value = false
    message.value = action === 'complete' ? '交付已完成，邮件已进入发送队列。' : '订单状态已更新。'
  } catch (err) { if (!isStepUpCancelled(err)) { preview.value = false; error.value = '操作失败，可能订单或资料已更新。请刷新后核对；不要重复进行站外操作。' } }
  finally { busy.value = false }
}
async function retry(channel: 'email' | 'feishu') {
  if (!order.value || busy.value) return
  busy.value = true
  error.value = ''
  try { order.value = (await shopAPI.retryNotification(order.value.order_id, channel)).data; message.value = '通知已重新排队，不会重复交付。' }
  catch { error.value = '通知重试失败，请稍后再试。' }
  finally { busy.value = false }
}
watch(() => route.params.id, () => { stepUp.onCancel(); order.value = null; delivery.value = ''; note.value = ''; preview.value = false; message.value = ''; void load() }, { immediate: true })
onBeforeUnmount(() => { generation++; delivery.value = ''; note.value = ''; order.value = null; stepUp.onCancel() })
</script>
<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-[1180px] py-2 sm:px-4 sm:py-4">
      <div class="mb-5 flex items-center justify-between gap-3">
        <RouterLink to="/admin/shop/orders" class="inline-flex min-h-11 items-center text-sm font-medium text-gray-600 hover:text-primary-700 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:text-gray-300 dark:hover:text-primary-300 dark:focus:ring-offset-gray-950">← 返回处理队列</RouterLink>
        <button type="button" class="btn btn-secondary min-h-11" :disabled="busy" @click="load">刷新订单</button>
      </div>

      <div v-if="busy && !order" class="rounded-xl border border-gray-200 bg-white p-8 text-center text-sm text-gray-500 dark:border-gray-800 dark:bg-gray-900 dark:text-gray-400" aria-busy="true">正在加载订单…</div>
      <div v-else-if="error && !order" role="alert" class="rounded-xl border border-red-200 bg-red-50 p-6 dark:border-red-900/70 dark:bg-red-950/40">
        <p class="text-sm text-red-800 dark:text-red-200">{{ error }}</p>
        <button type="button" class="mt-4 min-h-11 rounded-lg bg-primary-600 px-4 text-sm font-medium text-white focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2" @click="load">重新加载</button>
      </div>

      <template v-else-if="order">
        <div v-if="error" role="alert" class="mb-4 rounded-lg border border-red-200 bg-red-50 p-4 text-sm leading-6 text-red-800 dark:border-red-900/70 dark:bg-red-950/40 dark:text-red-200">{{ error }}</div>
        <div v-if="message" role="status" aria-live="polite" class="mb-4 rounded-lg border border-primary-200 bg-primary-50 p-4 text-sm text-primary-900 dark:border-primary-900 dark:bg-primary-950/40 dark:text-primary-100">{{ message }}</div>

        <div class="mb-5"><ShopOrderSummary :order="order" :show-guide="false" :show-notification="false" /></div>

        <div class="grid items-start gap-5 lg:grid-cols-[minmax(0,1fr)_300px]">
          <div class="min-w-0 space-y-5">
            <details class="rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-gray-900">
              <summary class="min-h-11 cursor-pointer content-center text-sm font-medium text-gray-700 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500 dark:text-gray-300">买家与订单信息</summary>
              <section aria-labelledby="buyer-title">
              <h2 id="buyer-title" class="sr-only">买家与订单信息</h2>
              <dl class="mt-4 grid grid-cols-[6rem_minmax(0,1fr)] gap-x-4 gap-y-3 text-sm sm:grid-cols-[7rem_minmax(0,1fr)]">
                <dt class="text-gray-500 dark:text-gray-400">用户 ID</dt><dd class="break-all text-gray-800 dark:text-gray-200">{{ order.user_id }}</dd>
                <dt class="text-gray-500 dark:text-gray-400">买家邮箱</dt><dd class="break-all text-gray-800 dark:text-gray-200">{{ order.user_email || '未提供' }}</dd>
                <dt class="text-gray-500 dark:text-gray-400">履约模式</dt><dd class="text-gray-800 dark:text-gray-200">{{ order.product.fulfillment_mode === 'session_topup' ? '提交资料 · 人工代充' : '人工账号交付' }}</dd>
                <dt class="text-gray-500 dark:text-gray-400">创建时间</dt><dd class="text-gray-800 dark:text-gray-200">{{ new Date(order.created_at).toLocaleString('zh-CN') }}</dd>
                <dt v-if="order.paid_at" class="text-gray-500 dark:text-gray-400">付款时间</dt><dd v-if="order.paid_at" class="text-gray-800 dark:text-gray-200">{{ new Date(order.paid_at).toLocaleString('zh-CN') }}</dd>
                <dt v-if="order.completed_at" class="text-gray-500 dark:text-gray-400">完成时间</dt><dd v-if="order.completed_at" class="text-gray-800 dark:text-gray-200">{{ new Date(order.completed_at).toLocaleString('zh-CN') }}</dd>
              </dl>
              </section>
            </details>

            <section v-if="order.product.fulfillment_mode === 'session_topup'" aria-labelledby="submission-title" class="rounded-xl border border-gray-200 bg-white p-4 md:p-5 dark:border-gray-800 dark:bg-gray-900">
              <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                <h2 id="submission-title" class="text-base font-semibold text-gray-900 dark:text-gray-100">目标账号与提交资料</h2>
              </div>
              <dl class="mt-4 grid grid-cols-[6rem_minmax(0,1fr)] gap-x-4 gap-y-3 text-sm">
                <dt class="text-gray-500 dark:text-gray-400">目标账号</dt><dd class="break-all font-medium text-gray-900 dark:text-gray-100">{{ order.input_account || '尚未识别' }}</dd>
                <dt class="text-gray-500 dark:text-gray-400">提交时间</dt><dd class="text-gray-800 dark:text-gray-200">{{ order.input_submitted_at ? new Date(order.input_submitted_at).toLocaleString('zh-CN') : '尚未提交' }}</dd>
              </dl>
              <div class="mt-4"><ShopSessionCheck v-if="order.input_check" :check="order.input_check" /><p v-else class="rounded-lg bg-gray-50 p-3 text-sm text-gray-600 dark:bg-gray-950 dark:text-gray-300">暂无初步检查结果。</p></div>
              <p v-if="order.can_start" class="mt-4 text-xs leading-5 text-gray-500 dark:text-gray-400">执行代充前先开始处理，锁定买家已提交的资料。</p>
            </section>

            <section v-if="order.product.fulfillment_mode === 'account_delivery' && order.can_complete" aria-labelledby="delivery-title" class="rounded-xl border border-gray-200 bg-white p-4 md:p-5 dark:border-gray-800 dark:bg-gray-900">
              <h2 id="delivery-title" class="text-base font-semibold text-gray-900 dark:text-gray-100">账号交付内容</h2>
              <p class="mt-1 text-sm leading-6 text-gray-500 dark:text-gray-400">填写需要完整交付给买家的原文。发送前必须预览并再次确认。</p>
              <label for="delivery-content" class="mt-4 block text-sm font-medium text-gray-700 dark:text-gray-200">交付原文</label>
              <textarea id="delivery-content" v-model="delivery" rows="8" required autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="65536" :disabled="busy" class="mt-2 w-full resize-y rounded-lg border border-gray-300 bg-white px-3 py-3 font-mono text-sm leading-6 text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500 disabled:opacity-60 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100" placeholder="输入账号、密码及其他必要交付信息"></textarea>
            </section>
          </div>

          <aside class="space-y-5 lg:sticky lg:top-6" aria-label="订单操作">
            <section class="rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-gray-900">
              <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">当前可执行操作</h2>
              <div class="mt-4 space-y-3">
                <button v-if="order.product.fulfillment_mode === 'session_topup' && order.can_reveal_input" type="button" :disabled="busy" class="min-h-11 w-full rounded-lg border border-gray-300 bg-white px-4 text-sm font-medium text-gray-800 hover:bg-gray-50 focus:outline-none focus:ring-2 focus:ring-primary-500 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-100 dark:hover:bg-gray-800" aria-live="polite" @click="copyInput">{{ copied ? '已复制' : '复制 Session' }}</button>
                <p v-if="order.product.fulfillment_mode === 'session_topup' && order.can_reveal_input" class="text-xs leading-5 text-gray-500 dark:text-gray-400">验证身份后复制完整 Session，页面不展示原文。</p>

                <button v-if="order.can_start" type="button" :disabled="busy" class="min-h-11 w-full rounded-lg bg-primary-600 px-4 text-sm font-medium text-white hover:bg-primary-700 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-60 dark:focus:ring-offset-gray-900" @click="act('start')">{{ busy ? '处理中…' : '开始处理' }}</button>

                <div v-if="order.product.fulfillment_mode === 'session_topup' && order.can_complete" class="border-t border-gray-100 pt-4 dark:border-gray-800">
                  <label for="request-note" class="block text-sm font-medium text-gray-700 dark:text-gray-200">要求补充资料</label>
                  <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">此操作只通知买家补充资料，不会完成订单。</p>
                  <textarea id="request-note" v-model="note" rows="4" required :disabled="busy" maxlength="2000" class="mt-2 w-full resize-y rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm leading-6 text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500 disabled:opacity-60 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100" placeholder="清楚说明需要买家补充或重新提交的内容"></textarea>
                  <button type="button" :disabled="busy || !note.trim()" class="mt-2 min-h-11 w-full rounded-lg border border-amber-500 px-4 text-sm font-medium text-amber-800 hover:bg-amber-50 focus:outline-none focus:ring-2 focus:ring-amber-500 disabled:cursor-not-allowed disabled:opacity-50 dark:text-amber-300 dark:hover:bg-amber-950/30" @click="act('request_input')">发送补充资料要求</button>

                  <button type="button" :disabled="busy" class="mt-4 min-h-11 w-full rounded-lg bg-primary-600 px-4 text-sm font-medium text-white hover:bg-primary-700 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-60 dark:focus:ring-offset-gray-900" @click="preview = true">确认代充完成</button>
                </div>

                <button v-if="order.product.fulfillment_mode === 'account_delivery' && order.can_complete" type="button" :disabled="busy || !delivery.trim()" class="min-h-11 w-full rounded-lg bg-primary-600 px-4 text-sm font-medium text-white hover:bg-primary-700 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:bg-gray-100 disabled:text-gray-500 dark:disabled:bg-gray-800 dark:disabled:text-gray-400 dark:focus:ring-offset-gray-900" @click="preview = true">预览并确认发货</button>
                <p v-if="order.product.fulfillment_mode === 'account_delivery' && order.can_complete && !delivery.trim()" class="text-xs leading-5 text-gray-500 dark:text-gray-400">填写交付原文后可预览发货。</p>

                <p v-if="!order.can_start && !order.can_complete && !order.can_reveal_input" class="rounded-lg bg-gray-50 p-3 text-sm leading-6 text-gray-600 dark:bg-gray-950 dark:text-gray-300">当前状态没有可执行操作。</p>
              </div>
            </section>

            <section aria-labelledby="notification-title" class="rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-gray-900">
              <h2 id="notification-title" class="text-base font-semibold text-gray-900 dark:text-gray-100">通知状态</h2>
              <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">通知结果独立于订单完成状态，重试不会重复交付。</p>
              <div class="mt-4 border-b border-gray-100 pb-4 dark:border-gray-800">
                <div class="flex items-center justify-between gap-3 text-sm"><span class="text-gray-500 dark:text-gray-400">买家邮件</span><span class="font-medium text-gray-900 dark:text-gray-100">{{ shopNotificationLabels[order.email_status] }}</span></div>
                <button v-if="order.email_status === 'FAILED'" type="button" :disabled="busy" class="mt-3 min-h-11 w-full rounded-lg border border-gray-300 px-3 text-sm font-medium text-gray-700 focus:outline-none focus:ring-2 focus:ring-primary-500 disabled:opacity-50 dark:border-gray-700 dark:text-gray-200" @click="retry('email')">重试邮件通知</button>
              </div>
              <div class="mt-4">
                <div class="flex items-center justify-between gap-3 text-sm"><span class="text-gray-500 dark:text-gray-400">管理员通知</span><span class="font-medium text-gray-900 dark:text-gray-100">{{ shopNotificationLabels[order.admin_notification_status] }}</span></div>
                <button v-if="order.admin_notification_status === 'FAILED'" type="button" :disabled="busy" class="mt-3 min-h-11 w-full rounded-lg border border-gray-300 px-3 text-sm font-medium text-gray-700 focus:outline-none focus:ring-2 focus:ring-primary-500 disabled:opacity-50 dark:border-gray-700 dark:text-gray-200" @click="retry('feishu')">重试飞书通知</button>
              </div>
            </section>
          </aside>
        </div>
      </template>

      <BaseDialog :show="preview" :title="order?.product.fulfillment_mode === 'account_delivery' ? '预览交付并确认' : '确认代充完成'" width="wide" :close-on-click-outside="!busy" :close-on-escape="!busy" :show-close-button="!busy" @close="preview = false">
        <div v-if="order">
          <template v-if="order.product.fulfillment_mode === 'account_delivery'">
            <p class="text-sm leading-6 text-gray-600 dark:text-gray-300">请核对下方完整交付原文。最终确认后订单将完成并进入邮件通知队列。</p>
            <pre class="mt-4 max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-gray-200 bg-gray-50 p-4 font-mono text-sm leading-6 text-gray-900 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100">{{ delivery }}</pre>
          </template>
          <template v-else>
            <div class="rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm leading-6 text-amber-950 dark:border-amber-900/70 dark:bg-amber-950/30 dark:text-amber-100">
              <p class="font-semibold">仅在你已经在站外实际完成本次代充后继续。</p>
              <p class="mt-2">若站外操作失败或订单资料有变化，请取消并刷新核对，不要重复操作。</p>
            </div>
          </template>

        </div>
        <template #footer>
          <div v-if="order" class="flex flex-col-reverse gap-3 sm:flex-row sm:justify-end">
            <button type="button" :disabled="busy" class="min-h-11 rounded-lg border border-gray-300 bg-white px-4 text-sm font-medium text-gray-700 focus:outline-none focus:ring-2 focus:ring-primary-500 disabled:opacity-50 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-200" @click="preview = false">取消</button>
            <button v-if="order.product.fulfillment_mode === 'account_delivery'" type="button" :disabled="busy || !delivery.trim()" class="min-h-11 rounded-lg bg-primary-600 px-4 text-sm font-medium text-white focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:opacity-60 dark:focus:ring-offset-gray-900" @click="act('complete')">{{ busy ? '提交中…' : '确认完成并通知用户' }}</button>
            <button v-else type="button" :disabled="busy" class="min-h-11 rounded-lg bg-primary-600 px-4 text-sm font-medium text-white focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:opacity-60 dark:focus:ring-offset-gray-900" @click="act('complete')">{{ busy ? '提交中…' : '确认代充完成' }}</button>
          </div>
        </template>
      </BaseDialog>

      <TotpStepUpDialog :controller="stepUp" />
    </div>
  </AppLayout>
</template>
