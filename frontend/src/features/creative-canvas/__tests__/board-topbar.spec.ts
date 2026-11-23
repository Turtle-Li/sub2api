import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import BoardTopbar from '../BoardTopbar.vue'
import type { CreativeCanvas } from '../types'

const serverIso = '2026-11-22T06:32:00Z'
const expectedClock = new Intl.DateTimeFormat('zh-CN', {
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
}).format(new Date(serverIso))

const sampleBoard: CreativeCanvas = {
  id: 'b0000000-0000-4000-8000-000000000001',
  title: '山海灯塔 · 三层续镜画布',
  revision: 4,
  updated_at: serverIso,
  document: { viewport: { x: 0, y: 0, zoom: 1 }, nodes: [], edges: [] },
  assets: {},
  attempts: [],
}

function mountTopbar(overrides: Record<string, unknown> = {}) {
  return mount(BoardTopbar, {
    attachTo: document.body,
    props: {
      boards: [{ id: sampleBoard.id, title: sampleBoard.title, revision: sampleBoard.revision, updated_at: serverIso }],
      board: sampleBoard,
      title: sampleBoard.title,
      dirty: false,
      busy: false,
      saving: false,
      conflict: false,
      saveErrorKind: null,
      lastSavedAt: serverIso,
      hasSavedInSession: true,
      canUndo: false,
      canRedo: false,
      canCreateDraft: false,
      canCompare: false,
      ...overrides,
    },
  })
}

