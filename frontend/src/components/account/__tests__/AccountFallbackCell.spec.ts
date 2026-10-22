import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AccountFallbackCell from '../AccountFallbackCell.vue'
import type { Account } from '@/types'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const account = (overrides: Partial<Account> = {}) => ({
  id: 7, name: 'Reserve candidate', platform: 'deepseek', type: 'apikey', priority: 3,
  extra: { custom_setting: 'preserved' }, ...overrides,
}) as Account

describe('AccountFallbackCell', () => {
  it.each([true, null, 'true'])('shows a read-only label for reserve flag %s', paid => {
    const wrapper = mount(AccountFallbackCell, { props: { account: account({ extra: { paid_fallback: paid } }) } })
    expect(wrapper.text()).toBe('admin.paidFallback.priorityLabel')
    expect(wrapper.find('select, button, input').exists()).toBe(false)
  })

  it.each([{}, { paid_fallback: false }])('does not label ordinary accounts', extra => {
    const wrapper = mount(AccountFallbackCell, { props: { account: account({ extra }) } })
    expect(wrapper.find('[data-testid="account-fallback-label"]').exists()).toBe(false)
  })
})
