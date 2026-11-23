import React, { useEffect, useRef, useState } from 'react'
import { errorMessage, outputErrorMessage, taskDisplay, taskPending, type StudioTask } from './api'
import type { StudioOriginal, workspaceAPI } from './workspaceAPI'
import { acceptsVideoImage } from '../video-studio/api'
import Lightbox from './upstream/components/Lightbox'
import { ChevronLeftIcon, ChevronRightIcon, CloseIcon, DownloadIcon, EditIcon, ExpandIcon } from './upstream/components/icons'
import { useCloseOnEscape } from './upstream/hooks/useCloseOnEscape'
import { usePreventBackgroundScroll } from './upstream/hooks/usePreventBackgroundScroll'
import { dismissAllTooltips } from './upstream/lib/tooltipDismiss'
import './detailDialog.css'

type API = ReturnType<typeof workspaceAPI>
type OriginalAction = (original: StudioOriginal) => Promise<void>
type OutputSize = { readonly width: number; readonly height: number }
type StatusTone = 'success' | 'running' | 'danger' | 'muted'
type ImageAssetProps = {
  readonly task: StudioTask
  readonly api: API
  readonly image?: Blob
  readonly index: number
  readonly measure: (size: OutputSize) => void
  readonly onReadable?: (index: number | null) => void
  readonly onFailed?: (index: number | null) => void
}
type DetailProps = {
  readonly task: StudioTask
  readonly api: API
  readonly close: () => void
  readonly edit: (index: number, image?: Blob) => void
  readonly cancel: (task: StudioTask) => Promise<void>
  readonly onVideo?: OriginalAction
  readonly onCanvas?: OriginalAction
}
const expiryFormat = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' })

function statusTone(status: string, pending: boolean, canViewOutput: boolean): StatusTone {
  if (status === 'failed') return 'danger'
  if (pending || ['pending', 'queued', 'running', 'processing'].includes(status)) return 'running'
  if (canViewOutput) return 'success'
  return 'muted'
}

function ImageAsset({ task, api, image, index, measure, onReadable, onFailed }: ImageAssetProps) {
  const [src, setSrc] = useState('')
  const [error, setError] = useState('')
  useEffect(() => {
    let active = true
    let url = ''
    setSrc(''); setError(''); onReadable?.(null); onFailed?.(null)
    const request = image ? Promise.resolve(image) : api.image(task, index)
    request.then(blob => {
      if (!active) return
      url = URL.createObjectURL(blob)
      setSrc(url)
      onFailed?.(null)
      if (!image && acceptsVideoImage(blob)) onReadable?.(index)
    }).catch(error => { if (active) { onFailed?.(index); setError(outputErrorMessage(error)) } })
    return () => { active = false; if (url) URL.revokeObjectURL(url) }
  }, [task.id, api, image, index, onReadable, onFailed])
  if (!src) {
    return <span role={error ? 'alert' : 'status'} className={error ? 'studio-detail-image-error' : 'studio-detail-loading'}>
      {error || '正在加载原图…'}
    </span>
  }
  return <img src={src} alt={task.item.prompt_preview || '生成图片'} onLoad={({ currentTarget }) => {
    const { naturalWidth: width, naturalHeight: height } = currentTarget
    if (width > 0 && height > 0) measure({ width, height })
  }} onError={() => { onReadable?.(null); onFailed?.(index); setSrc(''); setError('无法显示这张图片。') }} />
}

