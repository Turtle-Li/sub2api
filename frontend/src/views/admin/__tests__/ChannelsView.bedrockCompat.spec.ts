import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Channel, CreateChannelRequest, UpdateChannelRequest } from '@/api/admin/channels'
import ChannelsView from '@/views/admin/ChannelsView.vue'

const { listChannels, createChannel, updateChannel, previewPlatformDisplayModels, getGroups, showSuccess, showError } = vi.hoisted(() => ({
  listChannels: vi.fn(),
  createChannel: vi.fn(),
  updateChannel: vi.fn(),
  previewPlatformDisplayModels: vi.fn(),
  getGroups: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    channels: { list: listChannels, create: createChannel, update: updateChannel, previewPlatformDisplayModels },
    groups: { getAll: getGroups },
    settings: { getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false }) }
  }
}))

vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showError }) }))

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string, values?: { models?: string } | string) =>
    typeof values === 'object' && values.models ? `${key} ${values.models}` : key })
}))

function mountView() {
  return mount(ChannelsView, {
    global: {
      stubs: {
        AppLayout: defineComponent({ template: '<main><slot /></main>' }),
        TablePageLayout: defineComponent({
          template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>'
        }),
        BaseDialog: defineComponent({
          props: ['show'],
          template: '<section v-if="show"><slot /><slot name="footer" /></section>'
        }),
        DataTable: defineComponent({
          props: ['data'],
          template: '<div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div>'
        }),
        Pagination: true,
        ConfirmDialog: true,
        EmptyState: true,
        Select: true,
        Icon: true,
        PlatformIcon: true,
        PricingEntryCard: true
      }
    }
  })
}

type View = ReturnType<typeof mountView>

async function clickButton(wrapper: View, text: string) {
  const button = wrapper.findAll('button').find(button => button.text() === text)
  expect(button).toBeDefined()
  await button!.trigger('click')
  await flushPromises()
}

function platformCheckbox(wrapper: View, platform: string) {
  return wrapper.findAll('label')
    .find(label => label.text() === `admin.groups.platforms.${platform}`)!
    .get('input[type="checkbox"]')
}

function bedrockToggle(wrapper: View) {
  return wrapper.findAll('[role="switch"]')
    .find(toggle => toggle.element.parentElement?.textContent?.includes('admin.channels.form.bedrockCCCompat'))!
}

function makeChannel(features: Record<string, unknown>): Channel {
  return {
    id: 1, name: 'Bedrock relay', description: '', status: 'active',
    billing_model_source: 'channel_mapped', restrict_models: false,
    features_config: features, group_ids: [11, 22], model_pricing: [], model_mapping: {},
    apply_pricing_to_account_stats: false, account_stats_pricing_rules: [],
    created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z'
  }
}

