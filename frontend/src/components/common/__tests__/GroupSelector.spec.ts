import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import GroupSelector from '../GroupSelector.vue'

const authState = { isSimpleMode: false }

vi.mock('@/stores', () => ({ useAuthStore: () => authState }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const groups = [
  { id: 1, name: 'Basic', platform: 'anthropic', status: 'active' },
  { id: 2, name: 'Composite', platform: 'composite', status: 'active' }
] as any

const mountSelector = (modelValue: number[] = []) => mount(GroupSelector, {
  props: { modelValue, groups },
  global: { stubs: { GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' }, Icon: true } }
})

describe('GroupSelector simple-mode binding policy', () => {
  beforeEach(() => { authState.isSimpleMode = false })

  it('hides composite groups in simple mode and preserves basic groups', () => {
    authState.isSimpleMode = true
    const wrapper = mountSelector()
    expect(wrapper.text()).toContain('Basic')
    expect(wrapper.text()).not.toContain('Composite')
  })

  it('keeps composite groups available in advanced mode', () => {
    const wrapper = mountSelector()
    expect(wrapper.text()).toContain('Composite')
  })

  it('cleans hidden historical composite IDs while preserving visible selections', () => {
    authState.isSimpleMode = true
    const wrapper = mountSelector([1, 2])
    expect(wrapper.emitted('update:modelValue')).toEqual([[[1]]])
  })

  it.each(['opencode_go', 'command_code', 'cline'])('allows %s accounts in model-provider groups', (platform) => {
    const providerGroups = ['anthropic', 'openai', 'gemini', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax']
      .map((provider, index) => ({ id: index + 10, name: `group-${provider}`, platform: provider, status: 'active' }))
    const wrapper = mount(GroupSelector, {
      props: { modelValue: [], platform: platform as any, groups: providerGroups as any, searchable: false },
      global: { stubs: { GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' }, Icon: true } }
    })
    for (const group of providerGroups) expect(wrapper.text()).toContain(group.name)
  })

  it('keeps ordinary single-provider accounts restricted', () => {
    const wrapper = mount(GroupSelector, {
      props: { modelValue: [], platform: 'deepseek', groups },
      global: { stubs: { GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' }, Icon: true } }
    })
    expect(wrapper.text()).not.toContain('Basic')
    expect(wrapper.text()).toContain('Composite')
  })
})
