<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import CatalogTabs from '@/components/common/CatalogTabs.vue'
import ShopSKUConfig from './ShopSKUConfig.vue'
import { editableShopSKUs, newShopSKU, skuAttributeKey } from '@/utils/shopSku'
import type { ShopIntegrationOption, ShopProduct, ShopProductInput } from '@/types/shop'
const props = withDefaults(defineProps<{ product: ShopProduct | null; busy: boolean; integrations?: ShopIntegrationOption[] }>(), { integrations: () => [] })
const emit = defineEmits<{ save: [input: ShopProductInput] }>()
const defaults = (): ShopProductInput => ({ name: '', category: '', summary: '', description: '', price: 0, duration_value: 1, duration_unit: 'month', fulfillment_mode: 'session_topup', input_format: 'session_json', input_url: '', input_instructions: '', guide_key: '', enabled: false, sort_order: 0, skus: [], default_sku_id: '', payment_actions: [] })
const draft = reactive(defaults())
const tab = ref('basic')
const tabs = [{ value: 'basic', label: '基本信息' }, { value: 'sku', label: '规格与价格' }, { value: 'delivery', label: '交付设置' }]
const delivery = ref<'manual' | 'automatic'>('manual')
const individualCopy = ref(false)
const paramsText = ref('{}')
const error = ref('')
const native = computed(() => delivery.value === 'automatic' && draft.payment_actions?.[0]?.adapter === 'newapi.daypass.v1')
const selectedIntegration = computed({
  get: () => draft.payment_actions?.[0] ? `${draft.payment_actions[0].integration_ref}@${draft.payment_actions[0].integration_version}` : '',
  set: (key: string) => {
    const integration = props.integrations.find(item => `${item.id}@${item.version}` === key)
    draft.payment_actions = integration ? [{ binding_key: 'primary-delivery', adapter: integration.adapter, integration_ref: integration.id, integration_version: integration.version, params: {} }] : []
    paramsText.value = '{}'
  }
})
const missingIntegration = computed(() => selectedIntegration.value && !props.integrations.some(item => `${item.id}@${item.version}` === selectedIntegration.value))
watch(() => props.product, product => {
  const source = product ? JSON.parse(JSON.stringify(product)) as ShopProduct : null
  Object.assign(draft, defaults(), source ?? {})
  draft.revision = product?.revision
  draft.skus = source ? editableShopSKUs(source) : [newShopSKU(draft)]
  draft.default_sku_id = source?.default_sku_id || draft.skus[0].id
  delivery.value = source?.payment_actions?.length ? 'automatic' : 'manual'
  individualCopy.value = delivery.value === 'manual' && draft.fulfillment_mode === 'account_delivery' && source?.delivery_config?.individual_copy === true
  paramsText.value = JSON.stringify(source?.payment_actions?.[0]?.params ?? {}, null, 2)
  error.value = ''; tab.value = 'basic'
}, { immediate: true })
watch(() => [delivery.value, draft.fulfillment_mode], () => {
  if (delivery.value !== 'manual' || draft.fulfillment_mode !== 'account_delivery') individualCopy.value = false
}, { flush: 'sync' })
function invalid(message: string, panel: string) { error.value = message; tab.value = panel }
function save() {
  error.value = ''
  if (!draft.name.trim()) { invalid('请填写商品名称。', 'basic'); return }
  const skus = draft.skus ?? []
  const defaultSKU = skus.find(sku => sku.id === draft.default_sku_id)
  if (!skus.length || !defaultSKU || (draft.enabled && !defaultSKU.enabled)) { invalid('请选择一个已上架的默认规格。', 'sku'); return }
  if (new Set(skus.map(sku => skuAttributeKey(sku.attributes))).size !== skus.length) { invalid('规格属性组合重复，请重新生成规格表。', 'sku'); return }
  if (skus.some(sku => !Number.isFinite(sku.price) || sku.price < 0 || sku.price > 1000000 || Math.abs(sku.price * 100 - Math.round(sku.price * 100)) > 0.000001 || (draft.enabled && sku.enabled && sku.price <= 0) || !Number.isInteger(sku.duration_value) || sku.duration_value < 1 || sku.duration_value > 3650)) { invalid('请检查各规格价格与有效期。已上架规格价格须大于 0，最多保留两位小数。', 'sku'); return }
  if (native.value && skus.some(sku => sku.duration_unit !== 'day' || ![1, 7].includes(sku.duration_value) || ![2, 3].includes(sku.concurrency))) { invalid('DeepSeek 规格请选择 1 天或 7 天有效期，以及 2 路或 3 路并发。可使用规格模板创建。', 'sku'); return }
  if (delivery.value === 'automatic' && !draft.payment_actions?.length) { invalid('请选择自动交付服务。', 'delivery'); return }
  if (delivery.value === 'automatic' && !native.value && draft.payment_actions?.[0]) {
    try {
      const params: unknown = JSON.parse(paramsText.value)
      if (!params || typeof params !== 'object' || Array.isArray(params) || new TextEncoder().encode(paramsText.value).length > 4096) throw new Error('invalid params')
      draft.payment_actions = [{ ...draft.payment_actions[0], params: params as Record<string, unknown> }]
    } catch { invalid('请输入 4KB 以内的 JSON 对象参数。', 'delivery'); return }
  }
  emit('save', {
    name: draft.name.trim(), category: draft.category.trim(), summary: draft.summary, description: draft.description,
    price: defaultSKU.price, duration_value: defaultSKU.duration_value, duration_unit: defaultSKU.duration_unit,
    skus: skus.map(sku => ({ ...sku, attributes: { ...sku.attributes }, concurrency: native.value ? sku.concurrency : 0 })), default_sku_id: defaultSKU.id,
    fulfillment_mode: delivery.value === 'automatic' ? 'account_delivery' : draft.fulfillment_mode,
    input_format: draft.input_format, input_url: draft.input_url, input_instructions: draft.input_instructions,
    guide_key: draft.guide_key, enabled: draft.enabled, sort_order: draft.sort_order, revision: draft.revision,
    payment_actions: delivery.value === 'automatic' ? draft.payment_actions ?? [] : [],
    delivery_config: delivery.value === 'manual' && draft.fulfillment_mode === 'account_delivery' && individualCopy.value ? { individual_copy: true } : undefined
  })
}
</script>
<template>
  <form id="shop-product-editor" novalidate @submit.prevent="save">
    <fieldset :disabled="busy" class="min-w-0 space-y-5 text-gray-900 dark:text-gray-100">
      <CatalogTabs v-model="tab" id="product-editor" label="商品配置" :options="tabs" />
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
      <section v-show="tab === 'basic'" id="product-editor-panel-basic" role="tabpanel" aria-labelledby="product-editor-basic" class="space-y-5">
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="text-sm font-medium">商品名称 <span class="text-red-500">*</span><input id="product-name" v-model="draft.name" class="input mt-1.5" maxlength="160" placeholder="如：DeepSeek API 订阅卡" /></label>
          <label class="text-sm font-medium">商品分类<input id="product-category" v-model="draft.category" class="input mt-1.5" maxlength="80" placeholder="如：API 服务、账号、代充" /></label>
        </div>
        <label class="block text-sm font-medium">商品简介<textarea id="product-summary" v-model="draft.summary" class="input mt-1.5" rows="2" maxlength="600" placeholder="一句话介绍商品，展示在商城列表中"></textarea></label>
        <label class="block text-sm font-medium">商品详情<textarea id="product-description" v-model="draft.description" class="input mt-1.5" rows="6" maxlength="50000" placeholder="介绍商品权益、适用范围和购买须知，支持 Markdown"></textarea></label>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="text-sm font-medium">使用指南<select id="guide-key" v-model="draft.guide_key" class="input mt-1.5"><option value="">不附加指南</option><option value="chatgpt-plus-account">ChatGPT Plus 账号指南</option><option value="deepseek-daypass">DeepSeek API 使用指南</option></select><span class="mt-1 block text-xs font-normal leading-5 text-gray-500">指南仅控制说明内容，价格与交付方式单独设置。</span></label>
          <label class="text-sm font-medium">排序值<input id="sort-order" v-model.number="draft.sort_order" type="number" step="1" class="input mt-1.5" /><span class="mt-1 block text-xs font-normal text-gray-500">数值越小，展示越靠前。</span></label>
        </div>
        <label class="flex min-h-11 items-center gap-3 border-t border-gray-100 pt-4 text-sm dark:border-dark-700"><input id="product-enabled" v-model="draft.enabled" type="checkbox" class="h-4 w-4 rounded" /><span><span class="block font-medium">上架商品</span><span class="mt-0.5 block text-xs text-gray-500">关闭后保留配置，买家无法下单。</span></span></label>
      </section>
      <section v-show="tab === 'sku'" id="product-editor-panel-sku" role="tabpanel" aria-labelledby="product-editor-sku">
        <ShopSKUConfig :model-value="draft.skus ?? []" :default-id="draft.default_sku_id ?? ''" :native="native" @update:model-value="draft.skus = $event" @update:default-id="draft.default_sku_id = $event" />
      </section>
      <section v-show="tab === 'delivery'" id="product-editor-panel-delivery" role="tabpanel" aria-labelledby="product-editor-delivery" class="space-y-5">
        <fieldset><legend class="mb-2 text-sm font-medium">交付方式</legend><div class="grid gap-3 sm:grid-cols-2"><label :class="['flex cursor-pointer items-start gap-3 rounded-lg border p-4', delivery === 'manual' ? 'border-primary-500 bg-primary-50/50 dark:bg-primary-900/10' : 'border-gray-200 dark:border-dark-700']"><input v-model="delivery" type="radio" name="delivery-mode" value="manual" class="mt-1" /><span><span class="block text-sm font-medium">人工交付</span><span class="mt-1 block text-xs leading-5 text-gray-500">代充、成品号。收款后进入处理队列。</span></span></label><label :class="['flex cursor-pointer items-start gap-3 rounded-lg border p-4', delivery === 'automatic' ? 'border-primary-500 bg-primary-50/50 dark:bg-primary-900/10' : 'border-gray-200 dark:border-dark-700']"><input v-model="delivery" type="radio" name="delivery-mode" value="automatic" class="mt-1" /><span><span class="block text-sm font-medium">自动交付</span><span class="mt-1 block text-xs leading-5 text-gray-500">付款后调用服务，由回调返回交付结果。</span></span></label></div></fieldset>
        <template v-if="delivery === 'manual'">
          <label class="block text-sm font-medium">人工处理类型<select id="fulfillment-mode" v-model="draft.fulfillment_mode" class="input mt-1.5"><option value="session_topup">代充 · 买家提交资料</option><option value="account_delivery">成品号 · 管理员交付账号</option></select></label>
          <template v-if="draft.fulfillment_mode === 'session_topup'">
            <div class="grid gap-4 sm:grid-cols-2"><label class="text-sm font-medium">资料格式<select id="input-format" v-model="draft.input_format" class="input mt-1.5"><option value="session_json">Session JSON</option><option value="text">原始文本</option></select></label><label class="text-sm font-medium">资料来源链接<input id="input-url" v-model="draft.input_url" type="url" class="input mt-1.5" placeholder="https://chatgpt.com/api/auth/session" /></label></div>
            <label class="block text-sm font-medium">买家提交说明<textarea id="input-instructions" v-model="draft.input_instructions" rows="4" maxlength="5000" class="input mt-1.5" placeholder="说明买家需提交哪些资料，以及如何获取"></textarea></label>
          </template>
          <div v-else class="space-y-4">
            <p class="rounded-lg bg-gray-50 p-4 text-sm leading-6 text-gray-500 dark:bg-dark-800">付款后进入人工处理队列。管理员处理订单时填写账号与使用说明，买家在订单中查看交付内容。</p>
            <label class="flex min-h-11 items-start gap-3 text-sm">
              <input id="individual-copy" v-model="individualCopy" type="checkbox" class="mt-1 h-4 w-4 rounded" />
              <span><span class="block font-medium">开启逐项复制</span><span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">默认仅复制完整内容。开启后，成品号支持分别复制账号、密码；其他格式按行复制。仅对新订单生效。</span></span>
            </label>
          </div>
        </template>
        <template v-else>
          <label class="block text-sm font-medium">自动交付服务<select id="payment-action" v-model="selectedIntegration" class="input mt-1.5"><option value="">请选择服务</option><option v-if="missingIntegration" :value="selectedIntegration">{{ selectedIntegration }}（当前不可用）</option><option v-for="integration in integrations" :key="`${integration.id}@${integration.version}`" :value="`${integration.id}@${integration.version}`" :disabled="!integration.enabled">{{ integration.adapter === 'newapi.daypass.v1' ? 'DeepSeek API Key' : '链接 / 回调交付' }} · {{ integration.id }} v{{ integration.version }}{{ integration.enabled ? '' : '（停用）' }}</option></select></label>
          <p v-if="!integrations.length" class="text-sm text-amber-700 dark:text-amber-300">尚无可用的自动交付服务。请先配置服务集成，再上架自动交付商品。</p>
          <div v-if="native" class="rounded-lg bg-gray-50 p-4 text-sm leading-6 text-gray-600 dark:bg-dark-800 dark:text-gray-300">从所选 SKU 读取有效期、并发与价格，调用 DeepSeek 服务获取 API Key。请到「规格与价格」配置日卡、周卡及对应售价。</div>
          <details v-else-if="selectedIntegration" class="rounded-lg border border-gray-200 p-4 dark:border-dark-700"><summary class="cursor-pointer text-sm font-medium">高级服务参数</summary><label class="mt-3 block text-sm">商品参数（JSON）<textarea id="payment-action-params" v-model="paramsText" rows="4" spellcheck="false" class="input mt-1.5 font-mono text-xs"></textarea></label><p class="mt-2 text-xs leading-5 text-gray-500">SKU 与订单金额随请求发送。此处仅填写服务需要的商品参数，不填写密码或密钥。</p></details>
        </template>
      </section>
    </fieldset>
  </form>
</template>
