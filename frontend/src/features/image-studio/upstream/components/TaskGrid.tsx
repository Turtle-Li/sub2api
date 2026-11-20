// Adapted from 88lin TaskGrid.tsx 5523a592, MIT; see UPSTREAM.md.
import React, { useLayoutEffect, useRef, useState } from 'react'
import type { StudioTask } from '../../api'
import type { ThumbnailLoader } from '../../thumbnailLoader'
import TaskCard from './TaskCard'

interface Props {
  readonly tasks: readonly StudioTask[]
  readonly previews: ThumbnailLoader
  readonly open: (task: StudioTask) => void
  readonly edit: (task: StudioTask) => void
}

function resolveColumns(width: number): number {
  if (window.matchMedia?.('(max-width: 760px)').matches) return 2
  return width > 0 ? Math.max(2, Math.floor((width + 16) / 236)) : 3
}

export default function TaskGrid({ tasks, previews, open, edit }: Props) {
  const grid = useRef<HTMLDivElement>(null)
  const [columns, setColumns] = useState(3)
  useLayoutEffect(() => {
    const element = grid.current
    if (!element) return
    const measure = () => setColumns(resolveColumns(element.clientWidth))
    measure()
    const observer = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(measure) : undefined
    observer?.observe(element)
    window.addEventListener('resize', measure)
    return () => { observer?.disconnect(); window.removeEventListener('resize', measure) }
  }, [])

  const buckets = Array.from({ length: columns }, (): StudioTask[] => [])
  tasks.forEach((task, index) => buckets[index % columns]?.push(task))

  return (
    <div ref={grid} data-task-grid-root className="studio-source-grid" role="list" style={{ gridTemplateColumns: 'repeat(' + columns + ', minmax(0, 1fr))' }}>
      {buckets.map((bucket, index) => (
        <div key={index} className="studio-gallery-column" role="presentation">
          {bucket.map(task => (
            <div key={task.id} className="task-card-wrapper" data-task-id={task.id} role="listitem">
              <TaskCard record={task} previews={previews} onOpen={() => open(task)} onEditOutputs={() => edit(task)} />
            </div>
          ))}
        </div>
      ))}
    </div>
  )
}
