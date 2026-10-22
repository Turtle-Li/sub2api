import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountFallbackCell from '../AccountFallbackCell.vue'
import { setPaidFallback } from '@/api/admin/accounts'
import type { Account } from '@/types'

vi.mock('@/api/admin/accounts', () => ({ setPaidFallback: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const account = (overrides: Partial<Account> = {}) => ({
  id: 7, name: 'Reserve candidate', platform: 'deepseek', type: 'apikey', priority: 3,
  extra: { custom_setting: 'preserved' }, ...overrides,
}) as Account

beforeEach(() => {
  vi.mocked(setPaidFallback).mockReset().mockImplementation(async (id, enabled) => ({ account_id: id, enabled }))
})

describe('AccountFallbackCell', () => {
  it.each([false, true])('changes only the fallback flag from %s', async paid => {
    const wrapper = mount(AccountFallbackCell, { props: { account: account({ extra: { paid_fallback: paid, custom_setting: 'preserved' } }) } })
    await wrapper.get('select').setValue(paid ? 'ordinary' : 'paid')
    await flushPromises()
    expect(setPaidFallback).toHaveBeenCalledOnce()
    expect(setPaidFallback).toHaveBeenCalledWith(7, !paid)
    expect(wrapper.emitted('updated')?.[0]?.[0]).toMatchObject({ id: 7, extra: { paid_fallback: !paid, custom_setting: 'preserved' } })
  })

  it('disables changes while saving and restores the old value on failure', async () => {
    let rejectSave: (reason: Error) => void = () => { throw new Error('save not started') }
    vi.mocked(setPaidFallback).mockReturnValueOnce(new Promise<{ account_id: number; enabled: boolean }>((_, reject) => { rejectSave = reject }))
    const wrapper = mount(AccountFallbackCell, { props: { account: account() } })
    await wrapper.get('select').setValue('paid')
    expect(wrapper.get('select').attributes('disabled')).toBeDefined()
    rejectSave(new Error('offline'))
    await flushPromises()
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('ordinary')
    expect(wrapper.emitted('error')).toHaveLength(1)
    expect(wrapper.emitted('updated')).toBeUndefined()
  })

  it('keeps malformed historical flags isolated until explicitly cleared', async () => {
    const wrapper = mount(AccountFallbackCell, { props: { account: account({ platform: 'gemini', extra: { paid_fallback: null } }) } })
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('paid')
    expect(wrapper.get('option[value="paid"]').attributes('disabled')).toBeDefined()
    expect(setPaidFallback).not.toHaveBeenCalled()
    await wrapper.get('select').setValue('ordinary')
    await flushPromises()
    expect(setPaidFallback).toHaveBeenCalledWith(7, false)
  })

  it('does not offer reserve admission for an unsupported platform', () => {
    const wrapper = mount(AccountFallbackCell, { props: { account: account({ platform: 'gemini' }) } })
    expect(wrapper.find('select').exists()).toBe(false)
  })
})
