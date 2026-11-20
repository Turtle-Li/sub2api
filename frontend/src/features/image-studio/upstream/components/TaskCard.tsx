import React from 'react'
// Adapted from 88lin/gpt-image-studio 5523a592, MIT; see UPSTREAM.md.
import { useCallback, useEffect, useMemo, useState, useRef, useSyncExternalStore, type ReactNode } from 'react'
import { isImageOutputGone, taskDisplay, type StudioTask } from '../../api'
import type { ThumbnailLoader, ThumbnailPriority } from '../../thumbnailLoader'
import { useThumbnailPriority } from '../hooks/useNearViewport'
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

function galleryCoverAspect(width: number, height: number): number | undefined {
 const ratio = width / height
 return width > 0 && height > 0 && Number.isFinite(ratio) ? Math.max(9 / 16, Math.min(16 / 9, ratio)) : undefined
}

function useTaskThumbnail(record: StudioTask, previews: ThumbnailLoader, enabled: boolean, priority: ThumbnailPriority) {
 type Request = { priority: ThumbnailPriority; request: AbortController | null; failedOn: StudioTask | null }
 const slot = useMemo<Request>(() => ({ priority: 0, request: null, failedOn: null }), [previews, record.id, enabled])
 const [failure, setFailure] = useState<{ slot: Request; message: string } | null>(null)
 const subscribe = useCallback((listener: () => void) => previews.subscribe(record.id, listener), [previews, record.id])
 const read = useCallback(() => enabled ? previews.url(record.id) : '', [enabled, previews, record.id])
 const url = useSyncExternalStore(subscribe, read, () => '')

 useEffect(() => () => { slot.request?.abort(); slot.request = null }, [slot])
 useEffect(() => {
   slot.priority = priority
   if (!enabled) { previews.remove(record.id); return }
   if (!priority) return
   if (url) { previews.touch(record.id); return }
   if (slot.request || slot.failedOn === record) return
   const request = new AbortController()
   slot.request = request
   setFailure(null)
   void previews.load(record, request.signal, () => slot.priority).catch(error => {
     if (request.signal.aborted || (error instanceof DOMException && error.name === 'AbortError')) return
     slot.failedOn = record
     const message = isImageOutputGone(error) ? '图片已过期或已清理。' : error instanceof Error ? error.message : '预览暂时不可用。'
     setFailure({ slot, message })
   }).finally(() => { if (slot.request === request) slot.request = null })
 }, [enabled, previews, priority, record, slot, url])

 const onDecodeError = () => {
   if (!url || previews.url(record.id) !== url) return
   slot.failedOn = record
   setFailure({ slot, message: '无法显示这张预览图。' })
   previews.remove(record.id)
 }
 return { url, error: !url && failure?.slot === slot ? failure.message : '', onDecodeError }
}

export default function TaskCard({ record, previews, measureMissingPreview = false, onOpen, onEditOutputs }: {
 readonly record: StudioTask
 readonly previews: ThumbnailLoader
 readonly measureMissingPreview?: boolean
 readonly onOpen: () => void
 readonly onEditOutputs: () => void
}) {
 const cardRef = useRef<HTMLDivElement>(null)
 const priority = useThumbnailPriority(cardRef)
 const display = taskDisplay(record)
 const { url: thumbSrc, error: imageError, onDecodeError } = useTaskThumbnail(record, previews, display.canViewOutput, priority)
 const hasCoverOrNear = Boolean(thumbSrc || priority)
 const expiresAt = record.job.output_expires_at
 const [, setExpiredCheck] = useState(0)
 useEffect(() => {
   if (!hasCoverOrNear || !display.canViewOutput || expiresAt == null) return
   let timer: ReturnType<typeof setTimeout> | undefined
   function schedule(expiry: number) {
     const remaining = expiry * 1000 - Date.now()
     if (remaining <= 0) setExpiredCheck(current => current + 1)
     else timer = setTimeout(() => schedule(expiry), Math.min(remaining, 2147483647))
   }
   schedule(expiresAt)
   return () => clearTimeout(timer)
 }, [hasCoverOrNear, display.canViewOutput, expiresAt])
 const [decodedAspect, setDecodedAspect] = useState<number>()
 const previewSize = record.item.preview_size
 const metadataAspect = previewSize ? galleryCoverAspect(previewSize.width, previewSize.height) : undefined
 // Legacy gallery cards keep their fallback until a metadata refresh, not a thumbnail decode.
 const aspectRatio = metadataAspect ?? (measureMissingPreview ? decodedAspect : undefined)
 const isExpired = display.status === 'output_deleted'
 const isInterrupted = display.status === 'cancelled'
 const status = isExpired ? 'expired' : isInterrupted ? 'cancelled' : display.status === 'failed' ? 'error' : display.canOpenArchive ? 'archived' : ['succeeded', 'success', 'completed'].includes(display.status) ? 'done' : 'running'
 const badge = modelBadge(record.job.provider, record.job.model)
 const hasPreviewImage = display.canViewOutput && Boolean(thumbSrc)
 const outputImages = Array.from({ length: record.item.image_count }, (_, i) => `${record.id}/${i}`)
 return <div className="relative rounded-xl">
   <div ref={cardRef} data-status={status} className="studio-source-card relative bg-white dark:bg-gray-900 rounded-xl border border-gray-200 dark:border-white/[0.08] overflow-hidden cursor-pointer hover:shadow-lg" role="button" tabIndex={0} aria-label="查看任务详情" onClick={onOpen} onKeyDown={event => { if (event.target !== event.currentTarget) return; if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); onOpen() } }}>
      <div className="flex h-40">
        {/* 左侧图片区域 */}
        <div
          style={aspectRatio === undefined ? undefined : { aspectRatio }}
          data-size-missing={aspectRatio === undefined || undefined}
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
                decoding="async"
                alt={record.item.prompt_preview || "生成图片"}
                onError={onDecodeError}
                onLoad={measureMissingPreview && metadataAspect === undefined ? event => {
                  const { naturalWidth, naturalHeight } = event.currentTarget
                  setDecodedAspect(galleryCoverAspect(naturalWidth, naturalHeight))
                } : undefined}
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
