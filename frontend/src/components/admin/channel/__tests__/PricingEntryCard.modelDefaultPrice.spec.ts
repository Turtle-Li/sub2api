import { flushPromises, shallowMount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import PricingEntryCard from '../PricingEntryCard.vue'
import type { PricingFormEntry } from '../types'
import channelsAPI from '@/api/admin/channels'

vi.mock('@/api/admin/channels', () => ({
  default: { getModelDefaultPricing: vi.fn() },
}))

vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

const getModelDefaultPricing = vi.mocked(channelsAPI.getModelDefaultPricing)

function createEntry(models: string[] = []): PricingFormEntry {
  return {
    models,
    billing_mode: 'token',
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_read_price: null,
    fast_multiplier: null,
    flex_multiplier: null,
    reasoning_effort_multipliers: null,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    time_pricing: {
      timezone: 'Asia/Shanghai',
      weekdays_only: false,
      periods: [],
    },
  }
}

function addModels(entry: PricingFormEntry, models: string[]) {
  const wrapper = shallowMount(PricingEntryCard, {
    props: { entry, onUpdate: next => { void wrapper.setProps({ entry: next }) } },
  })
  wrapper.findComponent({ name: 'ModelTagInput' }).vm.$emit('update:models', models)
  return wrapper
}

const emittedEntries = (wrapper: ReturnType<typeof shallowMount>) =>
  (wrapper.emitted('update') ?? []).map(e => e[0] as PricingFormEntry)

describe('PricingEntryCard default price auto-fill', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getModelDefaultPricing.mockResolvedValue({ found: true, input_price: 3e-6, output_price: 15e-6 })
  })

  it('auto-fills the default price when a model is added to an empty entry', async () => {
    const wrapper = addModels(createEntry(), ['claude-opus-4-8'])
    await flushPromises()

    expect(getModelDefaultPricing).toHaveBeenCalledTimes(1)
    expect(getModelDefaultPricing).toHaveBeenCalledWith('claude-opus-4-8', 'USD')
    expect(emittedEntries(wrapper).at(-1)).toMatchObject({
      models: ['claude-opus-4-8'],
      input_price: 3,
      output_price: 15,
    })
  })

  it('does not overwrite prices when appending a model to an entry that already has one', async () => {
    const entry = createEntry(['claude-opus-4-7'])
    const wrapper = addModels(entry, ['claude-opus-4-7', 'claude-opus-4-8'])
    await flushPromises()

    expect(getModelDefaultPricing).not.toHaveBeenCalled()
    expect(emittedEntries(wrapper).at(-1)).toMatchObject({
      models: ['claude-opus-4-7', 'claude-opus-4-8'],
    })
    for (const emitted of emittedEntries(wrapper)) {
      expect(emitted.input_price).toBeNull()
      expect(emitted.output_price).toBeNull()
      expect(emitted.cache_write_price).toBeNull()
      expect(emitted.cache_read_price).toBeNull()
    }
  })

  it('does not auto-fill a shared price when several models are added to an empty entry at once', async () => {
    const wrapper = addModels(createEntry(), ['claude-opus-4-7', 'claude-opus-4-8'])
    await flushPromises()

    expect(getModelDefaultPricing).not.toHaveBeenCalled()
    expect(emittedEntries(wrapper).at(-1)).toMatchObject({
      models: ['claude-opus-4-7', 'claude-opus-4-8'],
      input_price: null,
      output_price: null,
    })
  })

  it('keeps manual prices untouched when a model is appended', async () => {
    const entry = { ...createEntry(['claude-opus-4-7']), input_price: '1.5' }
    const wrapper = addModels(entry, ['claude-opus-4-7', 'claude-opus-4-8'])
    await flushPromises()

    expect(getModelDefaultPricing).not.toHaveBeenCalled()
    for (const emitted of emittedEntries(wrapper)) {
      expect(emitted.input_price).toBe('1.5')
    }
  })

  it('fills native CNY prices including cache TTLs into a new CNY card', async () => {
    getModelDefaultPricing.mockResolvedValue({ found: true, currency: 'CNY', input_price: 13e-6,
      output_price: 68e-6, cache_read_price: 1.3e-6, cache_write_price: 13e-6, cache_write_1h_price: 26e-6 })
    const wrapper = addModels({ ...createEntry(), currency: 'CNY' }, ['kimi-k3.1-pro'])
    await flushPromises()
    expect(getModelDefaultPricing).toHaveBeenCalledWith('kimi-k3.1-pro', 'CNY')
    expect(emittedEntries(wrapper).at(-1)).toMatchObject({
      currency: 'CNY', input_price: 13, output_price: 68,
      cache_read_price: 1.3, cache_write_price: 13, cache_write_1h_price: 26,
      intervals: [], time_pricing: { periods: [] },
    })
  })

  it('preserves a manual edit made while the lookup is pending', async () => {
    let resolve: ((value: Awaited<ReturnType<typeof channelsAPI.getModelDefaultPricing>>) => void) | undefined
    getModelDefaultPricing.mockImplementation(() => new Promise(done => { resolve = done }))
    const wrapper = addModels(createEntry(), ['kimi-k3.1-pro'])
    await wrapper.setProps({ entry: { ...createEntry(['kimi-k3.1-pro']), output_price: 99 } })
    resolve?.({ found: true, input_price: 2e-6, output_price: 10e-6 })
    await flushPromises()
    expect(emittedEntries(wrapper)).toHaveLength(1)
    expect(wrapper.props('entry').output_price).toBe(99)
  })

  it('does not restore a removed model when a late lookup completes', async () => {
    let resolve: ((value: Awaited<ReturnType<typeof channelsAPI.getModelDefaultPricing>>) => void) | undefined
    getModelDefaultPricing.mockImplementation(() => new Promise(done => { resolve = done }))
    const wrapper = addModels(createEntry(), ['kimi-k3.1-pro'])
    await wrapper.setProps({ entry: createEntry() })
    resolve?.({ found: true, input_price: 2e-6, output_price: 10e-6 })
    await flushPromises()
    expect(wrapper.props('entry').models).toEqual([])
    expect(emittedEntries(wrapper)).toHaveLength(1)
  })
})
