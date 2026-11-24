import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import OpsAlertAccountPicker from '../OpsAlertAccountPicker.vue'

const { list } = vi.hoisted(() => ({ list: vi.fn() }))
vi.mock('@/api', () => ({ adminAPI: { accounts: { list } } }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))

afterEach(() => {
  vi.useRealTimers()
  list.mockReset()
})

const page = (id: number, name: string, numberOfPages = 2) => ({ items: [{ id, name }], pages: numberOfPages, total: numberOfPages * 20 })

function mountPicker() {
  return mount(defineComponent({
    components: { OpsAlertAccountPicker },
    setup: () => ({ selected: ref([199]) }),
    template: '<OpsAlertAccountPicker v-model="selected" platform="openai" :group-id="12" />'
  }))
}

describe('alert account multi-select', () => {
  it('keeps selected IDs and their labels when the next page replaces the options', async () => {
    list.mockResolvedValueOnce(page(248, 'First')).mockResolvedValueOnce(page(258, 'Second'))
    const wrapper = mountPicker()
    await flushPromises()
    expect(list).toHaveBeenCalledWith(1, 20, { lite: 'true', platform: 'openai', group: '12' }, { signal: expect.any(AbortSignal) })
    await wrapper.get('input[value="248"]').setValue(true)
    await wrapper.findAll('button').find((button) => button.text() === 'admin.ops.alertRules.accounts.next')!.trigger('click')
    await flushPromises()
    await wrapper.get('input[value="258"]').setValue(true)
    expect(wrapper.get('[data-selected-account="199"]').text()).toContain('#199')
    expect(wrapper.get('[data-selected-account="248"]').text()).toContain('First (#248)')
    expect(wrapper.get('[data-selected-account="258"]').text()).toContain('Second (#258)')
    await wrapper.get('[data-selected-account="248"]').trigger('click')
    expect(wrapper.find('[data-selected-account="248"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('debounces searches, aborts outdated responses and cancels on unmount', async () => {
    vi.useFakeTimers()
    let oldResolve!: (result: ReturnType<typeof page>) => void
    list.mockReturnValueOnce(new Promise((resolve) => { oldResolve = resolve }))
      .mockResolvedValueOnce(page(258, 'Searched', 1)).mockReturnValueOnce(new Promise(() => {}))
    const wrapper = mountPicker()
    const oldSignal = list.mock.calls[0][3].signal as AbortSignal
    await wrapper.get('input[type="search"]').setValue('old')
    expect(oldSignal.aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(180)
    await wrapper.get('input[type="search"]').setValue('new search')
    await vi.advanceTimersByTimeAsync(249)
    expect(list).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    expect(list.mock.calls[1][2]).toMatchObject({ lite: 'true', search: 'new search' })
    oldResolve(page(248, 'Stale'))
    await flushPromises()
    expect(wrapper.text()).not.toContain('Stale')
    expect(wrapper.text()).toContain('Searched')
    await wrapper.get('input[type="search"]').setValue('unmount')
    await vi.advanceTimersByTimeAsync(250)
    const newestSignal = list.mock.calls[2][3].signal as AbortSignal
    wrapper.unmount()
    expect(newestSignal.aborted).toBe(true)
  })
})
