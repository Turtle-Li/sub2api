import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { ShopProduct } from '@/types/shop'
import ShopProductEditor from '@/components/shop/ShopProductEditor.vue'
import AdminShopProductsPanel from '@/components/shop/AdminShopProductsPanel.vue'

const api = vi.hoisted(() => ({ adminProducts: vi.fn(), actionIntegrations: vi.fn(), saveProduct: vi.fn() }))
vi.mock('@/api/shop', () => ({ shopAPI: api }))
const integration = { id: 'deepseek', version: 1, adapter: 'newapi.daypass.v1', enabled: true }
const product: ShopProduct = {
  id: 8, name: 'DeepSeek 订阅卡', category: 'API', summary: '', description: '', price: 9.9, currency: 'CNY',
  duration_value: 1, duration_unit: 'day', fulfillment_mode: 'account_delivery', input_format: 'text', input_url: '', input_instructions: '', guide_key: 'deepseek-daypass',
  enabled: true, sort_order: 0, revision: 3, created_at: '', updated_at: '', default_sku_id: 'day',
  payment_actions: [{ binding_key: 'primary-delivery', adapter: integration.adapter, integration_ref: integration.id, integration_version: 1, params: {} }],
  skus: [
    { id: 'day', attributes: { 周期: '日卡' }, price: 9.9, duration_value: 1, duration_unit: 'day', concurrency: 2, enabled: true },
    { id: 'week', attributes: { 周期: '周卡' }, price: 49.9, duration_value: 7, duration_unit: 'day', concurrency: 3, enabled: true },
  ]
}
enableAutoUnmount(afterEach)
beforeEach(() => { vi.clearAllMocks(); api.adminProducts.mockResolvedValue({ data: [product] }); api.actionIntegrations.mockResolvedValue({ data: [integration] }) })

describe('unified SKU configuration', () => {
  it('saves independent prices, duration and default SKU without mutating the supplied product', async () => {
    const wrapper = mount(ShopProductEditor, { props: { product, busy: false, integrations: [integration] } })
    await wrapper.get('#product-editor-sku').trigger('click')
    await wrapper.get('[aria-label="规格 2 售价"]').setValue(39.9)
    await wrapper.get('[aria-label="规格 2 设为默认"]').setValue()
    await wrapper.get('#product-editor-basic').trigger('click')
    await wrapper.get('#guide-key').setValue('')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('save')?.[0]?.[0]).toMatchObject({ price: 39.9, duration_value: 7, default_sku_id: 'week', skus: [{ id: 'day', price: 9.9 }, { id: 'week', price: 39.9, concurrency: 3 }] })
    expect(product.skus?.[1].price).toBe(49.9)
    expect(product.guide_key).toBe('deepseek-daypass')
  })
  it('generates normal attribute combinations and preserves prices for existing combinations', async () => {
    const manual = { ...product, payment_actions: [], enabled: false, skus: product.skus?.map(sku => ({ ...sku, concurrency: 0 })) }
    const wrapper = mount(ShopProductEditor, { props: { product: manual, busy: false } })
    await wrapper.get('#product-editor-sku').trigger('click')
    await wrapper.get('[aria-label="属性 1 值"]').setValue('日卡，周卡，月卡')
    await wrapper.findAll('button').find(button => button.text() === '生成规格表')!.trigger('click')
    expect(wrapper.findAll('input[aria-label$="售价"]')).toHaveLength(3)
    expect((wrapper.get('[aria-label="规格 2 售价"]').element as HTMLInputElement).value).toBe('49.9')
    expect((wrapper.get('[aria-label="规格 3 售价"]').element as HTMLInputElement).value).toBe('0')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('save')?.[0]?.[0]).toMatchObject({ skus: [{ id: 'day' }, { id: 'week' }, { attributes: { 周期: '月卡' } }] })
  })
  it('keeps the editor and authored price after a server revision conflict', async () => {
    api.saveProduct.mockRejectedValue({ message: '商品已被其他管理员修改，请刷新后重试', code: 'SHOP_CONFLICT' })
    const wrapper = mount(AdminShopProductsPanel, { global: { stubs: {
      RouterLink: { template: '<a><slot /></a>' },
      DataTable: { props: ['data'], template: '<div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div>' },
      BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
    } } })
    await flushPromises()
    await wrapper.get('[aria-label="编辑 DeepSeek 订阅卡"]').trigger('click')
    await wrapper.get('[aria-label="规格 2 售价"]').setValue(39.9)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('其他管理员修改')
    expect((wrapper.get('[aria-label="规格 2 售价"]').element as HTMLInputElement).value).toBe('39.9')
    expect(api.saveProduct).toHaveBeenCalledWith(8, expect.objectContaining({ revision: 3 }))
  })
  it('fills a partial DeepSeek template with consistent attributes while keeping existing IDs and prices', async () => {
    const wrapper = mount(ShopProductEditor, { props: { product: { ...product, enabled: false }, busy: false, integrations: [integration] } })
    await wrapper.get('#product-editor-sku').trigger('click')
    await wrapper.findAll('button').find(button => button.text() === '载入日卡 / 周卡规格')!.trigger('click')
    await wrapper.get('form').trigger('submit')
    const saved = wrapper.emitted('save')?.[0]?.[0] as { skus: ShopProduct['skus'] }
    expect(saved.skus).toHaveLength(4)
    expect(saved.skus?.every(sku => Object.keys(sku.attributes).sort().join('/') === '并发/有效期')).toBe(true)
    expect(saved.skus?.find(sku => sku.id === 'day')?.price).toBe(9.9)
    expect(saved.skus?.find(sku => sku.id === 'week')?.price).toBe(49.9)
    expect(product.skus?.[0].attributes).toEqual({ 周期: '日卡' })
  })
})
