import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { BatchImageModel, BatchImageSubmitRequest } from '@/api/batchImage'
import { isImageOutputGone, listKeys, readImage, studioWalletRoute, taskDisplay, taskPending, taskRoot, type GalleryFilters, type StudioTask, type StudioKey, type StudioWalletMode } from './api'
import { modelIdentity, workspaceAPI, type StudioOriginal } from './workspaceAPI'
import { acceptsVideoImage } from '../video-studio/api'
import { cachedStudioKeys, cachedStudioWalletMode, cacheStudioKeys } from './cache'
import { useCloseOnEscape } from './upstream/hooks/useCloseOnEscape'
import { usePreventBackgroundScroll } from './upstream/hooks/usePreventBackgroundScroll'
import TaskCard from './upstream/components/TaskCard'
import TaskGrid from './upstream/components/TaskGrid'
import Lightbox from './upstream/components/Lightbox'
import InputBar from './upstream/components/InputBar'
import { useGlobalClickSuppression } from './upstream/lib/clickSuppression'
import { dismissAllTooltips } from './upstream/lib/tooltipDismiss'
import { useEditContinuation } from './useEditContinuation'
import { useGalleryHistory } from './useGalleryHistory'
import ExpiredGallery from './ExpiredGallery'
import GalleryTail from './GalleryTail'
import './studio.css'

type API = ReturnType<typeof workspaceAPI>
type VideoAction = (original: StudioOriginal) => Promise<void>
interface StudioProps { readonly userId: number; readonly onVideo?: VideoAction }
const galleryFilters = [['all', '全部'], ['pending', '生成中'], ['completed', '可查看'], ['failed', '失败'], ['expired', '已过期']] as const
type GalleryFilter = (typeof galleryFilters)[number][0]
function errorMessage(error: unknown): string { return error instanceof Error ? error.message : typeof error === 'object' && error !== null && 'message' in error ? String(error.message) : '请求失败，请刷新后重试' }
const outputErrorMessage = (error: unknown) => isImageOutputGone(error) ? '图片已过期或已清理。' : errorMessage(error)
function defaultStudioModel(models: readonly BatchImageModel[]) {
  const preferred = models.find(model => modelIdentity(model) === 'openai_images:gpt-image-2.6-sunburst')
    ?? models.find(model => modelIdentity(model) === 'openai_images:gpt-image-2.6-flare')
    ?? models[0]
  return preferred ? modelIdentity(preferred) : ''
}
function ImageAsset({ task, api, image, index = 0, onReadable }: { task: StudioTask; api: API; image?: Blob; index?: number; onReadable?: (index: number | null) => void }) {
  const [src, setSrc] = useState('')
  const [error, setError] = useState('')
  useEffect(() => {
    let active = true; let url = ''
    setSrc(''); setError(''); onReadable?.(null)
    const request = image ? Promise.resolve(image) : api.image(task, index)
    request.then(blob => {
      if (!active) return
      url = URL.createObjectURL(blob); setSrc(url)
      if (!image && acceptsVideoImage(blob)) onReadable?.(index)
    }).catch(error => { if (active) setError(outputErrorMessage(error)) })
    return () => { active = false; if (url) URL.revokeObjectURL(url) }
  }, [task.id, api, image, index, onReadable])
  return src ? <img src={src} alt={task.item.prompt_preview || '生成图片'} onError={() => { onReadable?.(null); setSrc(''); setError('无法显示这张图片。') }} /> : <span className="studio-muted">{error || '图片加载中…'}</span>
}