describe('BoardTopbar save status visibility and touch affordances (U5 P2)', () => {
  it('exposes the server HH:mm clock directly in the compact core badge, in the tap popover, and inside the More menu when saved', async () => {
    const wrapper = mountTopbar()
    const badge = wrapper.get('button.cc-status-badge')

    expect(badge.text()).toBe(`已自动保存 · ${expectedClock}`)
    expect(badge.get('.cc-status-core').text()).toBe(expectedClock)
    expect(badge.attributes('title')).toContain(`服务端时间 ${expectedClock}`)
    expect(badge.attributes('aria-expanded')).toBe('false')

    // Tap/click badge opens detailed touch-accessible popover
    await badge.trigger('click')
    expect(badge.attributes('aria-expanded')).toBe('true')
    const popover = wrapper.get('[role="dialog"][aria-label="画布保存状态详情"]')
    expect(popover.text()).toContain('已自动保存到服务端')
    expect(popover.text()).toContain('服务端确认时间')
    expect(popover.text()).toContain(expectedClock)
    expect(popover.text()).toContain('v4')
    expect(popover.text()).toContain('不写入本地浏览器存储')

    // Opening More menu closes popover and displays server confirmed time in menu
    await wrapper.get('button[aria-label="更多画布操作"]').trigger('click')
    expect(wrapper.find('[role="dialog"][aria-label="画布保存状态详情"]').exists()).toBe(false)
    const menu = wrapper.get('[role="menu"][aria-label="更多画布操作菜单"]')
    expect(menu.text()).toContain('已自动保存到服务端')
    expect(menu.text()).toContain(`服务端确认时间 ${expectedClock}`)
    expect(menu.text()).toContain('v4')

    wrapper.unmount()
  })

  it.each([
    { label: 'dirty', props: { dirty: true }, core: '待保存', full: '有改动待保存', action: '立即保存到服务端' },
    { label: 'saving', props: { dirty: true, saving: true }, core: '保存…', full: '正在保存…', action: null },
    { label: 'offline', props: { dirty: true, saveErrorKind: 'offline' }, core: '离线', full: '离线 · 尚未同步', action: '重试保存' },
    { label: 'error', props: { dirty: true, saveErrorKind: 'error' }, core: '失败', full: '保存失败 · 尚未同步', action: '重试保存' },
    { label: 'conflict', props: { dirty: true, conflict: true, saveErrorKind: 'conflict' }, core: '另一处更新', full: '此画布在另一处更新 · 本次修改已保留', action: '重新加载最新画布' },
  ])('never disguises $label state as currently saved while still clearly distinguishing prior server confirmation', async ({ props, core, full, action }) => {
    const wrapper = mountTopbar(props)
    const badge = wrapper.get('button.cc-status-badge')

    expect(badge.text()).toBe(full)
    expect(badge.get('.cc-status-core').text()).toBe(core)
    expect(badge.get('.cc-status-core').text()).not.toContain(expectedClock)

    await badge.trigger('click')
    const popover = wrapper.get('[role="dialog"][aria-label="画布保存状态详情"]')
    expect(popover.text()).toContain('上次服务端确认')
    expect(popover.text()).not.toContain('已自动保存到服务端')
    if (action) {
      expect(popover.text()).toContain(action)
    }

    await wrapper.get('button[aria-label="更多画布操作"]').trigger('click')
    const menu = wrapper.get('[role="menu"][aria-label="更多画布操作菜单"]')
    expect(menu.text()).toContain(`本次尚未同步 · 上次服务端确认 ${expectedClock}`)

    wrapper.unmount()
  })

  it('moves keyboard focus into the non-modal save-details dialog on open and restores focus to the status badge on Escape, Close, and closeMenus()', async () => {
    const wrapper = mountTopbar()
    const badge = wrapper.get('button.cc-status-badge')
    const badgeEl = badge.element as HTMLButtonElement

    // Focus badge and open via click (same activation as Enter on native <button>)
    badgeEl.focus()
    expect(document.activeElement).toBe(badgeEl)
    await badge.trigger('click')

    const popover = wrapper.get('[role="dialog"][aria-label="画布保存状态详情"]')
    expect(popover.attributes('aria-modal')).toBe('false')
    expect(badge.attributes('aria-controls')).toBe(popover.attributes('id'))
    expect(popover.attributes('aria-describedby')).toBeTruthy()
    expect(popover.element.contains(document.activeElement)).toBe(true)
    expect((document.activeElement as HTMLElement | null)?.getAttribute('aria-label')).toBe('关闭保存状态详情')

    // Escape from inside the dialog closes it and restores focus to the badge
    await popover.trigger('keydown', { key: 'Escape' })
    expect(wrapper.find('[role="dialog"][aria-label="画布保存状态详情"]').exists()).toBe(false)
    expect(document.activeElement).toBe(badgeEl)

    // Re-open and close via the close button restores focus to the badge
    await badge.trigger('click')
    const reopened = wrapper.get('[role="dialog"][aria-label="画布保存状态详情"]')
    expect(reopened.element.contains(document.activeElement)).toBe(true)
    await reopened.get('button[aria-label="关闭保存状态详情"]').trigger('click')
    expect(wrapper.find('[role="dialog"][aria-label="画布保存状态详情"]').exists()).toBe(false)
    expect(document.activeElement).toBe(badgeEl)

    // Re-open and close via exposed closeMenus() (global Escape path) restores focus to the badge
    await badge.trigger('click')
    expect(wrapper.get('[role="dialog"][aria-label="画布保存状态详情"]').element.contains(document.activeElement)).toBe(true)
    expect(wrapper.vm.closeMenus()).toBe(true)
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[role="dialog"][aria-label="画布保存状态详情"]').exists()).toBe(false)
    expect(document.activeElement).toBe(badgeEl)

    wrapper.unmount()
  })

  it('keeps unconfirmed writes visibly unresolved even when dirty is false and exposes enabled Retry and explicit Reload actions', async () => {
    const wrapper = mountTopbar({
      dirty: false,
      saveUnconfirmed: true,
      saveErrorKind: null,
    })
    const badge = wrapper.get('button.cc-status-badge')
    expect(badge.text()).toBe('保存未确认 · 尚未同步')
    expect(badge.get('.cc-status-core').text()).toBe('未确认')
    expect(badge.text()).not.toContain('已保存')
    expect(badge.text()).not.toContain(expectedClock)

    // Topbar retry button & desktop save button remain enabled despite dirty === false
    const topbarRetry = wrapper.get('button.cc-retry-save-btn')
    expect(topbarRetry.attributes('disabled')).toBeUndefined()
    await topbarRetry.trigger('click')
    expect(wrapper.emitted('save')).toHaveLength(1)

    const saveBtn = wrapper.findAll('button').find(btn => btn.text() === '保存')
    expect(saveBtn?.attributes('disabled')).toBeUndefined()

    // Popover exposes both enabled Retry and explicit Reload actions and returns focus to badge
    await badge.trigger('click')
    const popover = wrapper.get('[role="dialog"][aria-label="画布保存状态详情"]')
    expect(popover.text()).toContain('保存未确认 · 尚未同步')
    const retryBtn = popover.findAll('button').find(btn => btn.text() === '重试保存')
    const reloadBtn = popover.findAll('button').find(btn => btn.text() === '重新加载最新画布')
    expect(retryBtn?.attributes('disabled')).toBeUndefined()
    expect(reloadBtn?.attributes('disabled')).toBeUndefined()

    await retryBtn!.trigger('click')
    expect(wrapper.emitted('save')).toHaveLength(2)
    expect(document.activeElement).toBe(badge.element)

    await badge.trigger('click')
    const reloadBtnAgain = wrapper
      .get('[role="dialog"][aria-label="画布保存状态详情"]')
      .findAll('button')
      .find(btn => btn.text() === '重新加载最新画布')
    await reloadBtnAgain!.trigger('click')
    expect(wrapper.emitted('reloadBoard')).toHaveLength(1)
    expect(document.activeElement).toBe(badge.element)

    // Offline + unconfirmed + dirty === false also keeps retry enabled and never bleaches to saved
    await wrapper.setProps({ saveErrorKind: 'offline' })
    expect(badge.text()).toBe('离线 · 尚未同步')
    await badge.trigger('click')
    const offlinePopover = wrapper.get('[role="dialog"][aria-label="画布保存状态详情"]')
    expect(offlinePopover.text()).toContain('尚未确认')
    const offlineRetry = offlinePopover.findAll('button').find(btn => btn.text() === '重试保存')
    expect(offlineRetry?.attributes('disabled')).toBeUndefined()

    wrapper.unmount()
  })

  it('uses LiquidSelect for board switching, preserves selection/keyboard semantics, and never closes or rebuilds on background save updates', async () => {
    const secondBoard = {
      id: 'b0000000-0000-4000-8000-000000000002',
      title: '第二分镜画布',
      revision: 2,
      updated_at: serverIso,
    }
    const wrapper = mountTopbar({
      boards: [
        { id: sampleBoard.id, title: sampleBoard.title, revision: sampleBoard.revision, updated_at: serverIso },
        secondBoard,
      ],
    })

    const trigger = wrapper.get('button.lg-select-trigger[aria-label="切换画布"]')
    const triggerEl = trigger.element as HTMLButtonElement
    expect(trigger.text()).toContain(sampleBoard.title)
    expect(trigger.attributes('aria-haspopup')).toBe('listbox')
    expect(trigger.attributes('aria-expanded')).toBe('false')

    // Open via click and verify listbox renders in portal with current selection preserved
    triggerEl.focus()
    await trigger.trigger('click')
    expect(trigger.attributes('aria-expanded')).toBe('true')
    const menuEl = document.body.querySelector<HTMLElement>('.lg-select-menu[role="listbox"][aria-label="切换画布"]')
    expect(menuEl).not.toBeNull()
    expect(menuEl?.querySelectorAll('[role="option"]')).toHaveLength(3)
    expect(menuEl?.querySelector('[role="option"][aria-selected="true"]')?.textContent).toContain(sampleBoard.title)

    // Background autosave lifecycle (dirty -> saving -> saved with bumped revision & updated_at)
    // must NOT disable, close, rebuild the menu DOM node, or steal focus
    await wrapper.setProps({ dirty: true })
    await wrapper.setProps({ dirty: true, saving: true })
    expect(trigger.attributes('disabled')).toBeUndefined()
    expect(trigger.attributes('aria-expanded')).toBe('true')
    expect(document.body.querySelector('.lg-select-menu[role="listbox"][aria-label="切换画布"]')).toBe(menuEl)
    expect(document.activeElement).toBe(triggerEl)

    await wrapper.setProps({
      dirty: false,
      saving: false,
      lastSavedAt: '2026-11-22T06:35:00Z',
      board: { ...sampleBoard, revision: 5, updated_at: '2026-11-22T06:35:00Z' },
      boards: [
        { id: sampleBoard.id, title: sampleBoard.title, revision: 5, updated_at: '2026-11-22T06:35:00Z' },
        secondBoard,
      ],
    })
    expect(trigger.attributes('aria-expanded')).toBe('true')
    expect(document.body.querySelector('.lg-select-menu[role="listbox"][aria-label="切换画布"]')).toBe(menuEl)
    expect(document.activeElement).toBe(triggerEl)

    // Keyboard navigation (ArrowDown -> Enter) selects the second board and restores focus
    await trigger.trigger('keydown', { key: 'ArrowDown' })
    await trigger.trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('selectBoard')).toEqual([[secondBoard.id]])
    expect(trigger.attributes('aria-expanded')).toBe('false')
    expect(document.activeElement).toBe(triggerEl)

    // Reopen and Escape via exposed closeMenus() closes and restores focus
    await trigger.trigger('click')
    expect(trigger.attributes('aria-expanded')).toBe('true')
    expect(wrapper.vm.closeMenus()).toBe(true)
    await wrapper.vm.$nextTick()
    expect(trigger.attributes('aria-expanded')).toBe('false')
    expect(document.activeElement).toBe(triggerEl)

    wrapper.unmount()
  })

  it('keeps routine save states visually quiet and layout-stable without primary flashing or spinner, while surfacing explicit alerts for errors and conflicts', async () => {
    const wrapper = mountTopbar({ dirty: false, saving: false })
    const badge = wrapper.get('button.cc-status-badge')
    const saveBtn = wrapper.get('button.cc-save-btn')

    // Saved -> dirty -> saving: stays quiet, never promotes desktop Save to is-primary, never swaps label width, no spinning icon
    for (const next of [
      { dirty: false, saving: false, kind: 'saved' },
      { dirty: true, saving: false, kind: 'dirty' },
      { dirty: true, saving: true, kind: 'saving' },
    ]) {
      await wrapper.setProps({ dirty: next.dirty, saving: next.saving })
      expect(badge.attributes('data-status-kind')).toBe(next.kind)
      expect(badge.classes()).toContain('is-quiet')
      expect(badge.classes()).not.toContain('is-conflict')
      expect(wrapper.find('.cc-status-icon.is-spinning').exists()).toBe(false)
      expect(saveBtn.text()).toBe('保存')
      expect(saveBtn.classes()).not.toContain('is-primary')
      expect(saveBtn.classes()).not.toContain('is-alert')
    }
    expect(saveBtn.attributes('aria-busy')).toBe('true')

    // Offline / error / unconfirmed / conflict exit quiet mode and expose alert styling + retry affordances
    await wrapper.setProps({ dirty: true, saving: false, saveErrorKind: 'offline' })
    expect(badge.classes()).not.toContain('is-quiet')
    expect(badge.classes()).toContain('is-conflict')
    expect(saveBtn.classes()).toContain('is-alert')
    expect(wrapper.find('button.cc-retry-save-btn').exists()).toBe(true)

    await wrapper.setProps({ dirty: false, saving: false, saveErrorKind: null, saveUnconfirmed: true })
    expect(badge.classes()).not.toContain('is-quiet')
    expect(badge.classes()).toContain('is-conflict')
    expect(saveBtn.classes()).toContain('is-alert')
    expect(saveBtn.attributes('disabled')).toBeUndefined()

    await wrapper.setProps({ dirty: true, saving: false, conflict: true, saveErrorKind: 'conflict', saveUnconfirmed: false })
    expect(badge.classes()).not.toContain('is-quiet')
    expect(badge.classes()).toContain('is-conflict')
    expect(saveBtn.classes()).not.toContain('is-alert')

    wrapper.unmount()
  })
})
