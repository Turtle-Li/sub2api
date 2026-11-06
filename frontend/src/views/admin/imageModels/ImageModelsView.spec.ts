import { ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useImageGroup } from './useImageGroup'
import en from '@/i18n/locales/en/admin/imageModels'
import ImageModelsView from '../ImageModelsView.vue'

vi.mock('./useImageGroup', () => ({ useImageGroup: vi.fn() }))
vi.mock('vue-router', () => ({ onBeforeRouteLeave: vi.fn() }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('./ImageGroupSettings.vue', () => ({ default: { template: '<div />' } }))
vi.mock('./ImageGroupModels.vue', () => ({ default: { template: '<div />' } }))
vi.mock('./ImageGroupAccounts.vue', () => ({ default: { template: '<div />' } }))

beforeEach(() => vi.restoreAllMocks())

describe('image management group selection', () => {
  it('restores the displayed selection when a dirty group switch is cancelled', async () => {
    const select = vi.fn()
    const state = {
      groups: ref([]), imageGroups: ref([
        { id: 40, name: 'Image', platform: 'openai', status: 'active' },
        { id: 41, name: 'Gemini', platform: 'gemini', status: 'active' },
      ]),
      group: ref(null), draft: ref(null), catalogue: ref(null), selectedId: ref(40),
      loading: ref(false), saving: ref(false), error: ref(''), saved: ref(false), dirty: ref(true),
      select, save: vi.fn(), loadGroups: vi.fn(), refreshCatalogue: vi.fn(),
    }
    // The route shell consumes only the listed group fields; full group contracts are tested by the composable.
    vi.mocked(useImageGroup).mockReturnValue(state as ReturnType<typeof useImageGroup>)
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const wrapper = mount(ImageModelsView, { global: {
      plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { admin: en, common: { refresh: 'Refresh' } } } })],
      stubs: { RouterLink: { template: '<a><slot /></a>' } },
    } })
    const selector = wrapper.get('select')
    expect(selector.element.value).toBe('40')
    await selector.setValue('41')
    await flushPromises()
    expect(confirm).toHaveBeenCalledOnce()
    expect(select).not.toHaveBeenCalled()
    expect(state.selectedId.value).toBe(40)
    expect(selector.element.value).toBe('40')
    wrapper.unmount()
  })
})
