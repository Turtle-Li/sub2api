import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AttachmentGatewayR2Settings from '../AttachmentGatewayR2Settings.vue'

const { getR2Config, updateR2Config, testR2Connection, showError, showSuccess } = vi.hoisted(() => ({
  getR2Config: vi.fn(),
  updateR2Config: vi.fn(),
  testR2Connection: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api', () => ({
  adminAPI: {
    attachmentGateway: {
      getR2Config,
      updateR2Config,
      testR2Connection,
    },
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ showError, showSuccess }),
}))

vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: (operation: () => Promise<unknown>) => operation() }),
  isStepUpBlocked: () => false,
  isStepUpCancelled: () => false,
  stepUpBlockReason: () => '',
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const configuredResponse = {
  enabled: true,
  endpoint: 'https://account.r2.cloudflarestorage.com',
  region: 'auto',
  bucket: 'private-attachments',
  access_key_id: 'access-id',
  prefix: 'sub2api/',
  force_path_style: false,
  presign_expiry_minutes: 60,
  secret_configured: true,
  configured: true,
}

function mountSettings() {
  return mount(AttachmentGatewayR2Settings, {
    global: {
      stubs: {
        Toggle: {
          props: ['modelValue'],
          template: '<input type="checkbox" :checked="modelValue" />',
        },
        TotpStepUpDialog: true,
      },
    },
  })
}

describe('AttachmentGatewayR2Settings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getR2Config.mockResolvedValue({ ...configuredResponse })
    updateR2Config.mockImplementation(async (payload) => ({
      ...payload,
      secret_access_key: undefined,
      secret_configured: true,
      configured: true,
    }))
    testR2Connection.mockResolvedValue({ ok: true, message: 'probe ok' })
  })

  it('loads only a redacted secret state and displays enabled status', async () => {
    const wrapper = mountSettings()
    await flushPromises()

    expect(getR2Config).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="attachment-r2-status"]').text()).toContain('statusEnabled')
    const secret = wrapper.get('input[type="password"]')
    expect((secret.element as HTMLInputElement).value).toBe('')
    expect(secret.attributes('placeholder')).toContain('secretConfigured')
    expect(wrapper.text()).not.toContain('secret-value')
  })

  it('saves with an empty secret so the backend preserves the encrypted value', async () => {
    const wrapper = mountSettings()
    await flushPromises()

    const save = wrapper.findAll('button').find((button) => button.text().includes('common.save'))
    expect(save).toBeDefined()
    await save!.trigger('click')
    await flushPromises()

    expect(updateR2Config).toHaveBeenCalledTimes(1)
    expect(updateR2Config.mock.calls[0][0]).toMatchObject({
      enabled: true,
      bucket: 'private-attachments',
      secret_access_key: '',
    })
    expect(showSuccess).toHaveBeenCalledWith('admin.settings.attachmentGatewayR2.saved')
  })

  it('runs the read/write/delete probe without saving config', async () => {
    const wrapper = mountSettings()
    await flushPromises()

    const testButton = wrapper.findAll('button').find((button) => button.text().includes('testConnection'))
    expect(testButton).toBeDefined()
    await testButton!.trigger('click')
    await flushPromises()

    expect(testR2Connection).toHaveBeenCalledTimes(1)
    expect(updateR2Config).not.toHaveBeenCalled()
    expect(showSuccess).toHaveBeenCalledWith('probe ok')
  })
})