// Upstream DetailModal/Lightbox lifecycle hooks are retained verbatim, including nested ESC ownership.
function DetailModal({ task, api, close, edit, cancel, onVideo }: { onVideo?: VideoAction; task: StudioTask; api: API; close: () => void; edit: (index: number, image?: Blob) => void; cancel: (task: StudioTask) => Promise<void> }) {
  const [archive, setArchive] = useState<Awaited<ReturnType<API['archivePreview']>> | null>(null)
  const [openingArchive, setOpeningArchive] = useState(false)
  const archiveRequest = useRef<AbortController | null>(null)
  useEffect(() => () => archiveRequest.current?.abort(), [])
  const item = archive ? { ...archive, prompt_preview: task.item.prompt_preview } : task.item
  const displayedTask = { ...task, item }
  const display = taskDisplay(displayedTask)
  const dialog = useRef<HTMLDialogElement>(null)
  const [index, setIndex] = useState(0)
  const [readableIndex, setReadableIndex] = useState<number | null>(null)
  // Opened archive items are synthesized from a signed archive, not indexed originals.
  const videoOriginal = () => onVideo && !archive && readableIndex === index ? api.originalLocator(task, index) : null
  async function openVideo() {
    const original = videoOriginal()
    if (!original || !onVideo) { setError('原图不可用，请重新选择。'); return }
    try { await onVideo(original) } catch (error) { if (dialog.current?.isConnected) setError(errorMessage(error)) }
  }
  const [viewer, setViewer] = useState('')
  const viewerURL = useRef('')
  useEffect(() => () => { if (viewerURL.current) URL.revokeObjectURL(viewerURL.current) }, [])
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
    if (taskDisplay(displayedTask).canViewOutput) edit(index, archive?.images[index])
    else setError('图片已过期或已清理。')
  }
  async function view(next = index) {
    try { const blob = await imageAt(next); if (!dialog.current?.isConnected) return; const url = URL.createObjectURL(blob); if (viewerURL.current) URL.revokeObjectURL(viewerURL.current); viewerURL.current = url; setIndex(next); setViewer(url); dismissAllTooltips() } catch (error) { setError(outputErrorMessage(error)) }
  }
  const [error, setError] = useState('')
  useCloseOnEscape(true, close)
  usePreventBackgroundScroll(true, dialog)
  useEffect(() => { dialog.current?.showModal() }, [])
  async function download() {
    try {
      const blob = await imageAt(index); const url = URL.createObjectURL(blob)
      const a = document.createElement('a'); a.href = url; a.download = `${task.job.id}-${item.custom_id}-${index}.${item.file_extension || 'png'}`; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch (error) { setError(outputErrorMessage(error)) }
  }
  return <dialog ref={dialog} className="studio-detail" aria-label="任务详情" onCancel={event => { event.preventDefault(); close() }}>
    <div className="studio-actions"><strong>任务详情</strong><button onClick={close}>关闭</button></div>
    {display.canViewOutput && <button className="studio-detail-image" onClick={() => void view()} aria-label="放大图片"><ImageAsset task={task} api={api} image={archive?.images[index]} index={index} onReadable={setReadableIndex} /></button>}
    {display.canViewOutput && item.image_count > 1 && <div className="studio-actions"><button disabled={index === 0} onClick={() => setIndex(index - 1)}>上一张</button><span>{index + 1} / {item.image_count}</span><button disabled={index + 1 >= item.image_count} onClick={() => setIndex(index + 1)}>下一张</button></div>}
    <p className="studio-detail-state">{task.job.model} · {display.label}</p>
    {display.message && <p className="studio-muted">{display.message}</p>}
    {display.error?.code && <details className="studio-detail-error"><summary>错误详情</summary><p>{display.error.code}{display.error.message && display.error.message !== display.message && ` · ${display.error.message}`}</p></details>}
    <p className="studio-prompt">{task.item.prompt_preview}</p><p className="studio-muted">提示词预览可能被截断。重新生成前请核对完整内容。</p>
    {(display.canViewOutput || display.canOpenArchive) && task.job.output_expires_at != null && <p className="studio-muted">{display.canOpenArchive ? '归档结果可查看至' : '原图可下载至'} {new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(task.job.output_expires_at * 1000)}（本地时间）</p>}
    {taskPending(task) && <button onClick={() => { void cancel(task).then(close).catch(error => setError(errorMessage(error))) }}>取消此批次</button>}<p>预扣 {task.job.hold_amount} · 实际消耗 {task.job.actual_cost ?? '待结算'}</p>
    {error && <p role="alert">{error}</p>}
    {display.canOpenArchive && <p className="studio-muted">此批次的图片或失败详情保存在归档中，打开后按需读取。</p>}
    {onVideo && taskDisplay(task).canOpenArchive && <p className="studio-muted">归档结果不支持直接带入视频工作台；可下载原图后手动上传。</p>}
    <div className="studio-actions">{display.canOpenArchive ? <button disabled={openingArchive} onClick={() => void openArchive()}>{openingArchive ? '正在读取归档…' : '打开归档结果'}</button> : <><button disabled={!display.canViewOutput} onClick={() => void download()}>下载图片</button><button disabled={!display.canViewOutput} onClick={editCurrent}>引用此图继续修改</button>{videoOriginal() && <button onClick={() => void openVideo()}>用这张图生成视频</button>}</>}</div>
    {display.canViewOutput && viewer && <Lightbox src={viewer} imageId={`${task.id}/${index}`} onClose={() => setViewer('')} currentIndex={index} total={item.image_count} showNav={item.image_count > 1} onPrev={() => void view((index - 1 + item.image_count) % item.image_count)} onNext={() => void view((index + 1) % item.image_count)} onDownload={() => void download()} onEdit={editCurrent} />}
  </dialog>
}

