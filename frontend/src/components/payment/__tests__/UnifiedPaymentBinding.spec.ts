import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import UnifiedPaymentBinding from '../UnifiedPaymentBinding.vue'
const mocks = vi.hoisted(() => ({ get: vi.fn(), bind: vi.fn(), manual: vi.fn() }))
vi.mock('@/api/admin/unifiedPaymentBinding', () => ({ getUnifiedPaymentBinding: mocks.get, bindUnifiedPayment: mocks.bind, useManualUnifiedPayment: mocks.manual }))
const initial = { base_url: 'https://pay.example.com', app_id: 'app.sub2.sandbox', environment: 'sandbox', return_url: 'https://www.turtleligpt.com/payment/result', webhook_url: 'https://api.turtleligpt.com/api/v1/payment/webhook/unified', revision: 0, configured: false, bootstrap_ready: true, runtime_enabled: false, pending_restart: false }
function render() { return mount(UnifiedPaymentBinding, { global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: {} })], stubs: { TotpStepUpDialog: true } } }) }
beforeEach(() => { vi.clearAllMocks(); mocks.get.mockResolvedValue({ ...initial }) })
describe('UnifiedPaymentBinding', () => {
 it('loads exact registered identity, return URL, and webhook endpoint', async () => { const w = render(); await flushPromises(); expect(w.text()).toContain(initial.app_id); expect(w.text()).toContain(initial.return_url); expect(w.text()).toContain(initial.webhook_url); expect(w.find('input[type="password"]').exists()).toBe(true) })
 it('saves with one stable idempotency key and clears the code after success', async () => {
  mocks.bind.mockRejectedValueOnce(new Error('timeout')).mockResolvedValueOnce({ ...initial, configured: true, pending_restart: true })
  const w = render(); await flushPromises(); await w.get('#unified-binding-code').setValue('x'.repeat(43))
  const save = w.findAll('button').find(b => b.text() === '验证并保存配置')!
  await save.trigger('click'); await flushPromises(); expect(w.get('[role="alert"]').text()).toContain('操作未完成')
  await save.trigger('click'); await flushPromises(); expect(mocks.bind).toHaveBeenCalledTimes(2)
 expect(mocks.bind.mock.calls[0][3]).toBe(mocks.bind.mock.calls[1][3]); expect(mocks.bind.mock.calls[0][1]).toBe('x'.repeat(43)); expect(mocks.bind.mock.calls[0][2]).toBe(0)
  expect((w.get('#unified-binding-code').element as HTMLInputElement).value).toBe(''); expect(w.get('[role="status"]').text()).toContain('重启服务后加载')
 })
 it('changes the save key when either its payload or loaded revision changes', async () => {
  mocks.bind
   .mockRejectedValueOnce(new Error('timeout'))
   .mockResolvedValueOnce({ ...initial, configured: true, revision: 1 })
   .mockResolvedValueOnce({ ...initial, configured: true, revision: 2 })
  const w = render(); await flushPromises()
  const codeInput = w.get('#unified-binding-code')
  const save = w.findAll('button').find(b => b.text() === '验证并保存配置')!
  await codeInput.setValue('x'.repeat(43)); await save.trigger('click'); await flushPromises()
  const firstKey = mocks.bind.mock.calls[0][3]
  await codeInput.setValue('y'.repeat(43)); await save.trigger('click'); await flushPromises()
  const secondKey = mocks.bind.mock.calls[1][3]
  await codeInput.setValue('z'.repeat(43)); await save.trigger('click'); await flushPromises()
  expect(firstKey).not.toBe(secondKey)
  expect(secondKey).not.toBe(mocks.bind.mock.calls[2][3])
  expect(mocks.bind.mock.calls[0][2]).toBe(0)
  expect(mocks.bind.mock.calls[1][2]).toBe(0)
  expect(mocks.bind.mock.calls[2][2]).toBe(1)
 })
 it('prevents binding before product initialization', async () => { mocks.get.mockResolvedValue({ ...initial, bootstrap_ready: false }); const w = render(); await flushPromises(); const save = w.findAll('button').find(b => b.text() === '验证并保存配置')!; expect(save.attributes('disabled')).toBeDefined(); await save.trigger('click'); expect(mocks.bind).not.toHaveBeenCalled() })
 it('retries manual mode with its stable key and keeps it separate from a completed save', async () => {
  mocks.bind.mockResolvedValue({ ...initial, configured: true, revision: 1 })
  mocks.manual.mockRejectedValueOnce(new Error('timeout')).mockResolvedValueOnce({ ...initial, pending_restart: true, revision: 2 })
  const w = render(); await flushPromises(); await w.get('#unified-binding-code').setValue('x'.repeat(43))
  const save = w.findAll('button').find(b => b.text() === '验证并保存配置')!
  await save.trigger('click'); await flushPromises()
  const saveKey = mocks.bind.mock.calls[0][3]
  const manual = () => w.findAll('button').find(b => b.text() === '使用手工配置')!
  await manual().trigger('click'); await flushPromises(); await manual().trigger('click'); await flushPromises()
  expect(mocks.manual).toHaveBeenCalledTimes(2)
  expect(mocks.manual.mock.calls[0][1]).toBe(mocks.manual.mock.calls[1][1])
  expect(mocks.manual.mock.calls[0][1]).not.toBe(saveKey)
  expect(mocks.manual.mock.calls[0][0]).toBe(1)
  expect(w.get('[role="status"]').text()).toContain('重启服务后加载')
 })
 it('offers retry when status retrieval fails', async () => { mocks.get.mockRejectedValueOnce(new Error('down')).mockResolvedValueOnce(initial); const w = render(); await flushPromises(); expect(w.get('[role="alert"]').text()).toContain('无法读取'); await w.get('button').trigger('click'); await flushPromises(); expect(w.text()).toContain(initial.app_id) })
})
