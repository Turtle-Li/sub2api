import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AccountListItem } from '@/types'
import * as accountsAPI from '@/api/admin/accounts'
import { setWorkBuddyImagePolicy } from '@/api/admin/imageModels'
import en from '@/i18n/locales/en/admin/imageModels'
import ImageGroupAccounts from './ImageGroupAccounts.vue'

vi.mock('@/api/admin/accounts', () => ({ list: vi.fn(), getById: vi.fn() }))
vi.mock('@/api/admin/imageModels', () => ({ setWorkBuddyImagePolicy: vi.fn() }))
vi.mock('@/api/admin/proxies', () => ({ getAll: vi.fn() }))
vi.mock('@/api/admin/accountPools', () => ({ list: vi.fn() }))
vi.mock('@/components/account/EditAccountModal.vue', () => ({ default: { template: '<div />' } }))
const account = {
  id: 20, name: 'Hunyuan', platform: 'openai', type: 'apikey', status: 'active',
  credentials: { model_mapping: { 'cn:hunyuan-image-alpha': 'cn:hunyuan-image-alpha' } },
  extra: { workbuddy_image_native_retry_accepted: false, workbuddy_image_single_attempt_verified: true, unrelated: 'retain' },
} as AccountListItem
function harness() {
  return mount(ImageGroupAccounts, {
    props: { groupId: 7, groups: [], candidates: [{ id: 'cn:hunyuan-image-alpha', object: 'image.batch.model', provider: 'workbuddy_images' }] },
    global: {
      plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { admin: en, common: { loading: 'Loading', retry: 'Retry' } } } })],
      stubs: { RouterLink: { template: '<a><slot /></a>' } },
    },
  })
}
beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(accountsAPI.list).mockResolvedValue({ items: [structuredClone(account)], total: 1, page: 1, page_size: 10, pages: 1 })
})
describe('linked image accounts', () => {
  it('uses the scoped full account list and preserves the separate verified mode', async () => {
    const wrapper = harness()
    await flushPromises()
    expect(accountsAPI.list).toHaveBeenCalledWith(1, 10, { group: '7' })
    expect(wrapper.text()).toContain('verified single-attempt flag')
    vi.mocked(setWorkBuddyImagePolicy).mockResolvedValue(true)
    await wrapper.get('[role="switch"]').trigger('click')
    await flushPromises()
    expect(setWorkBuddyImagePolicy).toHaveBeenCalledWith(20, true)
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.emitted('changed')).toHaveLength(1)
    expect(wrapper.text()).toContain('verified single-attempt flag')
    wrapper.unmount()
  })
  it('shows a failed policy save without optimistically changing readiness', async () => {
    vi.mocked(setWorkBuddyImagePolicy).mockRejectedValue(new Error('Policy rejected'))
    const wrapper = harness()
    await flushPromises()
    await wrapper.get('[role="switch"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('Policy rejected')
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.emitted('changed')).toBeUndefined()
    wrapper.unmount()
  })
  it('does not show an acceptance switch for incomplete or unrelated account metadata', async () => {
    vi.mocked(accountsAPI.list).mockResolvedValue({ items: [{ ...account, credentials: undefined }], total: 1, page: 1, page_size: 10, pages: 1 })
    const wrapper = harness()
    await flushPromises()
    expect(wrapper.find('[role="switch"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Edit account')
    wrapper.unmount()
  })
})
