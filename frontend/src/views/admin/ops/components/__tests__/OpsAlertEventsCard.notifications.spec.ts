import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import { ref } from 'vue'
import OpsAlertEventsCard from '../OpsAlertEventsCard.vue'
import type { AlertEvent } from '@/api/admin/ops'

const { listAlertEvents, screen } = vi.hoisted(() => ({ listAlertEvents: vi.fn(), screen: { desktop: true } }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))
vi.mock('@vueuse/core', () => ({ useMediaQuery: () => ref(screen.desktop) }))
vi.mock('@/api/admin/ops', () => ({ opsAPI: { listAlertEvents } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))

enableAutoUnmount(afterEach)
const base: AlertEvent = {
  id: 1, rule_id: 9, severity: 'P1', status: 'firing', title: 'Upstream balance',
  email_sent: false, feishu_sent: false, created_at: '2026-11-24T00:00:00Z', fired_at: '2026-11-24T00:00:00Z'
}

describe('ops event delivery presentation', () => {
  it.each([true, false])('shows all delivery states and account fallback (desktop=%s)', async (desktop) => {
    screen.desktop = desktop
    listAlertEvents.mockResolvedValue([
      { ...base, dimensions: { latest_account_id: 248, latest_account_name: 'Sample', latest_upstream_status_code: 402 },
        email_delivery_status: 'disabled', feishu_delivery_status: 'pending' },
      { ...base, id: 2, dimensions: { latest_account_id: 258, latest_upstream_status_code: 400 },
        email_delivery_status: 'sent', feishu_delivery_status: 'failed', email_sent: true },
      { ...base, id: 3, feishu_sent: true },
    ] satisfies AlertEvent[])
    const card = shallowMount(OpsAlertEventsCard, { global: { stubs: { OpsAlertDeliveryBadges: false } } })
    await flushPromises()
    expect(card.text()).toContain('Sample (#248) · HTTP 402')
    expect(card.text()).toContain('#258 · HTTP 400')
    const badges = card.findAll('[data-notification-channel]')
    expect(badges.map((badge) => [badge.attributes('data-notification-channel'), badge.attributes('data-notification-state')])).toEqual([
      ['email', 'disabled'], ['feishu', 'pending'], ['email', 'sent'], ['feishu', 'failed'], ['email', 'unknown'], ['feishu', 'sent']
    ])
  })
})
