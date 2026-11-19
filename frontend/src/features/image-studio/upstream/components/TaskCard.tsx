import React from 'react'
// Adapted from 88lin/gpt-image-studio 5523a592, MIT; see UPSTREAM.md.
import { useEffect, useState, useRef, type ReactNode } from 'react'
import { isImageOutputGone, taskDisplay, type StudioTask, type studioAPI } from '../../api'
import { useNearViewport } from '../hooks/useNearViewport'
import { modelBadge } from '../../ModelPicker'
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
 const [aspectRatio, setAspectRatio] = useState<number | null>(null)
 const [imageError, setImageError] = useState('')
 const display = taskDisplay(record)
 const isExpired = display.status === 'output_deleted'
 const isInterrupted = display.status === 'cancelled'
 const status = isExpired ? 'expired' : isInterrupted ? 'cancelled' : display.status === 'failed' ? 'error' : display.canOpenArchive ? 'archived' : ['succeeded', 'success', 'completed'].includes(display.status) ? 'done' : 'running'
 const badge = modelBadge(record.job.provider, record.job.model)
 const hasPreviewImage = display.canViewOutput && Boolean(thumbSrc)
 const outputImages = Array.from({ length: record.item.image_count }, (_, i) => `${record.id}/${i}`)
 useEffect(() => {
   if (!nearViewport || !display.canViewOutput) return
   const controller = new AbortController()
   let active = true; let url = ''
   setImageError('')
   setThumbSrc('')
   setAspectRatio(null)
   api.thumbnail(record, controller.signal).then(blob => { if (active) { url = URL.createObjectURL(blob); setThumbSrc(url) } }).catch(error => {
     if (active) setImageError(isImageOutputGone(error) ? '图片已过期或已清理。' : error instanceof Error ? error.message : '预览暂时不可用。')
   })
   return () => { active = false; controller.abort(); if (url) URL.revokeObjectURL(url) }
 }, [nearViewport, record, api, display.canViewOutput])
 return <div className="relative rounded-xl">
   <div ref={cardRef} data-status={status} className="studio-source-card relative bg-white dark:bg-gray-900 rounded-xl border border-gray-200 dark:border-white/[0.08] overflow-hidden cursor-pointer hover:shadow-lg" role="button" tabIndex={0} aria-label="查看任务详情" onClick={onOpen} onKeyDown={event => { if (event.target !== event.currentTarget) return; if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); onOpen() } }}>
      <div className="flex h-40">
        {/* 左侧图片区域 */}
        <div
          data-has-image={hasPreviewImage ? 'true' : undefined}
          style={hasPreviewImage && aspectRatio ? { aspectRatio: String(aspectRatio) } : undefined}
          className="studio-card-preview w-40 min-w-[10rem] h-full bg-gray-100 dark:bg-black/20 relative flex items-center justify-center overflow-hidden flex-shrink-0"
        >
          {status === 'running' && (
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
          {(status === 'error' || isInterrupted || isExpired) && (
            <div className="flex flex-col items-center gap-1 px-2">
              <svg
                className={`w-7 h-7 ${isExpired ? 'studio-muted' : isInterrupted ? 'text-yellow-400' : 'text-red-400'}`}
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
              <span className={`text-xs text-center leading-tight ${isExpired ? 'studio-muted' : isInterrupted ? 'text-yellow-500' : 'text-red-400'}`}>
                {display.label}
              </span>
            </div>
          )}
          {hasPreviewImage && (
            <>
              <img
                src={thumbSrc}
                data-image-id={outputImages[0]}
                data-output-image-ids={outputImages.join(',')}
                className="saveable-image w-full h-auto block object-cover"
                loading="lazy"
                decoding="async"
                alt={record.item.prompt_preview || "生成图片"}
                onError={() => { setThumbSrc(''); setAspectRatio(null); setImageError('无法显示这张预览图。') }}
                onLoad={event => {
                  const { naturalWidth, naturalHeight } = event.currentTarget
                  if (naturalWidth > 0 && naturalHeight > 0) {
                    setAspectRatio(Math.min(16 / 9, Math.max(9 / 16, naturalWidth / naturalHeight)))
                  }
                }}
              />
              {(outputImages.length > 1) && (
                <span className="absolute bottom-1 right-1 bg-black/60 text-white text-xs px-1.5 py-0.5 rounded">
                  {outputImages.length}
                </span>
              )}
            </>
          )}
          {(status === 'done' || status === 'archived') && !hasPreviewImage && (
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
          <span className="studio-card-status absolute top-1.5 left-1.5 bg-black/60 text-white text-xs px-1.5 py-0.5 rounded">{display.label}</span>
        </div>

        {/* 右侧信息区域 */}
        <div className="studio-card-body flex-1 p-3 flex flex-col min-w-0">
          <div className="studio-card-caption flex-1 min-h-0 overflow-hidden">
            <p className="text-sm text-gray-700 dark:text-gray-300 leading-relaxed line-clamp-3">{record.item.prompt_preview || '(无提示词)'}</p>
            {display.message && <p className={`text-xs line-clamp-2 ${isExpired ? 'studio-muted' : 'text-red-400'}`}>{display.message}</p>}
            {display.canViewOutput && imageError && <p className="text-xs studio-muted" role="status">{imageError}</p>}
          </div>
          <div className="studio-card-footer mt-auto flex items-center gap-1.5">
            <span className="studio-card-model" title={badge.title}>
              <span className="studio-card-model-mark" data-platform={badge.key} aria-hidden="true">{badge.mark}</span>
              <span className="studio-card-model-name">{badge.label}</span>
            </span>
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
                className="studio-card-action rounded-md transition"
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
                className="studio-card-action rounded-md transition"
                disabled={!display.canViewOutput}
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