export function DetailModal({ task, api, close, edit, cancel, onVideo, onCanvas }: DetailProps) {
  const [archive, setArchive] = useState<Awaited<ReturnType<API['archivePreview']>> | null>(null)
  const [openingArchive, setOpeningArchive] = useState(false)
  const archiveRequest = useRef<AbortController | null>(null)
  const dialog = useRef<HTMLDialogElement>(null)
  const opener = useRef<HTMLElement | null>(null)
  const closed = useRef(false)
  const [index, setIndex] = useState(0)
  const [readableIndex, setReadableIndex] = useState<number | null>(null)
  const [failedIndex, setFailedIndex] = useState<number | null>(null)
  const [handoff, setHandoff] = useState<'video' | 'canvas' | null>(null)
  const handoffPending = useRef(false)
  const [sizes, setSizes] = useState<Readonly<Partial<Record<number, OutputSize>>>>({})
  const [viewer, setViewer] = useState('')
  const viewerURL = useRef('')
  const [error, setError] = useState('')
  const item = archive ? { ...archive, prompt_preview: task.item.prompt_preview } : task.item
  const displayedTask = { ...task, item }
  const display = taskDisplay(displayedTask)
  const pending = taskPending(task)
  const tone = statusTone(display.status, pending, display.canViewOutput)
  const decodedSize = sizes[index]
  const frameSize = decodedSize ?? (!archive && index === 0 ? task.item.preview_size : undefined)
  const ratio = frameSize ? frameSize.width / frameSize.height : 1
  const aspect = Number.isFinite(ratio) && ratio > 0 ? ratio : 1
  const layout = !display.canViewOutput ? 'record' : aspect < 1 ? 'portrait' : 'landscape'
  const frameStyle: React.CSSProperties & { readonly '--detail-aspect': number } = { '--detail-aspect': aspect }

  function hasExternalTopDialog() {
    return typeof document !== 'undefined' && Boolean(document.querySelector('dialog.cc-add-dialog[open]'))
  }

  function dismiss() {
    if (hasExternalTopDialog()) return
    closed.current = true
    if (dialog.current?.open) dialog.current.close()
    close()
    const target = opener.current
    if (target?.isConnected && document.activeElement !== target) target.focus()
  }
  useCloseOnEscape(true, dismiss)
  usePreventBackgroundScroll(true, dialog)
  useEffect(() => {
    opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    dialog.current?.showModal()
  }, [])
  useEffect(() => {
    closed.current = false
    return () => { closed.current = true; archiveRequest.current?.abort() }
  }, [])
  useEffect(() => () => { if (viewerURL.current) URL.revokeObjectURL(viewerURL.current) }, [])

  // Opened archive items are synthesized from a signed archive, not indexed originals.
  const videoOriginal = () => onVideo && !archive && readableIndex === index && failedIndex !== index ? api.originalLocator(task, index) : null
  const canvasOriginal = () => onCanvas && !archive && failedIndex !== index ? api.originalLocator(task, index) : null

  async function openVideo() {
    if (handoffPending.current) return
    const original = videoOriginal()
    if (!original || !onVideo) { setError('原图不可用，请重新选择。'); return }
    handoffPending.current = true
    setHandoff('video')
    setError('')
    try {
      await onVideo(original)
    } catch (error) {
      if (!closed.current && dialog.current?.isConnected) setError(errorMessage(error))
    } finally {
      handoffPending.current = false
      if (!closed.current && dialog.current?.isConnected) setHandoff(null)
    }
  }

  async function openCanvas() {
    if (handoffPending.current) return
    const original = canvasOriginal()
    if (!original || !onCanvas) { setError('原图不可用或已过期，无法加入画布。'); return }
    handoffPending.current = true
    setHandoff('canvas')
    setError('')
    try {
      await onCanvas(original)
    } catch (error) {
      if (!closed.current && dialog.current?.isConnected) setError(errorMessage(error))
    } finally {
      handoffPending.current = false
      if (!closed.current && dialog.current?.isConnected) setHandoff(null)
    }
  }

  async function openArchive() {
    if (archiveRequest.current) return
    if (!taskDisplay(task).canOpenArchive) { setError('图片已过期或已清理。'); return }
    const controller = new AbortController()
    archiveRequest.current = controller
    setOpeningArchive(true); setError('')
    try {
      const result = await api.archivePreview(task, controller.signal)
      if (!controller.signal.aborted) {
        if (taskDisplay(task).canOpenArchive) setArchive(result)
        else setError('图片已过期或已清理。')
      }
    } catch (error) {
      if (!controller.signal.aborted) setError(outputErrorMessage(error))
    } finally {
      if (!controller.signal.aborted) {
        archiveRequest.current = null
        setOpeningArchive(false)
      }
    }
  }
  async function imageAt(next: number) {
    if (!taskDisplay(displayedTask).canViewOutput) throw new Error('图片已过期或已清理。')
    return archive ? archive.images[next] : api.image(task, next)
  }
  function editCurrent() {
    if (taskDisplay(displayedTask).canViewOutput && failedIndex !== index) edit(index, archive?.images[index])
    else setError('当前这张原图不可用或已清理，请切换其他输出或刷新重试。')
  }
  async function view(next = index) {
    try {
      const blob = await imageAt(next)
      if (!dialog.current?.isConnected) return
      const url = URL.createObjectURL(blob)
      if (viewerURL.current) URL.revokeObjectURL(viewerURL.current)
      viewerURL.current = url
      setIndex(next); setViewer(url); dismissAllTooltips()
    } catch (error) { setError(outputErrorMessage(error)) }
  }
  async function download() {
    try {
      const blob = await imageAt(index)
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = `${task.job.id}-${item.custom_id}-${index}.${item.file_extension || 'png'}`
      link.click()
      setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch (error) { setError(outputErrorMessage(error)) }
  }

  const expiry = (display.canViewOutput || display.canOpenArchive) ? task.job.output_expires_at : undefined
  const noticeMessage = display.message || (display.canOpenArchive ? '图片或失败详情保存在归档中，打开后按需读取。' : '')
  const archiveVideoHint = onVideo && taskDisplay(task).canOpenArchive ? '归档结果不支持直接带入视频工作台；可下载原图后手动上传。' : ''
  const showCallout = Boolean(noticeMessage || archiveVideoHint || display.error?.code)
  const calloutTone = display.status === 'failed' ? 'danger' : 'neutral'
  const showPagination = display.canViewOutput && item.image_count > 1
  const canVideo = Boolean(videoOriginal())
  const canCanvas = Boolean(canvasOriginal())
  const hasActions = display.canViewOutput || display.canOpenArchive || pending

  return <dialog
    ref={dialog}
    className="studio-detail"
    aria-label="任务详情"
    data-layout={layout}
    data-status={display.status}
    style={frameStyle}
    onClick={event => { if (!viewer && !hasExternalTopDialog() && event.target === event.currentTarget) dismiss() }}
    onCancel={event => {
      event.preventDefault()
      if (hasExternalTopDialog()) return
      if (viewer) setViewer('')
      else dismiss()
    }}
    onKeyDown={event => {
      if (viewer || hasExternalTopDialog() || !showPagination) return
      if (event.ctrlKey || event.metaKey || event.altKey) return
      const target = event.target instanceof HTMLElement ? event.target : null
      if (target && ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)) return
      if (event.key === 'ArrowLeft' && index > 0) {
        event.preventDefault()
        setIndex(index - 1)
      } else if (event.key === 'ArrowRight' && index + 1 < item.image_count) {
        event.preventDefault()
        setIndex(index + 1)
      }
    }}
  >
    <header className="studio-detail-header">
      <div className="studio-detail-title">
        <h2>任务详情</h2>
        <span className="studio-detail-badge" data-tone={tone}>{display.label}</span>
      </div>
      <button type="button" className="studio-detail-icon studio-detail-close" onClick={dismiss} aria-label="关闭" title="关闭"><CloseIcon aria-hidden="true" /></button>
    </header>
    <div className="studio-detail-content">
      {display.canViewOutput && <figure className="studio-detail-media">
        <button type="button" className="studio-detail-image" onClick={() => void view()} aria-label="放大图片">
          <ImageAsset key={index} task={task} api={api} image={archive?.images[index]} index={index} measure={next => setSizes(sizes => ({ ...sizes, [index]: next }))} onReadable={setReadableIndex} onFailed={setFailedIndex} />
          <span className="studio-detail-expand" aria-hidden="true"><ExpandIcon /></span>
        </button>
      </figure>}
      <section className="studio-detail-info" aria-label="生成信息">
        {showCallout && <div className="studio-detail-callout" data-tone={calloutTone} role={calloutTone === 'danger' ? 'status' : undefined}>
          {noticeMessage && <p className="studio-detail-message">{noticeMessage}</p>}
          {archiveVideoHint && <p className="studio-detail-message">{archiveVideoHint}</p>}
          {display.error?.code && <details className="studio-detail-error">
            <summary>错误详情</summary>
            <p className="studio-detail-error-body">
              <code>{display.error.code}</code>
              {display.error.message && display.error.message !== display.message && <> · {display.error.message}</>}
            </p>
          </details>}
        </div>}
        <div className="studio-detail-prompt-block">
          <h3>提示词预览</h3>
          <p className={`studio-detail-prompt${task.item.prompt_preview ? '' : ' is-empty'}`}>{task.item.prompt_preview || '未记录提示词'}</p>
        </div>
        <dl className="studio-detail-facts">
          <div className="studio-detail-fact-model"><dt>模型</dt><dd>{task.job.model}</dd></div>
          {task.job.image_size && <div><dt>分辨率档位</dt><dd>{task.job.image_size}</dd></div>}
          {decodedSize && <div><dt>实际像素</dt><dd>{`${decodedSize.width} × ${decodedSize.height}`}</dd></div>}
          <div><dt>实际消耗</dt><dd>{task.job.actual_cost ?? '待结算'}</dd></div>
          {task.job.hold_amount != null && <div><dt>预扣</dt><dd>{task.job.hold_amount}</dd></div>}
          {expiry != null && <div><dt>{display.canOpenArchive ? '归档可查看至' : '原图可下载至'}</dt><dd>{expiryFormat.format(expiry * 1000)}<span className="studio-detail-tz">（本地时间）</span></dd></div>}
        </dl>
        {error && <p className="studio-detail-alert" role="alert">{error}</p>}
      </section>
    </div>
    {hasActions && <footer className="studio-detail-actions">
      {showPagination && <div className="studio-detail-pagination" role="group" aria-label="切换图片">
        <button type="button" className="studio-detail-icon studio-detail-step" aria-label="上一张" title="上一张" disabled={index === 0} onClick={() => setIndex(index - 1)}><ChevronLeftIcon aria-hidden="true" /></button>
        <span aria-live="polite">{index + 1} / {item.image_count}</span>
        <button type="button" className="studio-detail-icon studio-detail-step" aria-label="下一张" title="下一张" disabled={index + 1 >= item.image_count} onClick={() => setIndex(index + 1)}><ChevronRightIcon aria-hidden="true" /></button>
      </div>}
      {showPagination && <span className="studio-detail-divider" aria-hidden="true" />}
      {pending && <button type="button" className="studio-detail-action-danger" onClick={() => { void cancel(task).then(dismiss).catch(error => setError(errorMessage(error))) }}>取消此批次</button>}
      {display.canOpenArchive && <button type="button" className="studio-detail-action-soft" disabled={openingArchive} onClick={() => void openArchive()}>{openingArchive ? '正在读取归档…' : '打开归档结果'}</button>}
      {display.canViewOutput && <>
        <button type="button" className="studio-detail-action-quiet" onClick={() => void download()}><DownloadIcon aria-hidden="true" />下载图片</button>
        <button type="button" className="studio-detail-action-soft" onClick={editCurrent}><EditIcon aria-hidden="true" />引用此图继续修改</button>
        {canCanvas && <button type="button" className="studio-detail-action-soft" disabled={handoff !== null} onClick={() => void openCanvas()}>{handoff === 'canvas' ? '正在加入画布…' : '加入画布'}</button>}
        {canVideo && <button type="button" className="studio-detail-action-soft" disabled={handoff !== null} onClick={() => void openVideo()}>{handoff === 'video' ? '正在打开视频工作台…' : '用这张图生成视频'}</button>}
      </>}
    </footer>}
    {display.canViewOutput && viewer && <Lightbox src={viewer} imageId={`${task.id}/${index}`} onClose={() => setViewer('')} currentIndex={index} total={item.image_count} showNav={item.image_count > 1} onPrev={() => void view((index - 1 + item.image_count) % item.image_count)} onNext={() => void view((index + 1) % item.image_count)} onDownload={() => void download()} onEdit={editCurrent} />}
  </dialog>
}
