import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { Studio } from '../Studio'
import { listKeys, studioAPI, studioWalletRoute, type ItemContinuation, type StudioTask } from '../api'

vi.mock('../api', async importOriginal => ({
  ...await importOriginal<typeof import('../api')>(), listKeys: vi.fn(), studioAPI: vi.fn(),
}))

const task = (id: string, timestamp: number, failed = false) => ({
  id: `${id}/one`,
  job: { id, status: failed ? 'failed' : 'completed', model: 'fixture', provider: 'openai_images', created_at: timestamp },
  item: { custom_id: 'one', status: failed ? 'failed' : 'succeeded', image_count: 0, prompt_preview: id },
}) as StudioTask

const page = (tasks: StudioTask[], nextCursor: string | null = null, itemContinuations: ItemContinuation[] = []) => ({ tasks, nextCursor, itemContinuations })
const fixtureKey = (id: number) => ({ id, name: 'fixture', group_name: 'fixture', status: 'active', expired: false, quota_exhausted: false })

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((ok, fail) => { resolve = ok; reject = fail })
  return { promise, resolve, reject }
}

const tailObservers: (() => void)[] = []
const coverObservers = new Map<Element, () => void>()
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
let userId = 1020
beforeEach(() => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  vi.stubGlobal('IntersectionObserver', class {
    constructor(private readonly callback: IntersectionObserverCallback) {}
    observe(target: Element) {
      const enter = () => this.callback([{ target, isIntersecting: true } as IntersectionObserverEntry], this as unknown as IntersectionObserver)
      if (target.matches('.studio-gallery-tail')) {
        tailObservers.push(enter)
      } else if (target.matches('.studio-source-card')) {
        coverObservers.set(target, enter)
      }
    }
    unobserve(target: Element) { coverObservers.delete(target) }
    disconnect() {}
  })
  tailObservers.length = 0
  coverObservers.clear()
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  userId++
  vi.mocked(listKeys).mockResolvedValue([{ id: userId, name: 'fixture', group_name: 'fixture', status: 'active', expired: false, quota_exhausted: false }])
})

afterEach(() => {
  act(() => root.unmount())
  host.remove()
  vi.unstubAllGlobals()
  vi.resetAllMocks()
})

function clientWith(tasks: ReturnType<typeof studioAPI>['tasks'], items = vi.fn<ReturnType<typeof studioAPI>['items']>()) {
  return { tasks, items, models: vi.fn(async () => []), refreshPending: vi.fn(), image: vi.fn(), thumbnail: vi.fn(), archivePreview: vi.fn(), submit: vi.fn(), cancel: vi.fn() }
}
const show = () => act(async () => root.render(<Studio userId={userId} />))
function cards() {
  const columns = [...host.querySelectorAll('.studio-gallery-column')]
  return columns.flatMap((column, index) => [...column.querySelectorAll('[data-task-id]')].map((node, row) => ({ node, position: row * columns.length + index })))
    .sort((a, b) => a.position - b.position).map(card => card.node)
}
const ids = () => cards().map(el => el.getAttribute('data-task-id'))
const tailAction = () => host.querySelector<HTMLButtonElement>('.studio-gallery-tail button')!

it('commits the first page once instead of showing 1, 5, 10 and then 14 cards', async () => {
  const scopes = [1, 4, 5, 4].map((size, index) => {
    const gate = deferred<ReturnType<typeof page>>()
    const rows = Array.from({ length: size }, (_, i) => task(`scope-${index}-${i}`, 100 - index * 10 - i))
    const route = index === 0 ? studioWalletRoute : 60 + index
    return { key: fixtureKey(route), gate, rows, client: clientWith(vi.fn(() => gate.promise)) }
  })
  vi.mocked(listKeys).mockImplementation(async (_signal, setWalletMode) => {
    setWalletMode?.('on')
    return scopes.slice(1).map(scope => scope.key)
  })
  vi.mocked(studioAPI).mockImplementation(key => {
    const scope = scopes.find(candidate => candidate.key.id === key)
    if (!scope) throw new Error(`Unexpected fixture key ${key}`)
    return scope.client
  })
  await show()
  for (const scope of scopes.slice(0, -1)) {
    await act(async () => scope.gate.resolve(page(scope.rows)))
    expect(cards()).toHaveLength(0)
    expect(host.querySelector('.studio-gallery-start-loading')).not.toBeNull()
  }
  const last = scopes[scopes.length - 1]
  await act(async () => last.gate.resolve(page(last.rows)))
  expect(ids()).toEqual(scopes.flatMap(scope => scope.rows.map(row => row.id)))
  expect(host.querySelector('.studio-gallery-heading')?.textContent).toBe('我的作品')
})