export function Studio(props: StudioProps) {
  return <StudioSession key={props.userId} {...props} />
}

function StudioSession({ userId, onVideo }: StudioProps) {
  useGlobalClickSuppression()
  const [directory, setDirectory] = useState<{ keys: StudioKey[] | null; wallet: StudioWalletMode; current: boolean }>(() => ({ keys: cachedStudioKeys(userId) ?? null, wallet: cachedStudioWalletMode(userId), current: false }))
  const [error, setError] = useState('')
  useEffect(() => {
    const controller = new AbortController()
    let wallet: StudioWalletMode = 'legacy'
    listKeys(controller.signal, mode => { wallet = mode }).then(items => {
      if (controller.signal.aborted) return
      const eligible = items.filter(item => item.status === 'active' && !item.expired && !item.quota_exhausted && (item.can_generate !== false || item.has_history !== false))
      cacheStudioKeys(userId, eligible, wallet); setDirectory({ keys: eligible, wallet, current: true })
    }).catch(error => { if (!controller.signal.aborted) setError(errorMessage(error)) })
    return () => controller.abort()
  }, [userId])
  const { keys, wallet, current } = directory
  return <div className="image-studio">
    {error && <p className="studio-error" role="alert">{error}</p>}
    {keys && (keys.length || wallet !== 'legacy')
      ? <Workspace key={`${userId}:${wallet}:${keys.map(key => `${key.id}:${key.group_id}:${key.can_generate}:${key.has_history}`).join(',')}`} keys={keys} wallet={wallet} directoryCurrent={current} userId={userId} onVideo={onVideo} />
      : <div className="studio-empty" role="status">{keys === null && !error ? '正在读取可用模型…' : <p>暂无可用的生图权限，请联系管理员。</p>}</div>}
  </div>
}

