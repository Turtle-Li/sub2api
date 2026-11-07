import React from 'react'
// Adapted from 88lin/gpt-image-studio 5523a592, MIT; see UPSTREAM.md.
import { useEffect, useState, useRef, type ReactNode } from 'react'
import { taskDisplay, type StudioTask, type studioAPI } from '../../api'
import { useNearViewport } from '../hooks/useNearViewport'
import { CodeIcon } from './icons'
import ViewportTooltip from './ViewportTooltip'
function TaskActionButton({
  tooltip,
  className,
  disabled = false,
  onClick,
  children,
}: {
  tooltip: string
  className: string
  disabled?: boolean
  onClick?: () => void
  children: ReactNode
}) {
  const [tooltipVisible, setTooltipVisible] = useState(false)

  return (
    <span
      className="relative inline-flex"
      onMouseEnter={() => setTooltipVisible(true)}
      onMouseLeave={() => setTooltipVisible(false)}
      onFocus={() => setTooltipVisible(true)}
      onBlur={() => setTooltipVisible(false)}
    >
      <button
        type="button"
        onClick={onClick}
        className={className}
        disabled={disabled}
        aria-label={tooltip}
      >
        {children}
      </button>
      <ViewportTooltip visible={tooltipVisible} className="whitespace-nowrap">
        {tooltip}
      </ViewportTooltip>
    </span>
  )
}