describe('ChannelsView channel feature configuration', () => {
  let stored: Channel[]

  beforeEach(() => {
    vi.clearAllMocks()
    previewPlatformDisplayModels.mockReset()
    localStorage.clear()
    stored = []
    listChannels.mockImplementation(async () => ({ items: stored, total: stored.length }))
    getGroups.mockResolvedValue([
      { id: 11, name: 'Anthropic group', platform: 'anthropic', rate_multiplier: 1 },
      { id: 22, name: 'OpenAI group', platform: 'openai', rate_multiplier: 1 }
    ])
    createChannel.mockImplementation(async (request: CreateChannelRequest) => {
      const channel = makeChannel(request.features_config || {})
      channel.group_ids = request.group_ids || []
      stored = [channel]
      return channel
    })
    updateChannel.mockImplementation(async (_id: number, request: UpdateChannelRequest) => {
      stored = [{ ...stored[0], ...request } as Channel]
      return stored[0]
    })
  })

  it.each([true, false])('creates and reopens a channel with compatibility %s', async enabled => {
    const wrapper = mountView()
    await flushPromises()
    await clickButton(wrapper, 'admin.channels.createChannel')
    await wrapper.get('input[required]').setValue('Bedrock relay')
    await platformCheckbox(wrapper, 'anthropic').setValue(true)
    await clickButton(wrapper, 'admin.groups.platforms.anthropic')
    await wrapper.findAll('label').find(label => label.text().includes('Anthropic group'))!
      .get('input[type="checkbox"]').setValue(true)
    if (enabled) await bedrockToggle(wrapper).trigger('click')

    await wrapper.get('#channel-form').trigger('submit')
    await flushPromises()
    expect(showError).not.toHaveBeenCalled()
    expect(createChannel).toHaveBeenCalledOnce()
    expect(createChannel.mock.calls[0][0].features_config.bedrock_cc_compat).toBe(enabled)

    await clickButton(wrapper, 'common.edit')
    await clickButton(wrapper, 'admin.groups.platforms.anthropic')
    expect(bedrockToggle(wrapper).attributes('aria-checked')).toBe(String(enabled))
    wrapper.unmount()
  })

  it.each([true, false])('updates compatibility to %s while preserving other features', async enabled => {
    stored = [makeChannel({ bedrock_cc_compat: true, future_feature: { enabled: true } })]
    const wrapper = mountView()
    await flushPromises()
    await clickButton(wrapper, 'common.edit')
    await clickButton(wrapper, 'admin.groups.platforms.anthropic')
    expect(bedrockToggle(wrapper).attributes('aria-checked')).toBe('true')
    if (!enabled) await bedrockToggle(wrapper).trigger('click')

    await wrapper.get('#channel-form').trigger('submit')
    await flushPromises()
    expect(showError).not.toHaveBeenCalled()
    expect(updateChannel).toHaveBeenCalledOnce()
    expect(updateChannel.mock.calls[0][1].features_config).toMatchObject({
      bedrock_cc_compat: enabled,
      future_feature: { enabled: true }
    })

    await clickButton(wrapper, 'common.edit')
    await clickButton(wrapper, 'admin.groups.platforms.anthropic')
    expect(bedrockToggle(wrapper).attributes('aria-checked')).toBe(String(enabled))
    wrapper.unmount()
  })

  it('explicitly disables compatibility when the Anthropic section is removed', async () => {
    stored = [makeChannel({ bedrock_cc_compat: true, future_feature: { enabled: true } })]
    const wrapper = mountView()
    await flushPromises()
    await clickButton(wrapper, 'common.edit')
    await platformCheckbox(wrapper, 'anthropic').setValue(false)

    await wrapper.get('#channel-form').trigger('submit')
    await flushPromises()
    expect(showError).not.toHaveBeenCalled()
    expect(updateChannel).toHaveBeenCalledOnce()
    expect(updateChannel.mock.calls[0][1]).toMatchObject({
      group_ids: [22],
      features_config: { bedrock_cc_compat: false, future_feature: { enabled: true } }
    })
    wrapper.unmount()
  })

  it('previews GPT display changes on click and saves only the display snapshot', async () => {
    const channel = makeChannel({ gpt_display_models_by_group: { '22': ['gpt-6.1-sol'] }, future_feature: { enabled: true } })
    channel.model_mapping = { openai: { 'gpt-6-sol': 'gpt-6-sol' } }
    stored = [channel]
    previewPlatformDisplayModels.mockResolvedValue({ previous_models_by_group: { '22': ['gpt-6.1-sol'] }, models_by_group: { '22': ['gpt-6.2-astra', 'gpt-6.2-sol'] } })
    const wrapper = mountView()
    await flushPromises()
    await clickButton(wrapper, 'common.edit')
    await clickButton(wrapper, 'admin.groups.platforms.openai')
    expect(previewPlatformDisplayModels).not.toHaveBeenCalled()
    await clickButton(wrapper, 'admin.channels.form.accountDisplaySync')
    expect(previewPlatformDisplayModels).toHaveBeenCalledOnce()
    expect(previewPlatformDisplayModels.mock.calls[0][0]).toBe('openai')
    expect(previewPlatformDisplayModels.mock.calls[0][1]).toMatchObject({ channel_id: 1, group_ids: [22], model_mapping: channel.model_mapping })
    expect(updateChannel).not.toHaveBeenCalled()
    const preview = wrapper.get('details')
    expect(preview.text()).toContain('OpenAI group')
    expect(preview.text()).toContain('gptDisplayAdded gpt-6.2-astra, gpt-6.2-sol')
    expect(preview.text()).toContain('gptDisplayRemoved gpt-6.1-sol')
    await wrapper.get('#channel-form').trigger('submit')
    await flushPromises()
    const payload = updateChannel.mock.calls[0][1]
    expect(payload.features_config).not.toHaveProperty('gpt_display_models_by_group')
    expect(payload.display_model_updates).toEqual({ openai: { '22': ['gpt-6.2-astra', 'gpt-6.2-sol'] } })
    expect(payload.features_config.future_feature).toEqual({ enabled: true })
    expect(payload.model_pricing).toEqual([])
    expect(payload.model_mapping).toEqual(channel.model_mapping)
    wrapper.unmount()
  })

  it('syncs Kimi and a platform without a pricing catalog independently', async () => {
    const channel = makeChannel({ claude_display_models_by_group: { '11': ['leave-alone'] } })
    channel.group_ids = [33, 44]
    stored = [channel]
    getGroups.mockResolvedValue([{ id: 33, platform: 'kimi', name: 'Kimi via WorkBuddy' }, { id: 44, platform: 'cline', name: 'Cline' }])
    const wrapper = mountView()
    await flushPromises()
    await clickButton(wrapper, 'common.edit')
    for (const [platform, id, models] of [['kimi', 33, ['kimi-k3']], ['cline', 44, []]] as const) {
      await clickButton(wrapper, `admin.groups.platforms.${platform}`)
      previewPlatformDisplayModels.mockResolvedValueOnce({ previous_models_by_group: { [id]: ['retired'] }, models_by_group: { [id]: models } })
      await clickButton(wrapper, 'admin.channels.form.accountDisplaySync')
      expect(previewPlatformDisplayModels).toHaveBeenLastCalledWith(platform, expect.objectContaining({ channel_id: 1, group_ids: [id] }), expect.any(AbortSignal))
      expect(wrapper.get('details').text()).toContain('retired')
    }
    expect(updateChannel).not.toHaveBeenCalled()
    await wrapper.get('#channel-form').trigger('submit')
    await flushPromises()
    const { features_config: features, display_model_updates, model_mapping, model_pricing } = updateChannel.mock.calls[0][1]
    expect(display_model_updates).toEqual({ kimi: { 33: ['kimi-k3'] }, cline: { 44: [] } })
    expect(features).not.toHaveProperty('claude_display_models_by_group')
    expect(model_mapping).toEqual({})
    expect(model_pricing).toEqual([])
    wrapper.unmount()
  })

  it('does not save a preview after the display filters change', async () => {
    const saved = { '22': ['gpt-6.1-sol'] }
    stored = [makeChannel({ gpt_display_models_by_group: saved })]
    previewPlatformDisplayModels.mockResolvedValue({ previous_models_by_group: { '22': ['gpt-6.1-sol'] }, models_by_group: { '22': ['gpt-6.2-sol'] } })
    const wrapper = mountView()
    await flushPromises()
    await clickButton(wrapper, 'common.edit')
    await clickButton(wrapper, 'admin.groups.platforms.openai')
    await clickButton(wrapper, 'admin.channels.form.accountDisplaySync')
    await clickButton(wrapper, 'admin.channels.form.basicSettings')
    await wrapper.get('textarea[placeholder="admin.channels.form.plazaHiddenModelsPlaceholder"]').setValue('gpt-6.2-sol')
    await clickButton(wrapper, 'admin.groups.platforms.openai')
    expect(wrapper.get('[role="status"]').text()).toBe('admin.channels.form.gptDisplayStale')
    await wrapper.get('#channel-form').trigger('submit')
    await flushPromises()
    expect(updateChannel.mock.calls[0][1]).not.toHaveProperty('display_model_updates')
    expect(updateChannel.mock.calls[0][1].features_config).not.toHaveProperty('openai_account_display_models_by_group')
    expect(updateChannel.mock.calls[0][1].features_config).not.toHaveProperty('gpt_display_models_by_group')
    wrapper.unmount()
  })

  it('keeps the saved GPT list when the preview fails', async () => {
    const saved = { '22': ['gpt-6.1-sol'] }
    stored = [makeChannel({ gpt_display_models_by_group: saved })]
    previewPlatformDisplayModels.mockRejectedValue(new Error('Cannot read group models'))
    const wrapper = mountView()
    await flushPromises()
    await clickButton(wrapper, 'common.edit')
    await clickButton(wrapper, 'admin.groups.platforms.openai')
    await clickButton(wrapper, 'admin.channels.form.accountDisplaySync')
    expect(wrapper.get('[role="alert"]').text()).toBe('Cannot read group models')
    await wrapper.get('#channel-form').trigger('submit')
    await flushPromises()
    expect(updateChannel.mock.calls[0][1]).not.toHaveProperty('display_model_updates')
    expect(updateChannel.mock.calls[0][1].features_config).not.toHaveProperty('openai_account_display_models_by_group')
    expect(updateChannel.mock.calls[0][1].features_config).not.toHaveProperty('gpt_display_models_by_group')
    wrapper.unmount()
  })

  it('previews removal of a manual Claude model and saves an explicit empty group', async () => {
    const channel = makeChannel({ gpt_display_models_by_group: { '22': ['gpt-6.2-sol'] } })
    channel.model_mapping = { anthropic: { 'my-old-alias': 'claude-3-7-sonnet-20250219' } }
    stored = [channel]
    previewPlatformDisplayModels.mockResolvedValue({
      previous_models_by_group: { '11': ['my-old-alias'] }, models_by_group: { '11': [] }
    })
    const wrapper = mountView()
    await flushPromises()
    await clickButton(wrapper, 'common.edit')
    await clickButton(wrapper, 'admin.groups.platforms.anthropic')
    expect(previewPlatformDisplayModels).not.toHaveBeenCalled()
    await clickButton(wrapper, 'admin.channels.form.accountDisplaySync')
    expect(previewPlatformDisplayModels).toHaveBeenCalledOnce()
    expect(previewPlatformDisplayModels.mock.calls[0][0]).toBe('anthropic')
    expect(previewPlatformDisplayModels.mock.calls[0][1]).toMatchObject({
      channel_id: 1, group_ids: [11], model_mapping: channel.model_mapping
    })
    expect(wrapper.get('details').text()).toContain('Anthropic group')
    expect(wrapper.get('details').text()).toContain('gptDisplayRemoved my-old-alias')
    expect(updateChannel).not.toHaveBeenCalled()

    await wrapper.get('#channel-form').trigger('submit')
    await flushPromises()
    const payload = updateChannel.mock.calls[0][1]
    expect(payload.display_model_updates).toEqual({ anthropic: { '11': [] } })
    expect(payload.features_config).not.toHaveProperty('gpt_display_models_by_group')
    expect(payload.group_ids).toEqual(channel.group_ids)
    expect(payload.model_mapping).toEqual(channel.model_mapping)
    expect(payload.model_pricing).toEqual([])
    wrapper.unmount()
  })

  it('does not save a Claude preview after a display setting changes', async () => {
    stored = [makeChannel({ claude_display_models_by_group: { '11': ['claude-sonnet-4-6'] } })]
    previewPlatformDisplayModels.mockResolvedValue({
      previous_models_by_group: { '11': ['claude-sonnet-4-6'] }, models_by_group: { '11': [] }
    })
    const wrapper = mountView()
    await flushPromises()
    await clickButton(wrapper, 'common.edit')
    await clickButton(wrapper, 'admin.groups.platforms.anthropic')
    await clickButton(wrapper, 'admin.channels.form.accountDisplaySync')
    await clickButton(wrapper, 'admin.channels.form.basicSettings')
    await wrapper.get('textarea[placeholder="admin.channels.form.plazaHiddenModelsPlaceholder"]').setValue('claude-sonnet-4-6')
    await clickButton(wrapper, 'admin.groups.platforms.anthropic')
    expect(wrapper.get('[role="status"]').text()).toBe('admin.channels.form.gptDisplayStale')
    await wrapper.get('#channel-form').trigger('submit')
    await flushPromises()
    expect(updateChannel.mock.calls[0][1]).not.toHaveProperty('display_model_updates')
    expect(updateChannel.mock.calls[0][1].features_config).not.toHaveProperty('claude_display_models_by_group')
    wrapper.unmount()
  })

  it('aborts Claude discovery when the dialog closes and does not reuse a late response', async () => {
    stored = [makeChannel({})]
    let complete: (value: { models_by_group: Record<string, string[]> }) => void = () => {
      throw new Error('Preview request was not started')
    }
    previewPlatformDisplayModels.mockReturnValue(new Promise(resolve => { complete = resolve }))
    const wrapper = mountView()
    await flushPromises()
    await clickButton(wrapper, 'common.edit')
    await clickButton(wrapper, 'admin.groups.platforms.anthropic')
    await clickButton(wrapper, 'admin.channels.form.accountDisplaySync')
    const signal: AbortSignal = previewPlatformDisplayModels.mock.calls[0][2]
    expect(signal.aborted).toBe(false)
    expect(wrapper.get('button[form="channel-form"]').attributes('disabled')).toBeDefined()
    await clickButton(wrapper, 'common.cancel')
    expect(signal.aborted).toBe(true)
    complete({ models_by_group: { '11': [] } })
    await flushPromises()
    await clickButton(wrapper, 'common.edit')
    await wrapper.get('#channel-form').trigger('submit')
    await flushPromises()
    expect(updateChannel.mock.calls[0][1]).not.toHaveProperty('display_model_updates')
    expect(updateChannel.mock.calls[0][1].features_config).not.toHaveProperty('claude_display_models_by_group')
    wrapper.unmount()
  })

})
