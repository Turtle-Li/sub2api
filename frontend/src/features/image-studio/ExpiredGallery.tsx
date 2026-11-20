import React from 'react'
import { taskDisplay, type StudioTask } from './api'

const createdDate = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' })

export default function ExpiredGallery({ tasks, open }: { readonly tasks: readonly StudioTask[]; readonly open: (task: StudioTask) => void }) {
  return <>
    <p className="studio-muted studio-expired-note">图片已不可查看，这里只保留任务记录。</p>
    <ul className="studio-expired-list" aria-label="已过期作品">
      {tasks.map(task => {
        const createdAt = new Date(task.job.created_at * 1000)
        return <li key={task.id}>
          <button type="button" className="studio-expired-row" onClick={() => open(task)}>
            <span className="studio-expired-summary">
              <span className="studio-expired-title">{task.item.prompt_preview || '无提示词预览'}</span>
              <span className="studio-expired-meta studio-muted">
                <span>{task.job.model}</span>
                <time dateTime={createdAt.toISOString()}>{createdDate.format(createdAt)}</time>
              </span>
            </span>
            <span className="studio-muted">{taskDisplay(task).label}</span>
          </button>
        </li>
      })}
    </ul>
  </>
}