it('shows 24 combined metadata rows at a time and does not fetch again for a buffered page', async () => {
  const rows = Array.from({ length: 77 }, (_, i) => task(`work-${i}`, 100 - i))
  const next = deferred<ReturnType<typeof page>>()
  const first = vi.fn<ReturnType<typeof studioAPI>['tasks']>()
    .mockResolvedValueOnce(page(rows.slice(0, 24), 'older'))
    .mockReturnValueOnce(next.promise)
  const second = vi.fn<ReturnType<typeof studioAPI>['tasks']>().mockResolvedValue(page(rows.slice(24, 48)))
  const third = vi.fn<ReturnType<typeof studioAPI>['tasks']>().mockResolvedValue(page(rows.slice(48, 53)))
  vi.mocked(listKeys).mockResolvedValue([fixtureKey(60), fixtureKey(61), fixtureKey(62)])
  vi.mocked(studioAPI).mockImplementation(key => clientWith(key === 60 ? first : key === 61 ? second : third))

  await show()
  expect(ids()).toEqual(rows.slice(0, 24).map(row => row.id))
  const firstCard = cards()[0]
  const enter = tailObservers.at(-1)
  await act(async () => { enter?.(); enter?.(); tailAction().click() })
  expect(ids()).toEqual(rows.slice(0, 48).map(row => row.id))
  expect(first).toHaveBeenCalledOnce()
  expect(second).toHaveBeenCalledOnce()
  expect(third).toHaveBeenCalledOnce()

  await act(async () => tailAction().click())
  expect(first.mock.calls.map(([cursor]) => cursor)).toEqual(['', 'older'])
  expect(ids()).toEqual(rows.slice(0, 48).map(row => row.id))
  await act(async () => next.resolve(page(rows.slice(53))))
  expect(ids()).toEqual(rows.slice(0, 72).map(row => row.id))
  await act(async () => tailAction().click())
  expect(ids()).toEqual(rows.map(row => row.id))
  expect(first).toHaveBeenCalledTimes(2)
  expect(cards()[0]).toBe(firstCard)
  expect(tailAction()).toBeNull()
})

it('commits available history once when a scope fails and only retries that exhausted page on request', async () => {
  const available = task('available', 10)
  const recovered = task('recovered', 9)
  const slow = deferred<ReturnType<typeof page>>()
  const first = vi.fn<ReturnType<typeof studioAPI>['tasks']>().mockResolvedValue(page([available]))
  const second = vi.fn<ReturnType<typeof studioAPI>['tasks']>()
    .mockReturnValueOnce(slow.promise)
    .mockResolvedValueOnce(page([recovered]))
  vi.mocked(listKeys).mockResolvedValue([fixtureKey(60), fixtureKey(61)])
  vi.mocked(studioAPI).mockImplementation(key => clientWith(key === 60 ? first : second))

  await show()
  expect(cards()).toHaveLength(0)
  await act(async () => slow.reject({ status: 503 }))
  expect(ids()).toEqual([available.id])
  expect(tailAction().textContent).toBe('重试加载')
  const availableCard = cards()[0]
  await act(async () => tailAction().click())
  expect(ids()).toEqual([available.id, recovered.id])
  expect(cards()[0]).toBe(availableCard)
  expect(first).toHaveBeenCalledOnce()
  expect(second).toHaveBeenCalledTimes(2)
})

