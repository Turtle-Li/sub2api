import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import DesktopAuthorizationView from '@/views/auth/DesktopAuthorizationView.vue'

const { approveDesktopAuthorizationMock, routeState } = vi.hoisted(() => ({
  approveDesktopAuthorizationMock: vi.fn(),
  routeState: {
    query: {} as Record<string, unknown>
  }
}))

vi.mock('vue-router', () => ({
  useRoute: () => routeState
}))

vi.mock('@/api/auth', () => ({
  approveDesktopAuthorization: (...args: unknown[]) => approveDesktopAuthorizationMock(...args)
}))

function mountAuthorizationView() {
  return mount(DesktopAuthorizationView, {
    global: {
      stubs: {
        AuthLayout: { template: '<div><slot /></div>' },
        Icon: true
      }
    }
  })
}

describe('DesktopAuthorizationView', () => {
  beforeEach(() => {
    approveDesktopAuthorizationMock.mockReset()
    routeState.query = { user_code: 'ABCD-EFGH' }
  })

  it('requires a valid short code before exposing approval controls', () => {
    routeState.query = { user_code: 'not-a-device-code' }

    const wrapper = mountAuthorizationView()

    expect(wrapper.text()).toContain('授权码无效或已丢失')
    expect(wrapper.find('button').exists()).toBe(false)
    expect(approveDesktopAuthorizationMock).not.toHaveBeenCalled()
  })

  it('explicitly approves only the displayed normalized code', async () => {
    routeState.query = { user_code: 'abcd-efgh' }
    approveDesktopAuthorizationMock.mockResolvedValue({ status: 'approved' })
    const wrapper = mountAuthorizationView()

    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(approveDesktopAuthorizationMock).toHaveBeenCalledWith('ABCD-EFGH')
    expect(wrapper.text()).toContain('已授权 TT Switch')
    expect(wrapper.text()).toContain('可以回到 TT Switch')
  })

  it('shows expiry without treating it as an approved desktop session', async () => {
    approveDesktopAuthorizationMock.mockResolvedValue({ status: 'expired' })
    const wrapper = mountAuthorizationView()

    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('本次授权已过期')
    expect(wrapper.text()).not.toContain('已授权 TT Switch')
  })
})
