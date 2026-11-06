import { defineComponent, h } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as groupsAPI from '@/api/admin/groups'
import { getImageModels, type ImageModelCatalogue } from '@/api/admin/imageModels'
import type { AdminGroup } from '@/types'
import { useImageGroup } from './useImageGroup'

vi.mock('@/api/admin/groups', () => ({ getAllIncludingInactive: vi.fn(), getById: vi.fn(), update: vi.fn() }))
vi.mock('@/api/admin/imageModels', () => ({ getImageModels: vi.fn() }))
const catalogue: ImageModelCatalogue = { models: [], candidates: [], unavailable_reason: null }
const original = {
  id: 1, name: 'Group A', platform: 'openai', allow_image_generation: true,
  allow_batch_image_generation: true, image_rate_independent: false, image_rate_multiplier: 1,
  batch_image_discount_multiplier: 0.5, batch_image_hold_multiplier: 1,
  image_price_1k: null, image_price_2k: null, image_price_4k: null,
  model_allowlist: { enabled: true, models: ['text-*', 'muse-image'] },
} as AdminGroup
function harness() {
  return mount(defineComponent({ setup: () => ({ state: useImageGroup() }), render: () => h('div') }))
}

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(groupsAPI.getAllIncludingInactive).mockResolvedValue([original])
  vi.mocked(groupsAPI.getById).mockResolvedValue(original)
  vi.mocked(getImageModels).mockResolvedValue(catalogue)
})

describe('image group configuration lifecycle', () => {
  it('sends only changes and resets the draft after a successful save', async () => {
    const wrapper = harness()
    await flushPromises()
    const state = wrapper.vm.state
    if (!state.draft.value) throw new Error('draft missing')
    state.draft.value.image_price_1k = 0.12
    vi.mocked(groupsAPI.update).mockResolvedValue({ ...original, image_price_1k: 0.12 })
    await state.save()
    expect(groupsAPI.update).toHaveBeenCalledWith(1, { image_price_1k: 0.12 })
    expect(state.dirty.value).toBe(false)
    expect(state.saved.value).toBe(true)
    wrapper.unmount()
  })
  it('retains the draft and exposes save errors', async () => {
    const wrapper = harness()
    await flushPromises()
    const state = wrapper.vm.state
    if (!state.draft.value) throw new Error('draft missing')
    state.draft.value.image_price_1k = 0.12
    vi.mocked(groupsAPI.update).mockRejectedValue(new Error('Price rejected'))
    await state.save()
    expect(state.error.value).toBe('Price rejected')
    expect(state.dirty.value).toBe(true)
    expect(state.saving.value).toBe(false)
    wrapper.unmount()
  })
  it('ignores a stale group request after switching groups', async () => {
    const wrapper = harness()
    await flushPromises()
    let finishOld: ((group: AdminGroup) => void) | undefined
    vi.mocked(groupsAPI.getById).mockImplementation(id => id === 2
      ? new Promise(resolve => { finishOld = resolve })
      : Promise.resolve({ ...original, id }))
    const oldRequest = wrapper.vm.state.select(2)
    await wrapper.vm.state.select(3)
    if (!finishOld) throw new Error('old request missing')
    finishOld({ ...original, id: 2 })
    await oldRequest
    expect(wrapper.vm.state.group.value?.id).toBe(3)
    expect(wrapper.vm.state.selectedId.value).toBe(3)
    wrapper.unmount()
  })
  it('keeps failed catalogue reads unknown rather than showing stale availability', async () => {
    const wrapper = harness()
    await flushPromises()
    vi.mocked(getImageModels).mockRejectedValue(new Error('Unavailable'))
    await wrapper.vm.state.select(2)
    expect(wrapper.vm.state.catalogue.value).toBe(null)
    expect(wrapper.vm.state.group.value).toBe(null)
    expect(wrapper.vm.state.error.value).toBe('Unavailable')
    wrapper.unmount()
  })
  it('clears previously available models when a refresh fails after configuration changed', async () => {
    vi.mocked(getImageModels).mockResolvedValue({ ...catalogue, models: [{ id: 'muse-image', provider: 'muse_images', object: 'image.batch.model' }] })
    const wrapper = harness()
    await flushPromises()
    vi.mocked(getImageModels).mockRejectedValue(new Error('Refresh unavailable'))
    await wrapper.vm.state.refreshCatalogue()
    expect(wrapper.vm.state.catalogue.value?.models).toEqual([])
    expect(wrapper.vm.state.catalogue.value?.unavailable_reason).toBe('unknown')
    expect(wrapper.vm.state.error.value).toBe('Refresh unavailable')
    wrapper.unmount()
  })
  it('ignores an older same-group catalogue refresh arriving after a newer refresh', async () => {
    const wrapper = harness()
    await flushPromises()
    let finishOld: ((result: ImageModelCatalogue) => void) | undefined
    vi.mocked(getImageModels).mockImplementationOnce(() => new Promise(resolve => { finishOld = resolve }))
    const oldRefresh = wrapper.vm.state.refreshCatalogue()
    const latest = { ...catalogue, unavailable_reason: 'no_available_models' }
    vi.mocked(getImageModels).mockResolvedValue(latest)
    await wrapper.vm.state.refreshCatalogue()
    if (!finishOld) throw new Error('old request missing')
    finishOld({ ...catalogue, models: [{ id: 'muse-image', provider: 'muse_images', object: 'image.batch.model' }] })
    await oldRefresh
    expect(wrapper.vm.state.catalogue.value).toEqual(latest)
    wrapper.unmount()
  })
  it('ignores an older same-group refresh failure after a newer successful response', async () => {
    const wrapper = harness()
    await flushPromises()
    let failOld: ((error: Error) => void) | undefined
    vi.mocked(getImageModels).mockImplementationOnce(() => new Promise((_resolve, reject) => { failOld = reject }))
    const oldRefresh = wrapper.vm.state.refreshCatalogue()
    await wrapper.vm.state.refreshCatalogue()
    if (!failOld) throw new Error('old request missing')
    failOld(new Error('Stale failure'))
    await oldRefresh
    expect(wrapper.vm.state.catalogue.value).toEqual(catalogue)
    expect(wrapper.vm.state.error.value).toBe('')
    wrapper.unmount()
  })
})
