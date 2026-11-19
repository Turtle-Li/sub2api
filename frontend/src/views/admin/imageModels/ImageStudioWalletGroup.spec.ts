import { flushPromises, mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import { getImageStudioWalletGroup, setImageStudioWalletGroup } from '@/api/admin/imageModels'
import type { AdminGroup } from '@/types'
import ImageStudioWalletGroup from './ImageStudioWalletGroup.vue'

vi.mock('@/api/admin/imageModels', () => ({ getImageStudioWalletGroup: vi.fn(), setImageStudioWalletGroup: vi.fn() }))

it('saves only the workbench selection with compare-and-set, then can pause it without rebinding accounts', async () => {
  const off = { group_id: 0, account_ids: [] }
  const active = { group_id: 45, account_ids: [249, 252, 253] }
  vi.mocked(getImageStudioWalletGroup).mockResolvedValue(off)
  vi.mocked(setImageStudioWalletGroup).mockResolvedValueOnce(active).mockResolvedValueOnce(off)
  const group = { id: 45, name: '生图', platform: 'openai', subscription_type: 'standard', status: 'active', allow_image_generation: true, is_exclusive: true } as AdminGroup
  const wrapper = mount(ImageStudioWalletGroup, { props: { groups: [group] } })
  await flushPromises()
  expect(wrapper.get('#image-studio-wallet-description').text()).toContain('无需创建 API Key 或加入分组')
  expect(wrapper.get('option[value="45"]').text()).toBe('生图')
  await wrapper.get('select').setValue('45')
  await wrapper.get('input').setValue('249, 249')
  expect(wrapper.get('button[type="submit"]').element).toHaveProperty('disabled', true)
  await wrapper.get('input').setValue('253，249, 252')
  await wrapper.get('form').trigger('submit')
  await flushPromises()
  expect(setImageStudioWalletGroup).toHaveBeenNthCalledWith(1, active, off)
  await wrapper.get('select').setValue('0')
  await wrapper.get('form').trigger('submit')
  await flushPromises()
  expect(setImageStudioWalletGroup).toHaveBeenNthCalledWith(2, off, active)
  wrapper.unmount()
})
