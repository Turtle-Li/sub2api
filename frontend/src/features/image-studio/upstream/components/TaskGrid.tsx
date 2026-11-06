// Adapted from 88lin TaskGrid.tsx 5523a592, MIT; see UPSTREAM.md.
import React, { useMemo, useRef } from 'react'
import { useVirtualGrid } from '../hooks/useVirtualGrid'
import type { StudioTask, studioAPI } from '../../api'
import TaskCard from './TaskCard'

interface Props {
  tasks: StudioTask[]
  api: ReturnType<typeof studioAPI>
  open: (task: StudioTask) => void
  edit: (task: StudioTask) => void
}

export default function TaskGrid({ tasks, api, open, edit }: Props) {
  const rootRef = useRef<HTMLDivElement>(null)
  const gridRef = useRef<HTMLDivElement>(null)
  const filteredTasks = useMemo(() => [...tasks].sort((a, b) => b.job.created_at - a.job.created_at), [tasks])
  const { startIndex, endIndex, paddingTop, paddingBottom } = useVirtualGrid(gridRef, filteredTasks.length)

  return (
    <div
      ref={rootRef}
      data-task-grid-root
      className="relative min-h-[50vh] pb-10"
    >
      <div ref={gridRef} className="studio-source-grid grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4" style={{ paddingTop, paddingBottom }}>
        {filteredTasks.slice(startIndex, endIndex).map((task) => (
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
    </div>
  )
}