it('mounts all 24 proportional cards and appends the next page while visible covers are stalled', async () => {
  const firstPage = Array.from({ length: 24 }, (_, i) => {
    const record = task(`first-${i}`, 100 - i)
    const sizes = [[768, 1024], [1600, 900], [1024, 1024]]
    const [width, height] = sizes[i % sizes.length]
    return { ...record, item: { ...record.item, image_count: 1, ...(i !== 3 && { preview_size: { width, height } }) } }
  })
  const next = task('older', 1)
  const reads = vi.fn<ReturnType<typeof studioAPI>['tasks']>()
    .mockResolvedValueOnce(page(firstPage, 'older'))
    .mockResolvedValueOnce(page([next]))
  const cover = deferred<Blob>()
  const client = clientWith(reads)
  client.thumbnail.mockReturnValue(cover.promise)
  vi.mocked(studioAPI).mockReturnValue(client)
  vi.stubGlobal('URL', class extends URL {
    static createObjectURL = vi.fn(() => 'blob:delayed-cover')
    static revokeObjectURL = vi.fn()
  })

  await show()
  expect(cards()).toHaveLength(24)
  const frames = firstPage.slice(0, 4).map(record => host.querySelector<HTMLElement>(`[data-task-id="${record.id}"] .studio-card-preview`)!)
  const aspects = frames.map(frame => frame.style.aspectRatio)
  expect(aspects).toEqual(['0.75', String(16 / 9), '1', ''])
  expect(client.thumbnail).not.toHaveBeenCalled()
  await act(async () => { for (const frame of frames) coverObservers.get(frame.closest('.studio-source-card')!)?.() })
  expect(client.thumbnail).toHaveBeenCalledTimes(frames.length)
  expect(host.querySelector('img')).toBeNull()

  await act(async () => tailObservers.at(-1)?.())
  expect(reads.mock.calls.map(([cursor]) => cursor)).toEqual(['', 'older'])
  expect(cards()).toHaveLength(25)
  expect(host.querySelector('img')).toBeNull()

  await act(async () => cover.resolve(new Blob(['cover'], { type: 'image/jpeg' })))
  const images = [...host.querySelectorAll<HTMLImageElement>('.studio-card-preview img')]
  expect(images).toHaveLength(4)
  await act(async () => { for (const image of images) image.dispatchEvent(new Event('load')) })
  expect(frames.map(frame => frame.style.aspectRatio)).toEqual(aspects)
  expect(client.image).not.toHaveBeenCalled()
})

it('loads once per scroll cursor and waits for explicit retry after a partial page error', async () => {
  const first = task('already-visible', 1)
  const newestFromOtherPage = task('next-page', 5)
  const busy = deferred<ReturnType<typeof page>>()
  const tasks = vi.fn<ReturnType<typeof studioAPI>['tasks']>()
    .mockResolvedValueOnce(page([first], 'next'))
    .mockReturnValueOnce(busy.promise)
    .mockResolvedValueOnce(page([first, newestFromOtherPage]))
  vi.mocked(studioAPI).mockReturnValue(clientWith(tasks))
  await show()
  const card = cards()[0]
  const priorFocus = document.activeElement
  expect(priorFocus).not.toBe(tailAction())
  const enter = tailObservers.at(-1)!
  await act(async () => { enter(); enter(); tailAction().click() })
  expect(tasks.mock.calls.map(([cursor]) => cursor)).toEqual(['', 'next'])
  expect(tailAction().getAttribute('aria-disabled')).toBe('true')

  await act(async () => busy.reject({ status: 503 }))
  await act(async () => enter())
  expect(tasks).toHaveBeenCalledTimes(2)
  expect(cards()[0]).toBe(card)
  await act(async () => tailAction().click())
  expect(tasks.mock.calls.map(([cursor]) => cursor)).toEqual(['', 'next', 'next'])
  expect(ids()).toEqual([first.id, newestFromOtherPage.id])
  expect(cards()[0]).toBe(card)
  expect(tailAction()).toBeNull()
  expect(document.activeElement).toBe(priorFocus)
})

