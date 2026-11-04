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
  try {
    const id = order.value.order_id
    const revision = order.value.revision
    const revealed = await stepUp.run(() => shopAPI.revealInput(id, revision))
    await navigator.clipboard.writeText(revealed.data.content)
    message.value = `已复制第 ${revealed.data.input_revision} 版完整资料，请核对目标账号。`
  } catch (err) { if (!isStepUpCancelled(err)) error.value = '无法复制资料。请检查剪贴板权限或刷新订单后重试。' }
  finally { busy.value = false }
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
watch(() => route.params.id, () => { order.value = null; delivery.value = ''; note.value = ''; preview.value = false; message.value = ''; void load() }, { immediate: true })
onBeforeUnmount(() => { generation++; delivery.value = ''; note.value = ''; order.value = null; stepUp.onCancel() })
</script>
<template>
  <AppLayout><div class="mx-auto max-w-3xl space-y-5">
    <header class="flex items-center justify-between gap-3"><RouterLink to="/admin/shop/orders" class="text-sm text-primary-700 dark:text-primary-300">← 人工交付队列</RouterLink><button class="btn btn-secondary btn-sm" :disabled="busy" @click="load">刷新订单</button></header>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-4 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
    <p v-if="busy && !order" role="status">正在加载订单…</p>
    <template v-if="order">
      <ShopOrderSummary :order="order" />
      <section class="card space-y-4 p-6"><h2 class="text-lg font-semibold">处理信息</h2><p class="break-all text-sm">买家：{{ order.user_email || `#${order.user_id}` }}</p><ShopSessionCheck v-if="order.input_check" :check="order.input_check" /><p v-else-if="order.input_account" class="break-all text-sm">目标账号：{{ order.input_account }}</p><p v-if="order.input_submitted_at" class="text-sm text-gray-500 dark:text-gray-400">资料提交：{{ new Date(order.input_submitted_at).toLocaleString() }} · 第 {{ order.input_revision }} 版</p>
        <div class="flex flex-wrap gap-3"><button v-if="order.can_reveal_input" class="btn btn-secondary" :disabled="busy" @click="copyInput">复制完整 Session / 资料</button><button v-if="order.can_start" class="btn btn-primary" :disabled="busy" @click="act('start')">开始处理</button></div>
        <p v-if="order.can_start" class="text-xs text-gray-500 dark:text-gray-400">执行站外操作前先开始处理，锁定当前资料版本，避免与退款或资料更新冲突。</p>
      </section>
      <section v-if="order.product.fulfillment_mode === 'session_topup' && order.can_complete" class="card space-y-4 p-6"><h2 class="text-lg font-semibold">需要补充资料</h2><label class="block text-sm">给用户的说明<textarea v-model="note" class="input mt-2" rows="3" maxlength="2000" placeholder="说明需要补充什么，请勿粘贴密码或 Session" /></label><button class="btn btn-secondary" :disabled="busy || !note.trim()" @click="act('request_input')">请求补充资料</button></section>
      <section v-if="order.can_complete" class="card space-y-4 p-6"><h2 class="text-lg font-semibold">{{ order.product.fulfillment_mode === 'account_delivery' ? '填写发货资料' : '确认人工代充结果' }}</h2>
        <template v-if="order.product.fulfillment_mode === 'account_delivery'"><label class="block text-sm">完整交付信息<textarea v-model="delivery" class="input mt-2 min-h-52 font-mono text-sm" autocomplete="off" :spellcheck="false" maxlength="65536" placeholder="按行填写适用的账号、密码、邮箱密码、登录地址及备注" /></label><p class="text-xs text-gray-500 dark:text-gray-400">用户将在订单和邮件中收到以下原文。仅填写该商品实际需要的资料。</p></template>
        <p v-else class="text-sm text-gray-500 dark:text-gray-400">请确认已按当前第 {{ order.input_revision }} 版资料完成站外代充。确认后无法再次复制 Session。</p>
        <button class="btn btn-primary" :disabled="busy || (order.product.fulfillment_mode === 'account_delivery' && !delivery.trim())" @click="preview = true">{{ order.product.fulfillment_mode === 'account_delivery' ? '预览并确认发货' : '确认代充完成' }}</button>
      </section>
      <section class="card space-y-3 p-6"><h2 class="text-lg font-semibold">通知记录</h2><div class="flex flex-wrap items-center justify-between gap-3 text-sm"><span>买家邮件：{{ shopNotificationLabels[order.email_status] }}</span><button v-if="order.email_status === 'FAILED'" class="btn btn-secondary btn-sm" :disabled="busy" @click="retry('email')">重试邮件</button></div><div class="flex flex-wrap items-center justify-between gap-3 text-sm"><span>管理员通知：{{ shopNotificationLabels[order.admin_notification_status] }}</span><button v-if="order.admin_notification_status === 'FAILED'" class="btn btn-secondary btn-sm" :disabled="busy" @click="retry('feishu')">重试飞书</button></div><p class="text-xs text-gray-500 dark:text-gray-400">通知失败不会改变交付结果，重试仅发送通知。</p></section>
    </template>
    <p v-if="message" role="status" class="text-sm text-primary-700 dark:text-primary-300">{{ message }}</p>
  </div>
  <BaseDialog :show="preview" title="确认人工交付" :close-on-click-outside="!busy" :close-on-escape="!busy" :show-close-button="!busy" @close="preview = false"><p class="mb-4 text-sm">请核对订单、账号和交付结果。确认后将完成订单并通知买家。</p><pre v-if="delivery" class="max-h-80 overflow-y-auto whitespace-pre-wrap break-all rounded-lg bg-gray-50 p-4 text-sm dark:bg-dark-800">{{ delivery }}</pre><template #footer><div class="flex justify-end gap-3"><button class="btn btn-secondary" :disabled="busy" @click="preview = false">返回检查</button><button class="btn btn-primary" :disabled="busy" @click="act('complete')">{{ busy ? '提交中…' : '确认完成并通知用户' }}</button></div></template></BaseDialog>
  <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>
