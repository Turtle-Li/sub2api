import { useCallback, useEffect, useRef, useState } from 'react'
import type { BatchImageJob } from '@/api/batchImage'
import { galleryPageSize, taskPending, type GalleryFilters, type ItemContinuation, type StudioTask } from './api'
import type { workspaceAPI } from './workspaceAPI'

type HistoryAPI = Pick<ReturnType<typeof workspaceAPI>, 'tasks' | 'items' | 'refreshPending'>
type Phase = 'reading' | 'appending' | 'idle'
type Failure = 'read' | 'append'

interface GalleryHistory {
  filters: GalleryFilters
  tasks: StudioTask[]
  readonly visibleLimit: number
  continuations: ItemContinuation[]
  cursor: string | null
  pages: number
  phase: Phase
  failure: Failure | null
}

const emptyHistory = (filters: GalleryFilters): GalleryHistory => ({
  filters, tasks: [], visibleLimit: galleryPageSize, continuations: [], cursor: null, pages: 1, phase: 'reading', failure: null,
})

function visibleTasks({ tasks, visibleLimit }: GalleryHistory) {
  return tasks.length > visibleLimit ? tasks.slice(0, visibleLimit) : tasks
}

function galleryPageKey(history: GalleryHistory) {
  const next = history.continuations[0]
  const boundary = next ? [next.job.id, next.cursor] : history.cursor
  if (!boundary && history.tasks.length <= history.visibleLimit) return null
  return JSON.stringify([history.visibleLimit, boundary])
}

function appendTasks(current: StudioTask[], incoming: StudioTask[]) {
  const known = new Set(current.map(task => task.id))
  const added = [...incoming].sort((a, b) => b.job.created_at - a.job.created_at).filter(task => {
    if (known.has(task.id)) return false
    known.add(task.id)
    return true
  })
  return added.length ? [...current, ...added] : current
}

function reconcileTasks(current: StudioTask[], incoming: StudioTask[]) {
  const byId = new Map(incoming.map(task => [task.id, task]))
  let newest = -Infinity
  const retained = current.flatMap(task => {
    newest = Math.max(newest, task.job.created_at)
    const latest = byId.get(task.id)
    byId.delete(task.id)
    return latest ? [latest] : []
  })
  const added = appendTasks([], [...byId.values()])
  return [...added.filter(task => task.job.created_at >= newest), ...retained, ...added.filter(task => task.job.created_at < newest)]
}

function mergeContinuations(current: ItemContinuation[], incoming: ItemContinuation[]) {
  return [...new Map([...current, ...incoming].map(item => [item.job.id, item])).values()]
}

function withoutDenied(history: GalleryHistory, denied: ReadonlySet<string>) {
  if (!denied.size) return history
  return { ...history, tasks: history.tasks.filter(task => !denied.has(task.job.id)), continuations: history.continuations.filter(item => !denied.has(item.job.id)) }
}

