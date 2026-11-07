import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { beforeEach, expect, it, vi } from 'vitest'
import { getImageModels } from '@/api/admin/imageModels'
import type { AdminGroup } from '@/types'
import ImageModelDirectory from './ImageModelDirectory.vue'
import en from '@/i18n/locales/en/admin/imageModels'

vi.mock('@/api/admin/imageModels', () => ({ getImageModels: vi.fn() }))
beforeEach(() => vi.clearAllMocks())
const group = (id: number, enabled = true) => ({ id, platform: 'openai', allow_image_generation: enabled, allow_batch_image_generation: enabled, image_price_1k: 0, image_price_2k: 1, image_price_4k: null }) as AdminGroup
const muse = { id: 'muse-image', provider: 'muse_images', object: 'model', capabilities: { image_sizes: ['1K'], aspect_ratios: [], default_image_size: '1K', default_aspect_ratio: '', max_reference_images: 1, response_mime_types: [] } }
const mountDirectory = (groups = [group(1), group(2), group(3, false)]) => mount(ImageModelDirectory, { props: { groups, revision: 0 }, global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { admin: en, common: { loading: 'Loading' } } } })] } })

it('begins with deduplicated models, ignores text-only groups and chooses a configuration-ready binding', async () => {
  vi.mocked(getImageModels).mockImplementation(async id => ({ candidates: [muse], models: id === 2 ? [muse] : [], unavailable_reason: null }))
  const wrapper = mountDirectory()
  await flushPromises()
  expect(getImageModels).toHaveBeenCalledTimes(2)
  expect(getImageModels).not.toHaveBeenCalledWith(3)
  expect(wrapper.findAll('article')).toHaveLength(1)
  expect(wrapper.find('select').exists()).toBe(false)
  expect(wrapper.get('article').text()).toContain('0/ 张')
  expect(wrapper.get('article').text()).not.toContain('0 – 1')
  await wrapper.get('button').trigger('click')
  expect(wrapper.emitted('configure')).toEqual([[2, 'muse-image']])
  wrapper.unmount()
})

it('reloads availability after saved configuration or account policy changes', async () => {
  vi.mocked(getImageModels).mockResolvedValue({ candidates: [muse], models: [], unavailable_reason: null })
  const wrapper = mountDirectory([group(1)])
  await flushPromises()
  expect(wrapper.text()).toContain('Candidate')
  vi.mocked(getImageModels).mockResolvedValue({ candidates: [muse], models: [muse], unavailable_reason: null })
  await wrapper.setProps({ revision: 1 })
  await flushPromises()
  expect(wrapper.text()).toContain('Configuration available')
  wrapper.unmount()
})
