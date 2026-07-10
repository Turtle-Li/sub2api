import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

import OpsNetworkBandwidthCard from '../OpsNetworkBandwidthCard.vue'

const {
  getNetworkBandwidthSummary,
  getNetworkBandwidthSettings,
  getNetworkInterfaces,
  updateNetworkBandwidthSettings,
  showSuccess,
  showError
} = vi.hoisted(() => ({
  getNetworkBandwidthSummary: vi.fn(),
  getNetworkBandwidthSettings: vi.fn(),
  getNetworkInterfaces: vi.fn(),
  updateNetworkBandwidthSettings: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin/ops', () => ({
  opsAPI: {
    getNetworkBandwidthSummary,
    getNetworkBandwidthSettings,
    getNetworkInterfaces,
    updateNetworkBandwidthSettings
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError })
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        if (!params) return key
        return `${key}:${JSON.stringify(params)}`
      }
    })
  }
})

const BaseDialogStub = defineComponent({
  props: {
    show: { type: Boolean, default: false },
    title: { type: String, default: '' }
  },
  template: '<div v-if="show" data-testid="settings-dialog"><slot /></div>'
})

const ToggleStub = defineComponent({
  inheritAttrs: false,
  props: {
    modelValue: { type: Boolean, default: false }
  },
  emits: ['update:modelValue'],
  setup(props, { attrs, emit }) {
    return () => h('input', {
      ...attrs,
      type: 'checkbox',
      checked: props.modelValue,
      onChange: (event: Event) => emit('update:modelValue', (event.target as HTMLInputElement).checked)
    })
  }
})

const IconStub = defineComponent({
  template: '<span aria-hidden="true" />'
})

const defaultSettings = {
  enabled: true,
  iface: 'eth0',
  rx_limit_mbps: null,
  tx_limit_mbps: 5,
  limit_source: 'manual' as const,
  abnormal_retry_protection_enabled: false,
  abnormal_retry_protection_trigger_percent: 90,
  abnormal_retry_protection_min_body_bytes: 5 * 1024 * 1024,
  abnormal_retry_protection_window_seconds: 60,
  abnormal_retry_protection_max_repeats: 1
}

function mountCard() {
  return mount(OpsNetworkBandwidthCard, {
    props: { refreshToken: 0 },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Toggle: ToggleStub,
        Icon: IconStub
      }
    }
  })
}

async function openSettingsDialog(wrapper: ReturnType<typeof mountCard>) {
  await wrapper.get('[data-testid="network-settings-button"]').trigger('click')
  await flushPromises()
}

describe('OpsNetworkBandwidthCard abnormal retry settings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getNetworkBandwidthSummary.mockResolvedValue({ enabled: true, summary: null })
    getNetworkBandwidthSettings.mockResolvedValue({ ...defaultSettings })
    getNetworkInterfaces.mockResolvedValue({
      interfaces: [{ name: 'eth0', state: 'up', is_default: true }],
      default_iface: 'eth0'
    })
    updateNetworkBandwidthSettings.mockResolvedValue({ ...defaultSettings })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('refreshes the latest bandwidth sample independently every five seconds', async () => {
    vi.useFakeTimers()
    const wrapper = mountCard()
    await flushPromises()

    expect(getNetworkBandwidthSummary).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(5_000)
    await flushPromises()

    expect(getNetworkBandwidthSummary).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('hides retry details while disabled and keeps advanced settings collapsed when enabled', async () => {
    const wrapper = mountCard()
    await flushPromises()
    await openSettingsDialog(wrapper)

    expect(wrapper.find('[data-testid="retry-advanced-toggle"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="retry-min-body-input"]').exists()).toBe(false)

    await wrapper.get<HTMLInputElement>('[data-testid="retry-protection-toggle"]').setValue(true)

    const advancedToggle = wrapper.get('[data-testid="retry-advanced-toggle"]')
    expect(advancedToggle.attributes('aria-expanded')).toBe('false')
    expect(wrapper.find('[data-testid="retry-min-body-input"]').exists()).toBe(false)

    await advancedToggle.trigger('click')

    const minBodyInput = wrapper.get<HTMLInputElement>('[data-testid="retry-min-body-input"]')
    expect(minBodyInput.attributes('min')).toBe('0.001')
    expect(minBodyInput.attributes('step')).toBe('0.001')
    expect(minBodyInput.element.value).toBe('5')
    expect(minBodyInput.element.validity.valid).toBe(true)

    await wrapper.get<HTMLInputElement>('[data-testid="retry-protection-toggle"]').setValue(false)
    expect(wrapper.find('[data-testid="retry-advanced-toggle"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="retry-min-body-input"]').exists()).toBe(false)
  })

  it('allows saving while protection is disabled even if a hidden legacy value is invalid', async () => {
    getNetworkBandwidthSettings.mockResolvedValue({
      ...defaultSettings,
      abnormal_retry_protection_min_body_bytes: 1
    })

    const wrapper = mountCard()
    await flushPromises()
    await openSettingsDialog(wrapper)
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(updateNetworkBandwidthSettings).toHaveBeenCalledWith(expect.objectContaining({
      abnormal_retry_protection_enabled: false,
      abnormal_retry_protection_min_body_bytes: 5 * 1024 * 1024
    }))
    expect(showSuccess).toHaveBeenCalled()
  })
})