function Workspace({ keys, wallet, directoryCurrent, userId, onVideo }: StudioProps & { readonly keys: readonly StudioKey[]; readonly wallet: StudioWalletMode; readonly directoryCurrent: boolean }) {
  const [api] = useState(() => {
    const controller = new AbortController()
    const walletRoutes = wallet === 'legacy' ? [] : [studioWalletRoute]
    const routes = [...walletRoutes, ...keys.map(key => key.id)]
    const modelKeys = wallet === 'on' ? walletRoutes : keys.filter(key => key.can_generate !== false).map(key => key.id)
    const historyKeys = [...walletRoutes, ...keys.filter(key => key.has_history !== false).map(key => key.id)]
    return { client: workspaceAPI(routes, controller.signal, { modelKeys, historyKeys, userId }), controller }
  })
  const composer = useRef<HTMLDialogElement>(null)
  const [mobile, setMobile] = useState(() => Boolean(window.matchMedia?.('(max-width: 760px)').matches))
  const [batchMode, setBatchMode] = useState(false)
  const [filter, setFilter] = useState<GalleryFilter>('all')
  const [notice, setNotice] = useState('')
  useEffect(() => {
    const query = window.matchMedia?.('(max-width: 760px)')
    if (!query) return
    const update = () => { composer.current?.close(); setMobile(query.matches) }
    query.addEventListener('change', update)
    return () => query.removeEventListener('change', update)
  }, [])
  const [models, setModels] = useState<BatchImageModel[]>(api.client.cachedModels)
  const [model, setModel] = useState(() => defaultStudioModel(api.client.cachedModels()))
  const manuallySelectedModel = useRef(false)
  const [modelsUpdating, setModelsUpdating] = useState(true)
  const [catalogueRefresh, setCatalogueRefresh] = useState(0)
  const lastCatalogueCheck = useRef(0)
  const [refs, setRefs] = useState<string[]>([])
  const [imageSize, setImageSize] = useState('')
  const [aspectRatio, setAspectRatio] = useState('')
  const capabilities = models.find(item => modelIdentity(item) === model)?.capabilities
  const selectedSize = capabilities?.image_sizes.includes(imageSize) ? imageSize : capabilities?.image_sizes.includes(capabilities.default_image_size) ? capabilities.default_image_size : capabilities?.image_sizes[0]
  const selectedAspect = capabilities?.aspect_ratios.includes(aspectRatio) ? aspectRatio : capabilities?.aspect_ratios.includes(capabilities.default_aspect_ratio) ? capabilities.default_aspect_ratio : capabilities?.aspect_ratios[0]
  useEffect(() => { setImageSize(selectedSize || ''); setAspectRatio(selectedAspect || '') }, [selectedSize, selectedAspect])
  const referenceError = capabilities && refs.length > capabilities.max_reference_images ? `当前模型最多支持 ${capabilities.max_reference_images} 张参考图，请移除多余图片后生成。` : ''
  const [prompt, setPrompt] = useState('')
  const [parent, setParent] = useState<StudioTask | null>(null)
  const rootBatch = parent ? taskRoot(parent) : ''
  const [detail, setDetail] = useState<StudioTask | null>(null)
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  const [searchOpen, setSearchOpen] = useState(false)
  const searchInput = useRef<HTMLInputElement>(null)
  useEffect(() => { if (searchOpen) searchInput.current?.focus() }, [searchOpen])
  useEffect(() => { const timer = setTimeout(() => setQuery(search), 250); return () => clearTimeout(timer) }, [search])
  const [error, setError] = useState('')
  const [refresh, setRefresh] = useState(0)
  const expiredHistory = filter === 'expired'
  const filterOptions = parent ? galleryFilters.filter(([value]) => value !== 'expired') : galleryFilters
  const filters = useMemo<GalleryFilters>(() => ({ status: filter === 'all' && !rootBatch ? 'current' : filter, search: query, root: rootBatch }), [filter, query, rootBatch])
  const onPollError = useCallback((error: unknown) => setError(errorMessage(error)), [])
  const history = useGalleryHistory(api.client, api.controller.signal, filters, refresh, onPollError)
  const { tasks } = history
  const [busy, setBusy] = useState(false)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  const continuation = useEditContinuation({
    tasks, signal: api.controller.signal,
    load: async task => readImage(await api.client.image(task, 0)),
    onAdvance: (image, task) => { setRefs([image]); setParent(task) },
    onError: error => setError(`新图片已完成，但自动引用失败：${errorMessage(error)}。请从任务中重新选择参考图。`),
  })
  useEffect(() => {
    let active = true
    setModelsUpdating(true)
    lastCatalogueCheck.current = Date.now()
    api.client.refreshDirectory()
    const update = (catalog: BatchImageModel[]) => {
      if (!active) return
      setModels(previous => JSON.stringify(previous) === JSON.stringify(catalog) ? previous : catalog)
      setModel(previous => manuallySelectedModel.current && catalog.some(item => modelIdentity(item) === previous) ? previous : defaultStudioModel(catalog))
    }
    void api.client.models(update).then(catalog => {
      update(catalog)
      const warning = api.client.warnings().join(' ')
      if (active && warning) setError(warning)
    }).catch(error => { if (active) setError(errorMessage(error)) }).finally(() => { if (active) setModelsUpdating(false) })
    return () => { active = false }
  }, [api, refresh, catalogueRefresh])
  useEffect(() => {
    const revalidate = () => {
      if (document.visibilityState === 'visible' && Date.now() - lastCatalogueCheck.current >= 60000) {
        lastCatalogueCheck.current = Date.now(); setCatalogueRefresh(value => value + 1)
      }
    }
    window.addEventListener('focus', revalidate)
    document.addEventListener('visibilitychange', revalidate)
    return () => { window.removeEventListener('focus', revalidate); document.removeEventListener('visibilitychange', revalidate) }
  }, [])
  useEffect(() => () => api.controller.abort(), [api])
  const refreshGallery = () => { setError(''); setRefresh(value => value + 1) }
  async function cancel(task: StudioTask) {
    const job = await api.client.cancel(task)
    if (api.controller.signal.aborted) return
    // The returned batch is authoritative; its items may intentionally remain pending.
    history.updateJob(job)
  }
  async function edit(task: StudioTask, index = 0, archiveImage?: Blob) {
    const display = taskDisplay(task)
    if (!display.canViewOutput && !(display.canOpenArchive && archiveImage)) return
    const selection = continuation.beginSelection()
    try {
      const data = await readImage(archiveImage ?? await api.client.image(task, index))
      const sourceModel = models.find(item => item.id === task.job.model && item.provider === task.job.provider)
      const linked = sourceModel ? await api.client.canContinue(task, sourceModel) : false
      if (!continuation.isCurrent(selection)) return
      setRefs([data]); setParent(linked ? task : null); setPrompt(''); setBatchMode(false); setDetail(null); setFilter('all'); setSearch('')
      if (sourceModel) { manuallySelectedModel.current = true; setModel(modelIdentity(sourceModel)) }
      setNotice(linked ? '' : '原模型暂不可用，已保留参考图，可选择模型创建新作品。')
      if (window.matchMedia?.('(max-width: 760px)').matches) composer.current?.showModal()
    } catch (error) { setError(outputErrorMessage(error)) }
  }
  async function changeModel(value: string) {
    const selection = continuation.beginSelection()
    manuallySelectedModel.current = true
    setModel(value)
    const selected = models.find(item => modelIdentity(item) === value)
    if (parent && selected && !await api.client.canContinue(parent, selected) && continuation.isCurrent(selection)) {
      setParent(null); setNotice('已切换模型并保留参考图。本次将创建新作品。')
    }
  }
  async function upload(files: FileList | null) {
    if (!files?.length) return
    if (capabilities && refs.length + files.length > capabilities.max_reference_images) { setError(`当前模型最多支持 ${capabilities.max_reference_images} 张参考图，请减少选择或先移除已有图片。`); return }
    const selection = continuation.beginSelection()
    try { const picked = Array.from(files); if (picked.some(file => !['image/png', 'image/jpeg', 'image/webp'].includes(file.type) || file.size > 8 * 1024 * 1024)) throw new Error('请选择 8 MB 以内的 PNG、JPEG 或 WebP 图片'); const images = await Promise.all(picked.map(file => readImage(file, api.controller.signal))); if (continuation.isCurrent(selection)) setRefs(previous => [...previous, ...images]) } catch (error) { setError(errorMessage(error)) }
  }
  async function submit() {
    const selected = models.find(item => modelIdentity(item) === model)
    if (!directoryCurrent || !selected || !prompt.trim() || busy || referenceError) return
    const prompts = parent || !batchMode ? [prompt.trim()] : prompt.split(/\n\s*---\s*\n/).map(value => value.trim()).filter(Boolean)
    const body: BatchImageSubmitRequest = { model: selected.id, ...(selectedSize ? { image_size: selectedSize } : {}), ...(selectedAspect ? { aspect_ratio: selectedAspect } : {}), ...(capabilities?.response_mime_types[0] ? { response_mime_type: capabilities.response_mime_types[0] } : {}), provider: selected.provider, parent_batch_id: parent ? taskRoot(parent) : undefined, items: prompts.map((text, i) => ({ custom_id: `image-${i + 1}`, prompt: text, output_count: 1, reference_images: refs.map(data => ({ mime_type: data.slice(5, data.indexOf(';')), data: data.slice(data.indexOf(',') + 1) })) })), metadata: parent ? { source_batch_id: parent.job.id, source_item_id: parent.item.custom_id } : {} }
    const serialized = JSON.stringify(body)
    if (attempt.current?.body !== serialized) attempt.current = { body: serialized, key: crypto.randomUUID() }
    const selection = continuation.beginSelection()
    setBusy(true); setError('')
    try {
      const submitted = await api.client.submit(body, attempt.current.key)
      if (api.controller.signal.aborted) return
      if (parent) continuation.expectOutput(submitted.id, selection)
      attempt.current = null
      setPrompt(current => current === prompt ? '' : current)
      if (mobile) composer.current?.close()
      setNotice(`已提交 ${submitted.item_count || prompts.length} 张图片任务，生成结果会自动更新。`)
      setRefresh(value => value + 1)
    }
    catch (error) { if (!api.controller.signal.aborted) setError(`${errorMessage(error)}。请求结果未确认时，保持内容重试会复用同一提交编号。`) }
    finally { if (!api.controller.signal.aborted) setBusy(false) }
  }
  const currentDetail = detail && tasks.find(task => task.id === detail.id)
  const selectedDetail = currentDetail || detail
  const visible = useMemo(() => tasks.filter(task => {
    if (parent && taskRoot(task) !== taskRoot(parent)) return false
    const display = taskDisplay(task)
    return (filter === 'all' || filter === 'expired' || filter === 'pending' && taskPending(task) || filter === 'completed' && (display.canViewOutput || display.canOpenArchive) || filter === 'failed' && display.status === 'failed') && `${task.item.prompt_preview} ${task.job.model}`.toLowerCase().includes(search.toLowerCase())
  }), [tasks, filter, parent, search])
  const searching = search !== query
  const showInitialLoading = !searching && history.phase === 'reading' && tasks.length === 0
  const nextPageKey = history.pageKey
  const galleryReadError = searching ? '' : history.failure === 'append' ? '下一页暂时无法加载' : history.failure === 'read' ? '作品暂时无法读取' : ''
  const galleryCards = visible.length ? parent
    ? <div className="studio-thread">{[...visible].sort((a, b) => a.job.created_at - b.job.created_at).map(task => <div key={task.id}><p className="studio-message">{task.item.prompt_preview}</p><TaskCard record={task} previews={api.client.previews} measureMissingPreview onOpen={() => setDetail(task)} onEditOutputs={() => void edit(task)} /></div>)}</div>
    : expiredHistory ? <ExpiredGallery tasks={visible} open={setDetail} />
      : <TaskGrid tasks={visible} previews={api.client.previews} open={task => { dismissAllTooltips(); setDetail(task) }} edit={task => void edit(task)} />
    : null
  return <div className="studio-workspace">
    <dialog ref={composer} open={!mobile} className="studio-composer" aria-label="创作设置" onClick={event => { if (mobile && event.target === event.currentTarget) composer.current?.close() }}>
      <div className="studio-composer-heading"><h2>{parent ? '继续改图' : '创作设置'}</h2><button className="studio-mobile-only" aria-label="关闭创作设置" onClick={() => composer.current?.close()}>关闭</button></div>
      {parent && <p className="studio-edit-source studio-muted">基于当前参考图续改 · {parent.job.model}</p>}
      {mobile && error && <p className="studio-error" role="alert">{error}</p>}
      <InputBar capabilities={capabilities} imageSize={selectedSize || ''} aspectRatio={selectedAspect || ''} setImageSize={setImageSize} setAspectRatio={setAspectRatio} referenceError={referenceError} refs={refs} removeRef={index => { continuation.beginSelection(); setRefs(refs.filter((_, i) => i !== index)) }} prompt={prompt} setPrompt={setPrompt} editing={Boolean(parent)} upload={files => void upload(files)} models={models} modelLoading={modelsUpdating} model={model} setModel={value => void changeModel(value)} busy={busy} submitDisabled={!directoryCurrent} submit={() => void submit()} batchMode={batchMode} setBatchMode={setBatchMode} />
    </dialog>
    <section className="studio-gallery" aria-label="图片画廊"><div className="studio-toolbar"><div className="studio-gallery-heading"><h2>{parent ? '连续改图' : '我的作品'}</h2><span className="studio-gallery-count" aria-label={`已加载 ${tasks.length} ${expiredHistory ? '条过期记录' : '个图片任务'}`}>{tasks.length}</span></div>
      <div className="studio-tabs studio-gallery-tabs" aria-label="筛选任务">{filterOptions.map(([value, label]) => <button key={value} aria-pressed={filter === value} onClick={() => setFilter(value)}>{label}</button>)}</div>
      <select className="studio-gallery-status" aria-label="筛选作品" value={filter} onChange={event => { const next = filterOptions.find(([value]) => value === event.target.value); if (next) setFilter(next[0]) }}>{filterOptions.map(([value, label]) => <option key={value} value={value}>{value === 'all' ? '全部作品' : label}</option>)}</select>
      <div className="studio-toolbar-actions">{parent && <button onClick={() => { continuation.beginSelection(); setParent(null) }}>返回画廊</button>}
        <button className="studio-search-toggle" aria-label="搜索作品" aria-expanded={searchOpen} aria-pressed={searchOpen || Boolean(search)} onClick={() => setSearchOpen(value => !value)}><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 4 4" /></svg></button>
        <button className="studio-refresh" aria-label="刷新作品" disabled={history.phase === 'reading'} onClick={refreshGallery}><svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" aria-hidden="true"><path d="M20 7v5h-5M4 17v-5h5M6.1 7a7 7 0 0 1 11.5-2L20 8M4 16l2.4 3A7 7 0 0 0 18 17" /></svg></button>
      </div>
      <input ref={searchInput} className={`studio-gallery-search${searchOpen ? ' is-open' : ''}`} type="search" aria-label="搜索图片任务" placeholder="搜索提示词或模型" value={search} onChange={event => setSearch(event.target.value)} onKeyDown={event => { if (event.key === 'Escape' && !search) { setSearchOpen(false); event.currentTarget.closest('.studio-toolbar')?.querySelector<HTMLButtonElement>('.studio-search-toggle')?.focus() } }} />
    </div>
    {notice && <p className="studio-notice" role="status">{notice}</p>}
    {error && <div className="studio-error" role="alert">{error}</div>}
    {showInitialLoading ? <div className="studio-gallery-start-loading" role="status">正在读取作品…</div> : galleryCards || (searching
      ? <div className="studio-empty" role="status">正在搜索作品…</div>
      : galleryReadError || nextPageKey ? null
      : expiredHistory ? <div className="studio-expired-empty" role="status"><h3>{search ? '没有匹配的过期作品' : '暂无过期作品'}</h3><p className="studio-muted">{search ? '试试其他关键词。' : '图片到期或清理后，任务记录会出现在这里。'}</p></div>
      : <div className="studio-empty"><svg width="52" height="52" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="4" /><path d="m3 17 6-6 4 4 3-3 5 5M15 7h.01" /></svg><h2>{search || filter !== 'all' ? '没有匹配的任务' : '从一个想法开始'}</h2>{filter === 'completed' ? <p>{parent ? '目前没有匹配的可查看作品。' : '目前没有匹配的可查看作品。过期记录可在「已过期」中查看。'}</p> : (search || filter !== 'all') ? <p>试试其他筛选条件。</p> : <p>写下画面描述，或上传参考图。<br />生成结果会保存在这里。</p>}</div>)}
    {!showInitialLoading && !searching && (visible.length > 0 || nextPageKey || galleryReadError) && <GalleryTail
      pageKey={nextPageKey}
      loading={history.phase !== 'idle'}
      error={galleryReadError}
      onLoadMore={history.loadMore}
      onRetry={history.failure === 'append' ? history.retryMore : refreshGallery}
    />}
    </section>
    <button className="studio-mobile-create" onClick={() => { composer.current?.showModal(); composer.current?.querySelector<HTMLTextAreaElement>('textarea')?.focus() }}><span>{parent ? '继续修改这张图' : '描述想要的画面…'}</span><strong>展开 ↑</strong></button>
    {selectedDetail && <DetailModal key={selectedDetail.id} task={selectedDetail} api={api.client} onVideo={directoryCurrent && currentDetail ? onVideo : undefined} cancel={cancel} close={() => setDetail(null)} edit={(index, image) => void edit(selectedDetail, index, image)} />}
  </div>
}