it('discards a late batch continuation when the status filter changes', async () => {
  const original = task('old-filter', 10)
  const filtered = task('new-filter', 9, true)
  const stale = task('late-continuation', 8, true)
  const busy = deferred<Awaited<ReturnType<ReturnType<typeof studioAPI>['items']>>>()
  const tasks = vi.fn<ReturnType<typeof studioAPI>['tasks']>().mockImplementation(async (_, filters) => filters?.status === 'failed' ? page([filtered]) : page([original], null, [{ job: original.job, cursor: 100 }]))
  const items = vi.fn<ReturnType<typeof studioAPI>['items']>().mockReturnValue(busy.promise)
  vi.mocked(studioAPI).mockReturnValue(clientWith(tasks, items))
  await show()
  await act(async () => tailObservers.at(-1)!())
  expect(items).toHaveBeenCalledWith(original.job, 100)

  const tab = [...host.querySelectorAll<HTMLButtonElement>('.studio-gallery-tabs button')].find(button => button.textContent === '失败')!
  await act(async () => tab.click())
  expect(ids()).toEqual([filtered.id])
  await act(async () => busy.resolve({ tasks: [stale], next: { job: original.job, cursor: 200 } }))
  expect(ids()).toEqual([filtered.id])
  expect(tailAction()).toBeNull()
})

it('keeps the populated view in place when a refresh supersedes a delayed append', async () => {
  const first = task('first', 3)
  const second = task('second', 2)
  const stale = task('stale', 1)
  const oldAppend = deferred<ReturnType<typeof page>>()
  const refresh = deferred<ReturnType<typeof page>>()
  const tasks = vi.fn<ReturnType<typeof studioAPI>['tasks']>()
    .mockResolvedValueOnce(page([first], 'second'))
    .mockResolvedValueOnce(page([second], 'third'))
    .mockReturnValueOnce(oldAppend.promise)
    .mockReturnValueOnce(refresh.promise)
    .mockResolvedValueOnce(page([{ ...second }]))
  vi.mocked(studioAPI).mockReturnValue(clientWith(tasks))
  await show()
  await act(async () => tailObservers.at(-1)!())
  const retained = cards()
  await act(async () => tailObservers.at(-1)!())
  await act(async () => host.querySelector<HTMLButtonElement>('.studio-refresh')!.click())
  expect(cards()).toEqual(retained)
  await act(async () => oldAppend.resolve(page([stale])))
  expect(cards()).toEqual(retained)
  await act(async () => refresh.resolve(page([{ ...first }], 'second')))
  expect(ids()).toEqual([first.id, second.id])
  expect(cards()).toEqual(retained)
  expect(tasks.mock.calls.map(([cursor]) => cursor)).toEqual(['', 'second', 'third', '', 'second'])
})

it('preserves usable history but removes cards from an explicitly denied scope during a partial refresh', async () => {
  const kept = task('kept', 3)
  const revoked = task('revoked', 2)
  const temporary = task('temporary', 1)
  const good = vi.fn<ReturnType<typeof studioAPI>['tasks']>().mockResolvedValue(page([kept]))
  const denied = vi.fn<ReturnType<typeof studioAPI>['tasks']>().mockResolvedValueOnce(page([revoked])).mockRejectedValue({ status: 403 })
  const unavailable = vi.fn<ReturnType<typeof studioAPI>['tasks']>().mockResolvedValueOnce(page([temporary])).mockRejectedValue({ status: 503 })
  vi.mocked(listKeys).mockResolvedValue([fixtureKey(60), fixtureKey(61), fixtureKey(62)])
  vi.mocked(studioAPI).mockImplementation(key => clientWith(key === 60 ? good : key === 61 ? denied : unavailable))
  await show()
  expect(ids()).toEqual([kept.id, revoked.id, temporary.id])
  await act(async () => host.querySelector<HTMLButtonElement>('.studio-refresh')!.click())
  expect(ids()).toEqual([kept.id, temporary.id])
  expect(tailAction()).not.toBeNull()
  expect(unavailable).toHaveBeenCalledTimes(2)
})