export default function TaskCard({ record, api, onOpen, onEditOutputs }: { record: StudioTask; api: ReturnType<typeof studioAPI>; onOpen: () => void; onEditOutputs: () => void }) {
 const cardRef = useRef<HTMLDivElement>(null)
 const nearViewport = useNearViewport(cardRef)
 const [thumbSrc, setThumbSrc] = useState('')
 const [imageError, setImageError] = useState('')
 const display = taskDisplay(record)
 const task = { status: ['failed', 'cancelled', 'output_deleted'].includes(display.status) ? 'error' : ['success', 'completed'].includes(display.status) || record.item.image_count ? 'done' : 'running', outputImages: Array.from({length: record.item.image_count}, (_, i) => `${record.id}/${i}`), apiProvider: record.job.provider, apiProfileName: '', apiModel: record.job.model }
 const showModel = Boolean(task.apiModel)
 const isInterrupted = display.status === 'cancelled'
 useEffect(() => {
   if (!nearViewport || !record.item.image_count || record.job.status !== 'completed') return
   const controller = new AbortController()
   let active = true; let url = ''
   setImageError('')
   setThumbSrc('')
   api.thumbnail(record, controller.signal).then(blob => { if (active) { url = URL.createObjectURL(blob); setThumbSrc(url) } }).catch(error => { if (active) setImageError(error instanceof Error ? error.message : '图片加载失败') })
   return () => { active = false; controller.abort(); if (url) URL.revokeObjectURL(url) }
 }, [nearViewport, record, api])
 return <div className="relative rounded-xl">
   <div ref={cardRef} className="studio-source-card relative bg-white dark:bg-gray-900 rounded-xl border border-gray-200 dark:border-white/[0.08] overflow-hidden cursor-pointer hover:shadow-lg" role="button" tabIndex={0} aria-label="查看任务详情" onClick={onOpen} onKeyDown={event => { if (event.target !== event.currentTarget) return; if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); onOpen() } }}>
      <div className="flex h-40">
        {/* 左侧图片区域 */}
        <div className="w-40 min-w-[10rem] h-full bg-gray-100 dark:bg-black/20 relative flex items-center justify-center overflow-hidden flex-shrink-0">
          {task.status === 'running' && (
            <div className="flex flex-col items-center gap-2">
              <svg
                className="w-8 h-8 text-blue-400 animate-spin"
                fill="none"
                viewBox="0 0 24 24"
              >
                <circle
                  className="opacity-25"
                  cx="12"
                  cy="12"
                  r="10"
                  stroke="currentColor"
                  strokeWidth="4"
                />
                <path
                  className="opacity-75"
                  fill="currentColor"
                  d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
                />
              </svg>
              <span className="text-xs text-gray-400 dark:text-gray-500">生成中...</span>
            </div>
          )}
          {task.status === 'error' && (
            <div className="flex flex-col items-center gap-1 px-2">
              <svg
                className={`w-7 h-7 ${isInterrupted ? 'text-yellow-400' : 'text-red-400'}`}
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth={2}
                  d="M12 9v2m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
                />
              </svg>
              <span className={`text-xs text-center leading-tight ${isInterrupted ? 'text-yellow-500' : 'text-red-400'}`}>
                {display.label}
              </span>
            </div>
          )}
          {task.status === 'done' && thumbSrc && (
            <>
              <img
                src={thumbSrc}
                data-image-id={task.outputImages[0]}
                data-output-image-ids={task.outputImages.join(',')}
                className="saveable-image w-full h-full object-cover"
                loading="lazy"
                alt={record.item.prompt_preview || "生成图片"}
              />
              {(task.outputImages.length > 1) && (
                <span className="absolute bottom-1 right-1 bg-black/60 text-white text-xs px-1.5 py-0.5 rounded">
                  {task.outputImages.length}
                </span>
              )}
            </>
          )}
          {task.status === 'done' && !thumbSrc && (
            <svg
              className="w-8 h-8 text-gray-300"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={1.5}
                d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z"
              />
            </svg>
          )}
          <span className="absolute top-1.5 left-1.5 bg-black/60 text-white text-xs px-1.5 py-0.5 rounded">{display.label}</span>
        </div>

        {/* 右侧信息区域 */}
        <div className="flex-1 p-3 flex flex-col min-w-0">
          <div className="flex-1 min-h-0 mb-2 overflow-hidden">
            <p className="text-sm text-gray-700 dark:text-gray-300 leading-relaxed line-clamp-3">{record.item.prompt_preview || '(无提示词)'}</p>
            {display.message && <p className="text-xs text-red-400 line-clamp-2">{display.message}</p>}
            {imageError && <p className="text-xs text-red-400">{imageError}</p>}
          </div>
          <div className="mt-auto flex flex-col gap-1.5">
            {/* 参数与信息：横向滚动 */}
            <div 
              data-tag-scroll-area
              className="flex overflow-x-auto hide-scrollbar pt-0.5 gap-1.5 whitespace-nowrap mask-edge-r min-w-0 pr-2"
              onTouchStart={(e) => e.stopPropagation()}
              onTouchMove={(e) => e.stopPropagation()}
              onTouchEnd={(e) => e.stopPropagation()}
              onTouchCancel={(e) => e.stopPropagation()}
            >
              {/* API Name */}
              {(task.apiProfileName || task.apiProvider) && (
                <span 
                  className="flex items-center gap-1 px-1.5 py-0.5 rounded bg-gray-100 dark:bg-white/[0.04] text-gray-600 dark:text-gray-300 text-xs flex-shrink-0"
                  title={task.apiProfileName || task.apiProvider}
                >
                  <CodeIcon className="w-3 h-3 flex-shrink-0 text-gray-400" />
                  <span className="truncate max-w-[8rem]">
                    {task.apiProfileName || task.apiProvider}
                  </span>
                </span>
              )}
              {/* Model */}
              {showModel && (
                <span 
                  className="flex items-center gap-1 px-1.5 py-0.5 rounded bg-gray-100 dark:bg-white/[0.04] text-gray-600 dark:text-gray-300 text-xs flex-shrink-0"
                  title={task.apiModel}
                >
                  <svg className="w-3 h-3 flex-shrink-0 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 3v4M3 5h4M6 17v4m-2-2h4m5-16l2.286 6.857L21 12l-5.714 2.143L13 21l-2.286-6.857L5 12l5.714-2.143L13 3z" />
                  </svg>
                  <span className="truncate max-w-[8rem]">
                    {task.apiModel}
                  </span>
                </span>
              )}

            </div>
            {/* 操作按钮 */}
            <div
              data-tag-scroll-area
              className="flex items-center gap-1 flex-shrink-0 mt-0.5 ml-auto max-w-full overflow-x-auto hide-scrollbar mask-edge-r pr-2"
              onClick={(e) => e.stopPropagation()}
              onTouchStart={(e) => e.stopPropagation()}
              onTouchMove={(e) => e.stopPropagation()}
              onTouchEnd={(e) => e.stopPropagation()}
              onTouchCancel={(e) => e.stopPropagation()}
            >
              <TaskActionButton
                tooltip="查看详情"
                onClick={onOpen}
                className="p-1.5 rounded-md hover:bg-blue-50 dark:hover:bg-blue-950/30 text-gray-400 hover:text-blue-500 transition"
              >
                <svg
                  className="w-4 h-4"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeWidth={2}
                    d="M3 10h10a8 8 0 018 8v2M3 10l6 6m-6-6l6-6"
                  />
                </svg>
              </TaskActionButton>
              <TaskActionButton
                tooltip="继续改图"
                onClick={onEditOutputs}
                className="p-1.5 rounded-md hover:bg-green-50 dark:hover:bg-green-950/30 text-gray-400 hover:text-green-500 transition disabled:opacity-30"
                disabled={!task.outputImages?.length}
              >
                <svg
                  className="w-4 h-4"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeWidth={2}
                    d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z"
                  />
                </svg>
              </TaskActionButton>

            </div>
          </div>
        </div>
      </div>
      </div>
    </div>

}