export function useGalleryHistory(api: HistoryAPI, signal: AbortSignal, filters: GalleryFilters, refresh: number, onPollError: (error: unknown) => void) {
  const [state, setState] = useState(() => emptyHistory(filters))
  const latest = useRef(state)
  const request = useRef<symbol | null>(null)
  const revision = useRef(0)
  const publish = useCallback((next: GalleryHistory) => {
    latest.current = next
    setState(next)
  }, [])

  useEffect(() => {
    const start = latest.current.filters === filters ? latest.current : emptyHistory(filters)
    const ticket = Symbol()
    const version = ++revision.current
    const currentRequest = () => request.current === ticket && !signal.aborted
    const canCommit = () => currentRequest() && revision.current === version
    request.current = ticket
    publish({ ...start, phase: 'reading', failure: null })

    async function read() {
      let tasks: StudioTask[] = []
      let continuations: ItemContinuation[] = []
      let pages = 0
      const denied = new Set<string>()
      const patchLoadedTasks = () => {
        const current = withoutDenied(latest.current, denied)
        const updated = new Map(tasks.map(task => [task.id, task]))
        return { ...current, tasks: current.tasks.map(task => updated.get(task.id) ?? task) }
      }
      try {
        let cursor = ''
        do {
          const next = await api.tasks(cursor, filters)
          if (!canCommit()) return
          next.deniedBatchIds.forEach(id => denied.add(id))
          tasks = appendTasks(tasks, next.tasks)
          continuations = mergeContinuations(continuations, next.itemContinuations)
          if (next.nextCursor === cursor || next.incomplete) {
            const current = patchLoadedTasks()
            if (!start.tasks.length && tasks.length && next.nextCursor && next.nextCursor !== cursor) {
              publish({ ...current, tasks: appendTasks(current.tasks, tasks.filter(task => !denied.has(task.job.id))), continuations: continuations.filter(item => !denied.has(item.job.id)), cursor: next.nextCursor, pages: pages + 1, failure: 'append' })
            } else publish({ ...current, failure: 'read' })
            return
          }
          cursor = next.nextCursor || ''
          pages++
        } while (cursor && pages < start.pages)

        const current = withoutDenied(latest.current, denied)
        publish({ ...current, tasks: reconcileTasks(current.tasks, tasks.filter(task => !denied.has(task.job.id))), continuations: continuations.filter(item => !denied.has(item.job.id)), cursor: cursor || null, pages })
      } catch {
        if (canCommit()) publish({ ...patchLoadedTasks(), failure: 'read' })
      } finally {
        if (currentRequest()) {
          request.current = null
          publish({ ...latest.current, phase: 'idle' })
        }
      }
    }

    void read()
    return () => { request.current = null; revision.current++ }
  }, [api, filters, publish, refresh, signal])

  const history = state.filters === filters ? state : emptyHistory(filters)
  const pageKey = galleryPageKey(history)
  const appendPage = useCallback(async (retry: boolean) => {
    const start = latest.current
    const continuation = start.continuations[0]
    if (request.current || signal.aborted || start.filters !== filters || start.phase !== 'idle' || !pageKey || pageKey !== galleryPageKey(start)) return
    if (start.failure && !(retry && start.failure === 'append')) return

    const visibleLimit = visibleTasks(start).length + galleryPageSize
    if (!start.failure && (start.tasks.length >= visibleLimit || (!start.cursor && !continuation))) {
      publish({ ...start, visibleLimit })
      return
    }

    const ticket = Symbol()
    const version = revision.current
    const currentRequest = () => request.current === ticket && !signal.aborted
    const canCommit = () => currentRequest() && revision.current === version
    request.current = ticket
    publish({ ...start, phase: 'appending', failure: null })
    try {
      if (continuation) {
        const page = await api.items(continuation.job, continuation.cursor)
        if (!canCommit()) return
        const current = latest.current
        if (page.next?.cursor === continuation.cursor) {
          publish({ ...current, failure: 'append' })
          return
        }
        const job = current.tasks.find(task => task.job.id === continuation.job.id)?.job ?? continuation.job
        const continuations = current.continuations.flatMap(item => item.job.id === job.id ? page.next ? [{ ...page.next, job }] : [] : [item])
        publish({ ...current, tasks: appendTasks(current.tasks, page.tasks.map(task => ({ ...task, job }))), visibleLimit, continuations })
      } else if (start.cursor) {
        const page = await api.tasks(start.cursor, filters)
        if (!canCommit()) return
        const current = withoutDenied(latest.current, page.deniedBatchIds)
        if (page.nextCursor === start.cursor) {
          publish({ ...current, failure: 'append' })
          return
        }
        publish({
          ...current,
          tasks: appendTasks(current.tasks, page.tasks),
          visibleLimit,
          continuations: mergeContinuations(current.continuations, page.itemContinuations),
          cursor: page.nextCursor,
          pages: current.pages + 1,
          failure: page.incomplete ? 'append' : null,
        })
      }
    } catch {
      if (canCommit()) publish({ ...latest.current, failure: 'append' })
    } finally {
      if (currentRequest()) {
        request.current = null
        publish({ ...latest.current, phase: 'idle' })
      }
    }
  }, [api, filters, pageKey, publish, signal])
  const loadMore = useCallback(() => void appendPage(false), [appendPage])
  const retryMore = useCallback(() => void appendPage(true), [appendPage])

  const tasks = visibleTasks(history)
  const hasPending = tasks.some(taskPending)
  useEffect(() => {
    if (!hasPending) return
    let active = true
    let timer: ReturnType<typeof setTimeout>
    async function poll() {
      const version = revision.current
      try {
        const updates = await api.refreshPending(visibleTasks(latest.current))
        if (active && !signal.aborted && version === revision.current && latest.current.phase !== 'reading') {
          const byId = new Map(updates.map(task => [task.id, task]))
          publish({ ...latest.current, tasks: latest.current.tasks.map(task => byId.get(task.id) ?? task) })
        }
      } catch (error) {
        if (active && !signal.aborted && version === revision.current) onPollError(error)
      } finally {
        if (active && !signal.aborted) timer = setTimeout(() => void poll(), 6000)
      }
    }
    timer = setTimeout(() => void poll(), 6000)
    return () => { active = false; clearTimeout(timer) }
  }, [api, hasPending, onPollError, publish, signal])

  const updateJob = (job: BatchImageJob) => {
    revision.current++
    publish({
      ...latest.current,
      tasks: latest.current.tasks.map(task => task.job.id === job.id ? { ...task, job } : task),
      continuations: latest.current.continuations.map(item => item.job.id === job.id ? { ...item, job } : item),
    })
  }

  return { tasks, phase: history.phase, failure: history.failure, pageKey, loadMore, retryMore, updateJob }
}