it('offers the same single load control without IntersectionObserver', async () => {
  vi.stubGlobal('IntersectionObserver', undefined)
  const tasks = vi.fn<ReturnType<typeof studioAPI>['tasks']>()
    .mockResolvedValueOnce(page([task('first', 2)], 'next'))
    .mockResolvedValueOnce(page([task('second', 1)]))
  vi.mocked(studioAPI).mockReturnValue(clientWith(tasks))
  await show()
  expect(tasks).toHaveBeenCalledOnce()
  expect(host.querySelectorAll('.studio-gallery-tail button')).toHaveLength(1)
  await act(async () => { tailAction().focus(); tailAction().click() })
  expect(ids()).toEqual(['first/one', 'second/one'])
  expect(document.activeElement).toBe(host.querySelector('.studio-gallery-tail [role="status"]'))
})

it('uses the size learned for a legacy cover on metadata refresh without replacing its card', async () => {
  const row = task('legacy', 1)
  const legacy = { ...row, item: { ...row.item, image_count: 1 } }
  const refreshed = deferred<ReturnType<typeof page>>()
  const tasks = vi.fn<ReturnType<typeof studioAPI>['tasks']>()
    .mockResolvedValueOnce(page([legacy]))
    .mockReturnValueOnce(refreshed.promise)
  const client = clientWith(tasks)
  vi.mocked(studioAPI).mockReturnValue(client)

  await show()
  const [card] = cards()
  const frame = card.querySelector<HTMLElement>('.studio-card-preview')!
  expect(frame.style.aspectRatio).toBe('')
  await act(async () => host.querySelector<HTMLButtonElement>('.studio-refresh')?.click())
  expect(cards()).toEqual([card])
  expect(frame.style.aspectRatio).toBe('')
  await act(async () => refreshed.resolve(page([{ ...legacy, item: { ...legacy.item, preview_size: { width: 360, height: 480 } } }])))
  expect(cards()).toEqual([card])
  expect(frame.style.aspectRatio).toBe('0.75')
  expect(client.thumbnail).not.toHaveBeenCalled()
})

it('filters gallery items via LiquidSelect status control while keeping segmented tabs and query status in sync', async () => {
  const okRow = task('done-item', 2, false)
  const failedRow = task('failed-item', 1, true)
  const tasks = vi.fn<ReturnType<typeof studioAPI>['tasks']>(async (_cursor, query) => {
    if (query?.status === 'failed') return page([failedRow])
    return page([okRow, failedRow])
  })
  vi.mocked(studioAPI).mockReturnValue(clientWith(tasks))

  await show()
  expect(ids()).toEqual([okRow.id, failedRow.id])
  expect(tasks).toHaveBeenCalledWith('', expect.objectContaining({ status: 'current' }))

  const statusWrap = host.querySelector<HTMLElement>('.lg-select.studio-gallery-status')!
  expect(statusWrap).not.toBeNull()
  const statusTrigger = statusWrap.querySelector<HTMLButtonElement>('button.lg-select-trigger[aria-label="筛选作品"]')!
  expect(statusTrigger.textContent).toContain('全部作品')

  // Open LiquidSelect menu and pick "失败"
  await act(async () => statusTrigger.click())
  const menu = document.body.querySelector<HTMLDivElement>('.lg-select-menu[role="listbox"][aria-label="筛选作品"]')
  expect(menu).not.toBeNull()
  const failedOption = Array.from(menu!.querySelectorAll<HTMLButtonElement>('[role="option"]'))
    .find(option => option.textContent?.trim() === '失败')
  expect(failedOption).toBeDefined()
  await act(async () => failedOption!.click())

  expect(statusTrigger.textContent).toContain('失败')
  expect(tasks).toHaveBeenCalledWith('', expect.objectContaining({ status: 'failed' }))
  expect(ids()).toEqual([failedRow.id])
  const failedTab = Array.from(host.querySelectorAll<HTMLButtonElement>('.studio-gallery-tabs button'))
    .find(btn => btn.textContent?.trim() === '失败')
  expect(failedTab?.getAttribute('aria-pressed')).toBe('true')
})
