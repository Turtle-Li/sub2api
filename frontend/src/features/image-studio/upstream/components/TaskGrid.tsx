// Adapted from 88lin TaskGrid.tsx 5523a592, MIT; see UPSTREAM.md.
import React, { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { StudioTask, studioAPI } from '../../api'
import TaskCard from './TaskCard'

const PAGE_STEP = 24

function resolveColumns(width: number): number {
  if (typeof window !== 'undefined' && window.innerWidth > 0 && window.innerWidth <= 760) return 2
  if (width <= 0) return 3
  if (width <= 520) return 2
  return Math.max(2, Math.floor((width + 16) / (220 + 16)))
}

interface Props {
  tasks: StudioTask[]
  api: ReturnType<typeof studioAPI>
  open: (task: StudioTask) => void
  edit: (task: StudioTask) => void
  hasMore?: boolean
  loadingMore?: boolean
  onLoadMore?: () => void
}

export default function TaskGrid({ tasks, api, open, edit, hasMore = false, loadingMore = false, onLoadMore }: Props) {
  const rootRef = useRef<HTMLDivElement>(null)
  const gridRef = useRef<HTMLDivElement>(null)
  const sentinelRef = useRef<HTMLDivElement>(null)
  const filteredTasks = useMemo(() => [...tasks].sort((a, b) => b.job.created_at - a.job.created_at), [tasks])
  const [columns, setColumns] = useState(() => resolveColumns(0))
  const [visibleCount, setVisibleCount] = useState(() => (
    typeof IntersectionObserver === 'undefined' ? filteredTasks.length : Math.max(PAGE_STEP, filteredTasks.length)
  ))

  useEffect(() => {
    setVisibleCount(prev => (
      typeof IntersectionObserver === 'undefined'
        ? filteredTasks.length
        : Math.max(PAGE_STEP, prev, Math.min(filteredTasks.length, prev + PAGE_STEP))
    ))
  }, [filteredTasks.length])

  useLayoutEffect(() => {
    const grid = gridRef.current
    if (!grid) return
    const measure = () => setColumns(resolveColumns(grid.clientWidth))
    measure()
    const ro = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(measure) : null
    ro?.observe(grid)
    window.addEventListener('resize', measure)
    return () => {
      ro?.disconnect()
      window.removeEventListener('resize', measure)
    }
  }, [filteredTasks.length])

  useEffect(() => {
    const sentinel = sentinelRef.current
    if (!sentinel || typeof IntersectionObserver === 'undefined') return
    const observer = new IntersectionObserver(entries => {
      if (!entries.some(entry => entry.isIntersecting)) return
      if (visibleCount < filteredTasks.length) {
        setVisibleCount(prev => Math.min(filteredTasks.length, prev + PAGE_STEP))
      } else if (hasMore && !loadingMore) {
        onLoadMore?.()
      }
    }, { rootMargin: '360px 0px' })
    observer.observe(sentinel)
    return () => observer.disconnect()
  }, [filteredTasks.length, hasMore, loadingMore, onLoadMore, visibleCount])

  const visibleTasks = filteredTasks.slice(0, visibleCount)
  const columnTasks = useMemo(() => {
    const count = Math.max(1, columns)
    const buckets = Array.from({ length: count }, () => [] as StudioTask[])
    visibleTasks.forEach((task, index) => {
      buckets[index % count].push(task)
    })
    return buckets
  }, [visibleTasks, columns])

  return (
    <div
      ref={rootRef}
      data-task-grid-root
      className="relative min-h-[50vh] pb-10"
    >
      <div
        ref={gridRef}
        className="studio-source-grid grid gap-4"
        style={{ '--studio-columns': columns } as React.CSSProperties}
      >
        {columnTasks.map((col, colIndex) => (
          <div key={colIndex} className="studio-gallery-column" data-gallery-column={colIndex}>
            {col.map((task) => (
              <div key={task.id} className="task-card-wrapper" data-task-id={task.id}>
                <TaskCard
                  record={task}
                  api={api}
                  onOpen={() => open(task)}
                  onEditOutputs={() => edit(task)}
                />
              </div>
            ))}
          </div>
        ))}
      </div>
      <div ref={sentinelRef} className="studio-grid-sentinel" aria-hidden="true" />
    </div>
  )
}
