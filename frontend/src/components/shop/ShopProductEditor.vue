<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import type { ShopIntegrationOption, ShopProduct, ShopProductInput } from '@/types/shop'
const props = withDefaults(defineProps<{ product: ShopProduct | null; busy: boolean; integrations?: ShopIntegrationOption[] }>(), { integrations: () => [] })
const emit = defineEmits<{ save: [input: ShopProductInput] }>()
const defaults = (): ShopProductInput => ({ name: '', category: '', summary: '', description: '', price: 0, duration_value: 1, duration_unit: 'month', fulfillment_mode: 'session_topup', input_format: 'session_json', input_url: '', input_instructions: '', guide_key: '', enabled: false, sort_order: 0 })
const draft = reactive(defaults())
const paramsText = ref('{}')
const actionError = ref('')
const selectedIntegration = computed({
  get: () => draft.payment_actions?.[0] ? `${draft.payment_actions[0].integration_ref}@${draft.payment_actions[0].integration_version}` : '',
  set: (key: string) => {
    const integration = props.integrations.find(item => `${item.id}@${item.version}` === key)
    draft.payment_actions = integration ? [{ binding_key: 'primary-delivery', adapter: integration.adapter, integration_ref: integration.id, integration_version: integration.version, params: {} }] : []
    paramsText.value = '{}'; actionError.value = ''
    if (integration) draft.fulfillment_mode = 'account_delivery'
    if (integration?.adapter === 'newapi.daypass.v1') Object.assign(draft, { price: 6, duration_value: 1, duration_unit: 'day', guide_key: 'deepseek-daypass' })
  }
})
watch(() => draft.guide_key, (key) => {
  if (key === 'deepseek-daypass') Object.assign(draft, { fulfillment_mode: 'account_delivery', price: 6, duration_value: 1, duration_unit: 'day', input_format: 'text', input_url: '', input_instructions: '' })
})
watch(() => props.product, product => { Object.assign(draft, defaults(), { payment_actions: [] }, product ?? {}); draft.revision = product?.revision; paramsText.value = JSON.stringify(product?.payment_actions?.[0]?.params ?? {}, null, 2); actionError.value = '' }, { immediate: true })
function isParams(value: unknown): value is Record<string, unknown> { return typeof value === 'object' && value !== null && !Array.isArray(value) }
function save() {
  actionError.value = ''
  if (draft.payment_actions?.[0]) {
    try {
      const params: unknown = JSON.parse(paramsText.value)
      if (!isParams(params) || new TextEncoder().encode(paramsText.value).length > 4096) throw new Error('invalid params')
      draft.payment_actions = [{ ...draft.payment_actions[0], params }]
    } catch { actionError.value = '请输入 4KB 以内的 JSON 对象参数。'; return }
  }
  emit('save', { name: draft.name, category: draft.category, summary: draft.summary, description: draft.description, price: draft.price, duration_value: draft.duration_value, duration_unit: draft.duration_unit, fulfillment_mode: draft.fulfillment_mode, input_format: draft.input_format, input_url: draft.input_url, input_instructions: draft.input_instructions, guide_key: draft.guide_key, enabled: draft.enabled, sort_order: draft.sort_order, revision: draft.revision, payment_actions: draft.payment_actions ?? [] })
}
</script>
<template>
  <form id="shop-product-editor" class="space-y-4" @submit.prevent="save">
    <fieldset :disabled="busy" class="min-w-0 space-y-4">
      <section aria-labelledby="editor-basic" class="rounded-xl border border-gray-200 p-4 md:p-5 dark:border-gray-700">
        <div class="mb-4">
          <h2 id="editor-basic" class="text-base font-semibold text-gray-900 dark:text-gray-100">基础信息</h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">用于商品列表与详情页展示，详情支持 Markdown。</p>
        </div>
        <div class="grid gap-4 md:grid-cols-2">
          <div>
            <div class="flex items-center justify-between gap-3"><label for="product-name" class="text-sm font-medium text-gray-700 dark:text-gray-200">商品名称 <span class="text-red-600" aria-hidden="true">*</span></label><span id="product-name-count" class="text-xs text-gray-400">{{ draft.name.length }}/160</span></div>
            <input id="product-name" aria-describedby="product-name-count" v-model="draft.name" type="text" required maxlength="160" autocomplete="off" class="mt-1.5 min-h-11 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100" />
          </div>
          <div>
            <div class="flex items-center justify-between gap-3"><label for="product-category" class="text-sm font-medium text-gray-700 dark:text-gray-200">分类</label><span id="product-category-count" class="text-xs text-gray-400">{{ draft.category.length }}/80</span></div>
            <input id="product-category" aria-describedby="product-category-count" v-model="draft.category" type="text" maxlength="80" autocomplete="off" class="mt-1.5 min-h-11 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100" />
          </div>
        </div>
        <div class="mt-4">
          <div class="flex items-center justify-between gap-3"><label for="product-summary" class="text-sm font-medium text-gray-700 dark:text-gray-200">摘要</label><span id="product-summary-count" class="text-xs text-gray-400">{{ draft.summary.length }}/1000</span></div>
          <textarea id="product-summary" aria-describedby="product-summary-count" v-model="draft.summary" rows="2" maxlength="1000" class="mt-1.5 w-full resize-y rounded-lg border border-gray-300 bg-white px-3 py-2.5 text-sm leading-6 text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100"></textarea>
        </div>
        <div class="mt-4">
          <div class="flex items-center justify-between gap-3"><label for="product-description" class="text-sm font-medium text-gray-700 dark:text-gray-200">Markdown 详情</label><span id="product-description-count" class="text-xs text-gray-400">{{ draft.description.length }}/50000</span></div>
          <textarea id="product-description" aria-describedby="product-description-count" v-model="draft.description" rows="5" maxlength="50000" class="mt-1.5 w-full resize-y rounded-lg border border-gray-300 bg-white px-3 py-2.5 font-mono text-sm leading-6 text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100" placeholder="输入商品说明、适用范围和必要注意事项"></textarea>
        </div>
      </section>

      <section aria-labelledby="editor-payment-action" class="rounded-xl border border-gray-200 p-4 md:p-5 dark:border-gray-700">
        <h2 id="editor-payment-action" class="text-base font-semibold text-gray-900 dark:text-gray-100">付款后操作</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">选择人工交付或已登记的自动服务。修改配置只影响新订单。</p>
        <label for="payment-action" class="mt-4 block text-sm font-medium text-gray-700 dark:text-gray-200">处理方式</label>
        <select id="payment-action" v-model="selectedIntegration" class="mt-1.5 min-h-11 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-900 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100">
          <option value="">人工处理</option>
          <option v-for="item in integrations" :key="`${item.id}@${item.version}`" :value="`${item.id}@${item.version}`">{{ item.adapter === 'newapi.daypass.v1' ? 'DeepSeek 自动发 Key' : 'HTTP 钩子' }} · {{ item.id }} v{{ item.version }}{{ item.enabled ? '' : '（尚未启用）' }}</option>
          <option v-if="selectedIntegration && !integrations.some(item => `${item.id}@${item.version}` === selectedIntegration)" :value="selectedIntegration">原配置 {{ selectedIntegration }}（当前未登记）</option>
        </select>
        <template v-if="draft.payment_actions?.length">
          <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">自动交付仅支持无需买家补充资料的商品。接口与签名凭据由部署配置管理，本页只填业务参数。</p>
          <label for="payment-action-params" class="mt-4 block text-sm font-medium text-gray-700 dark:text-gray-200">业务参数（JSON）</label>
          <textarea id="payment-action-params" v-model="paramsText" rows="4" spellcheck="false" :aria-invalid="!!actionError" aria-describedby="payment-action-help" class="mt-1.5 w-full rounded-lg border border-gray-300 bg-white p-3 font-mono text-sm text-gray-900 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100"></textarea>
          <p id="payment-action-help" class="mt-1 text-xs text-gray-500 dark:text-gray-400">例如 SKU 或服务规格。不要填写 API Key、密码或签名密钥。</p>
        </template>
        <p v-if="actionError" role="alert" class="mt-2 text-sm text-red-700 dark:text-red-300">{{ actionError }}</p>
      </section>

      <section aria-labelledby="editor-sales" class="rounded-xl border border-gray-200 p-4 md:p-5 dark:border-gray-700">
        <div class="mb-4"><h2 id="editor-sales" class="text-base font-semibold text-gray-900 dark:text-gray-100">销售设置</h2><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">价格单位为人民币；启用商品时价格不得低于 0.01。</p></div>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <label for="product-price" class="text-sm font-medium text-gray-700 dark:text-gray-200">价格（CNY）</label>
            <div class="relative mt-1.5"><span class="pointer-events-none absolute inset-y-0 left-3 flex items-center text-gray-500" aria-hidden="true">¥</span><input id="product-price" v-model.number="draft.price" type="number" required :min="draft.enabled ? 0.01 : 0" step="0.01" class="min-h-11 w-full rounded-lg border border-gray-300 bg-white py-2 pl-8 pr-3 text-sm text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100" /></div>
          </div>
          <div>
            <label for="duration-value" class="text-sm font-medium text-gray-700 dark:text-gray-200">时长数值</label>
            <input id="duration-value" v-model.number="draft.duration_value" type="number" required min="1" step="1" inputmode="numeric" class="mt-1.5 min-h-11 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100" />
          </div>
          <div>
            <label for="duration-unit" class="text-sm font-medium text-gray-700 dark:text-gray-200">时长单位</label>
            <select id="duration-unit" v-model="draft.duration_unit" class="mt-1.5 min-h-11 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100"><option value="month">个月</option><option value="day">天</option><option value="year">年</option></select>
          </div>
        </div>
      </section>

      <section v-if="!draft.payment_actions?.length" aria-labelledby="editor-fulfillment" class="rounded-xl border border-gray-200 p-4 md:p-5 dark:border-gray-700">
        <div class="mb-4"><h2 id="editor-fulfillment" class="text-base font-semibold text-gray-900 dark:text-gray-100">履约设置</h2><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">切换模式不会清空其他字段。资料来源仅接受 HTTPS 地址。</p></div>
        <div class="grid gap-4 md:grid-cols-2">
          <div>
            <label for="fulfillment-mode" class="text-sm font-medium text-gray-700 dark:text-gray-200">履约模式</label>
            <select id="fulfillment-mode" v-model="draft.fulfillment_mode" class="mt-1.5 min-h-11 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100"><option value="session_topup">提交资料 · 人工代充</option><option value="account_delivery">人工账号交付</option></select>
          </div>
          <div>
            <label for="input-format" class="text-sm font-medium text-gray-700 dark:text-gray-200">输入格式</label>
            <select id="input-format" v-model="draft.input_format" class="mt-1.5 min-h-11 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100"><option value="session_json">Session JSON</option><option value="text">原始文本</option></select>
          </div>
        </div>
        <div class="mt-4">
          <label for="input-url" class="text-sm font-medium text-gray-700 dark:text-gray-200">HTTPS 资料来源</label>
          <input id="input-url" v-model="draft.input_url" type="url" pattern="https://.*" autocomplete="off" placeholder="https://chatgpt.com/api/auth/session" class="mt-1.5 min-h-11 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100" />
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">代充默认使用 ChatGPT 会话页面，其他来源请填写 HTTPS 地址。</p>
        </div>
        <div class="mt-4">
          <div class="flex items-center justify-between gap-3"><label for="input-instructions" class="text-sm font-medium text-gray-700 dark:text-gray-200">买家输入说明</label><span id="input-instructions-count" class="text-xs text-gray-400">{{ draft.input_instructions.length }}/5000</span></div>
          <textarea id="input-instructions" aria-describedby="input-instructions-count" v-model="draft.input_instructions" rows="3" maxlength="5000" class="mt-1.5 w-full resize-y rounded-lg border border-gray-300 bg-white px-3 py-2.5 text-sm leading-6 text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100" placeholder="说明如何打开来源页面并复制全部原始内容"></textarea>
        </div>
      </section>

      <section aria-labelledby="editor-display" class="rounded-xl border border-gray-200 p-4 md:p-5 dark:border-gray-700">
        <div class="mb-4"><h2 id="editor-display" class="text-base font-semibold text-gray-900 dark:text-gray-100">展示与指南</h2><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">控制指南入口、列表顺序和商品是否可用。</p></div>
        <div class="grid gap-4 md:grid-cols-2">
          <div>
            <label for="guide-key" class="text-sm font-medium text-gray-700 dark:text-gray-200">使用指南</label>
            <select id="guide-key" v-model="draft.guide_key" class="mt-1.5 min-h-11 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100"><option value="">不使用指南</option><option value="chatgpt-plus-account">ChatGPT Plus 账号使用指南</option><option value="deepseek-daypass">DeepSeek API 使用指南</option></select>
            <p v-if="draft.guide_key === 'deepseek-daypass' && !draft.payment_actions?.length" class="mt-2 text-xs leading-5 text-amber-700 dark:text-amber-300">指南仅用于说明。自动发 Key 还需在“付款后操作”中选择 DeepSeek 集成。</p>
          </div>
          <div>
            <label for="sort-order" class="text-sm font-medium text-gray-700 dark:text-gray-200">排序值</label>
            <input id="sort-order" v-model.number="draft.sort_order" type="number" required step="1" class="mt-1.5 min-h-11 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100" />
          </div>
        </div>
        <label for="product-enabled" class="mt-4 flex min-h-11 cursor-pointer items-start gap-3 rounded-lg bg-gray-50 p-3 dark:bg-gray-950">
          <input id="product-enabled" v-model="draft.enabled" type="checkbox" class="mt-0.5 h-5 w-5 shrink-0 rounded border-gray-300 text-primary-600 focus:ring-2 focus:ring-primary-500 dark:border-gray-600" />
          <span><span class="block text-sm font-medium text-gray-800 dark:text-gray-100">启用并上线商品</span><span class="mt-0.5 block text-xs leading-5 text-gray-500 dark:text-gray-400">关闭时保留为草稿或停用状态；启用前请核对价格、时长与履约配置。</span></span>
        </label>
        <p v-if="draft.revision !== undefined" class="mt-3 text-xs text-gray-500 dark:text-gray-400">当前编辑版本：{{ draft.revision }}</p>
      </section>
    </fieldset>

  </form>
</template>
