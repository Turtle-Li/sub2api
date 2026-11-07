import React, { useEffect, useRef, useState } from 'react'
import type { BatchImageModel, BatchImageSubmitRequest } from '@/api/batchImage'
import { listKeys, readImage, taskDisplay, taskPending, taskRoot, type StudioTask, type ItemContinuation } from './api'
import { modelIdentity, workspaceAPI } from './workspaceAPI'
import { useCloseOnEscape } from './upstream/hooks/useCloseOnEscape'
import { usePreventBackgroundScroll } from './upstream/hooks/usePreventBackgroundScroll'
import TaskCard from './upstream/components/TaskCard'
import TaskGrid from './upstream/components/TaskGrid'
import Lightbox from './upstream/components/Lightbox'
import InputBar from './upstream/components/InputBar'
import { useGlobalClickSuppression } from './upstream/lib/clickSuppression'
import { dismissAllTooltips } from './upstream/lib/tooltipDismiss'
import { useEditContinuation } from './useEditContinuation'
import './studio.css'

type API = ReturnType<typeof workspaceAPI>
function errorMessage(error: unknown): string { return error instanceof Error ? error.message : typeof error === 'object' && error !== null && 'message' in error ? String(error.message) : '请求失败，请刷新后重试' }
function ImageAsset({ task, api, index = 0 }: { task: StudioTask; api: API; index?: number }) {
  const [src, setSrc] = useState('')
  const [error, setError] = useState('')
  useEffect(() => {
    let active = true; let url = ''
    setSrc(''); setError('')
    if (task.item.image_count) api.image(task, index).then(blob => { if (active) { url = URL.createObjectURL(blob); setSrc(url) } }).catch(error => { if (active) setError(errorMessage(error)) })
    return () => { active = false; if (url) URL.revokeObjectURL(url) }
  }, [task.id, task.item.image_count, api, index])
  return src ? <img src={src} alt={task.item.prompt_preview || '生成图片'} /> : <span className="studio-muted">{error || (task.item.image_count ? '图片加载中…' : taskDisplay(task).message || taskDisplay(task).label)}</span>
}

// Upstream DetailModal/Lightbox lifecycle hooks are retained verbatim, including nested ESC ownership.
function DetailModal({ task, api, close, edit, cancel }: { task: StudioTask; api: API; close: () => void; edit: (index: number) => void; cancel: (task: StudioTask) => Promise<void> }) {
  const dialog = useRef<HTMLDialogElement>(null)
  const [index, setIndex] = useState(0)
  const [viewer, setViewer] = useState('')
  const viewerURL = useRef('')
  useEffect(() => () => { if (viewerURL.current) URL.revokeObjectURL(viewerURL.current) }, [])
  async function view(next = index) {
    try { const blob = await api.image(task, next); if (!dialog.current?.isConnected) return; const url = URL.createObjectURL(blob); if (viewerURL.current) URL.revokeObjectURL(viewerURL.current); viewerURL.current = url; setIndex(next); setViewer(url); dismissAllTooltips() } catch (error) { setError(errorMessage(error)) }
  }
  const [error, setError] = useState('')
  useCloseOnEscape(true, close)
  usePreventBackgroundScroll(true, dialog)
  useEffect(() => { dialog.current?.showModal() }, [])
  async function download() {
    try {
      const blob = await api.image(task, index); const url = URL.createObjectURL(blob)
      const a = document.createElement('a'); a.href = url; a.download = `${task.job.id}-${task.item.custom_id}-${index}.${task.item.file_extension || 'png'}`; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch (error) { setError(errorMessage(error)) }
  }
  return <dialog ref={dialog} className="studio-detail" onCancel={event => { event.preventDefault(); close() }}>
    <div className="studio-actions"><strong>任务详情</strong><button onClick={close}>关闭</button></div>
    <button className="studio-detail-image" onClick={() => void view()} disabled={!task.item.image_count} aria-label="放大图片"><ImageAsset task={task} api={api} index={index} /></button>
    {task.item.image_count > 1 && <div className="studio-actions"><button disabled={index === 0} onClick={() => setIndex(index - 1)}>上一张</button><span>{index + 1} / {task.item.image_count}</span><button disabled={index + 1 >= task.item.image_count} onClick={() => setIndex(index + 1)}>下一张</button></div>}
    <p className="studio-prompt">{task.item.prompt_preview}</p><p className="studio-muted">提示词预览可能被截断。重新生成前请核对完整内容。</p>
    <p>{task.job.model} · {taskDisplay(task).label}</p>{taskDisplay(task).message && <p role="alert">{taskDisplay(task).message}</p>}{taskPending(task) && <button onClick={() => { void cancel(task).then(close).catch(error => setError(errorMessage(error))) }}>取消此批次</button>}<p>预扣 {task.job.hold_amount} · 实际消耗 {task.job.actual_cost ?? '待结算'}</p>
    {error && <p role="alert">{error}</p>}
    <div className="studio-actions"><button disabled={!task.item.image_count} onClick={() => void download()}>下载图片</button><button disabled={!task.item.image_count} onClick={() => edit(index)}>引用此图继续修改</button></div>
    {viewer && <Lightbox src={viewer} imageId={`${task.id}/${index}`} onClose={() => setViewer('')} currentIndex={index} total={task.item.image_count} showNav={task.item.image_count > 1} onPrev={() => void view((index - 1 + task.item.image_count) % task.item.image_count)} onNext={() => void view((index + 1) % task.item.image_count)} onDownload={() => void download()} onEdit={() => edit(index)} />}
  </dialog>
}

export function Studio({ userId }: { readonly userId: number }) {
  useGlobalClickSuppression()
  const [keys, setKeys] = useState<number[] | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    const controller = new AbortController()
    listKeys(controller.signal).then(items => { if (!controller.signal.aborted) setKeys(items.filter(item => item.status === 'active' && !item.expired && !item.quota_exhausted).map(item => item.id)) }).catch(error => { if (!controller.signal.aborted) setError(errorMessage(error)) })
    return () => controller.abort()
  }, [userId])
  return <div className="image-studio">
    {error && <p className="studio-error" role="alert">{error}</p>}{keys?.length ? <Workspace key={`${userId}:${keys.join(',')}`} keys={keys} /> : <div className="studio-empty" role="status">{keys === null && !error ? '正在读取可用模型…' : '暂无可用的生图权限，请联系管理员开通。'}</div>}
  </div>
}

function Workspace({ keys }: { readonly keys: readonly number[] }) {
  const [api] = useState(() => { const controller = new AbortController(); return { client: workspaceAPI(keys, controller.signal), controller } })
  const composer = useRef<HTMLDialogElement>(null)
  const [mobile, setMobile] = useState(() => Boolean(window.matchMedia?.('(max-width: 760px)').matches))
  const [batchMode, setBatchMode] = useState(false)
  const [filter, setFilter] = useState('all')
  const [notice, setNotice] = useState('')
  useEffect(() => {
    const query = window.matchMedia?.('(max-width: 760px)')
    if (!query) return
    const update = () => { composer.current?.close(); setMobile(query.matches) }
    query.addEventListener('change', update)
    return () => query.removeEventListener('change', update)
  }, [])
  const [tasks, setTasks] = useState<StudioTask[]>([])
  const readVersion = useRef(0)
  const tasksRef = useRef<StudioTask[]>([])
  tasksRef.current = tasks
  const [nextCursor, setNextCursor] = useState<string | null>(null)
  const loadedPages = useRef(1)
  const [itemContinuations, setItemContinuations] = useState<ItemContinuation[]>([])
  const [models, setModels] = useState<BatchImageModel[]>([])
  const [model, setModel] = useState('')
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
  const [detail, setDetail] = useState<StudioTask | null>(null)
  const [search, setSearch] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  const continuation = useEditContinuation({
    tasks, signal: api.controller.signal,
    load: async task => readImage(await api.client.image(task, 0)),
    onAdvance: (image, task) => { setRefs([image]); setParent(task) },
    onError: error => setError(`新图片已完成，但自动引用失败：${errorMessage(error)}。请从任务中重新选择参考图。`),
  })
  const [refresh, setRefresh] = useState(0)
  useEffect(() => {
    let active = true
    async function load() {
      const version = readVersion.current
      try {
        api.client.refreshDirectory()
        const [catalog, first] = await Promise.all([api.client.models(), api.client.tasks()])
        let cursor = first.nextCursor
        const rows = [...first.tasks]
        const continuations = [...first.itemContinuations]
        for (let page = 1; page < loadedPages.current && cursor; page++) {
          const next = await api.client.tasks(cursor); rows.push(...next.tasks); continuations.push(...next.itemContinuations); cursor = next.nextCursor
        }
        if (!active || version !== readVersion.current) return
        setModels(catalog); setModel(previous => catalog.some(item => modelIdentity(item) === previous) ? previous : catalog[0] ? modelIdentity(catalog[0]) : ''); setTasks(rows); setItemContinuations(continuations); setNextCursor(cursor); setError(api.client.warnings().join(' '))
      }
      catch (error) { if (active) setError(errorMessage(error)) }
      finally { if (active) setLoading(false) }
    }
    void load(); return () => { active = false }
  }, [api, refresh])
  const hasPending = tasks.some(taskPending)
  useEffect(() => {
    if (!hasPending) return
    let active = true; let timer: ReturnType<typeof setTimeout>
    async function poll() {
      const version = readVersion.current
      try { const updated = await api.client.refreshPending(tasksRef.current); if (active && version === readVersion.current) setTasks(current => current.map(task => updated.find(value => value.id === task.id) || task)) }
      catch (error) { if (active) setError(errorMessage(error)) }
      finally { if (active) timer = setTimeout(() => void poll(), 6000) }
    }
    timer = setTimeout(() => void poll(), 6000)
    return () => { active = false; clearTimeout(timer) }
  }, [api, hasPending])
  async function loadMore() {
    if (!nextCursor || loading) return
    setLoading(true)
    try { const page = await api.client.tasks(nextCursor); if (!api.controller.signal.aborted) { loadedPages.current++; setTasks(current => [...current, ...page.tasks.filter(task => !current.some(old => old.id === task.id))]); setNextCursor(page.nextCursor); setItemContinuations(current => [...current, ...page.itemContinuations]) } }
    catch (error) { if (!api.controller.signal.aborted) setError(errorMessage(error)) }
    finally { if (!api.controller.signal.aborted) setLoading(false) }
  }
  async function loadItems(continuation: ItemContinuation) {
    if (loading) return
    setLoading(true)
    try {
      const page = await api.client.items(continuation.job, continuation.cursor)
      if (api.controller.signal.aborted) return
      setTasks(current => {
        const job = current.find(task => task.job.id === continuation.job.id)?.job || continuation.job
        return [...current, ...page.tasks.filter(task => !current.some(old => old.id === task.id)).map(task => ({ ...task, job }))]
      })
      setItemContinuations(current => current.flatMap(item => item.job.id === continuation.job.id ? page.next ? [page.next] : [] : [item]))
    } catch (error) { if (!api.controller.signal.aborted) setError(errorMessage(error)) }
    finally { if (!api.controller.signal.aborted) setLoading(false) }
  }
  useEffect(() => () => api.controller.abort(), [api])
  async function cancel(task: StudioTask) {
    const job = await api.client.cancel(task)
    if (api.controller.signal.aborted) return
    // The returned batch is authoritative; its items may intentionally remain pending.
    readVersion.current++
    setItemContinuations(current => current.map(item => item.job.id === job.id ? { ...item, job } : item))
    setTasks(current => current.map(item => item.job.id === job.id ? { ...item, job } : item))
  }
  async function edit(task: StudioTask, index = 0) {
    const selection = continuation.beginSelection()
    try {
      const data = await readImage(await api.client.image(task, index))
      const sourceModel = models.find(item => item.id === task.job.model && item.provider === task.job.provider)
      const linked = sourceModel ? await api.client.canContinue(task, sourceModel) : false
      if (!continuation.isCurrent(selection)) return
      setRefs([data]); setParent(linked ? task : null); setPrompt(''); setBatchMode(false); setDetail(null)
      if (sourceModel) setModel(modelIdentity(sourceModel))
      setNotice(linked ? '' : '原模型暂不可用，已保留参考图，可选择模型创建新作品。')
      if (window.matchMedia?.('(max-width: 760px)').matches) composer.current?.showModal()
    } catch (error) { setError(errorMessage(error)) }
  }
  async function changeModel(value: string) {
    const selection = continuation.beginSelection()
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
    try { const picked = Array.from(files); if (picked.some(file => !['image/png', 'image/jpeg', 'image/webp'].includes(file.type) || file.size > 8 * 1024 * 1024)) throw new Error('请选择 8 MB 以内的 PNG、JPEG 或 WebP 图片'); const images = await Promise.all(picked.map(readImage)); if (continuation.isCurrent(selection)) setRefs(previous => [...previous, ...images]) } catch (error) { setError(errorMessage(error)) }
  }
  async function submit() {
    const selected = models.find(item => modelIdentity(item) === model)
    if (!selected || !prompt.trim() || busy || referenceError) return
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
  const selectedDetail = detail ? tasks.find(task => task.id === detail.id) || detail : null
  const visible = tasks.filter(task => (!parent || taskRoot(task) === taskRoot(parent)) && (filter === 'all' || filter === 'pending' && taskPending(task) || filter === 'completed' && task.item.image_count > 0 || filter === 'failed' && taskDisplay(task).status === 'failed') && `${task.item.prompt_preview} ${task.job.model}`.toLowerCase().includes(search.toLowerCase()))
  return <div className="studio-workspace">
    <dialog ref={composer} open={!mobile} className="studio-composer" aria-label="创作设置">
      <div className="studio-composer-heading"><h2>{parent ? '继续改图' : '创作设置'}</h2><button className="studio-mobile-only" aria-label="关闭创作设置" onClick={() => composer.current?.close()}>关闭</button></div>
      {parent && <p className="studio-edit-source studio-muted">基于当前参考图续改 · {parent.job.model}</p>}
      {mobile && error && <p className="studio-error" role="alert">{error}</p>}
      <InputBar capabilities={capabilities} imageSize={selectedSize || ''} aspectRatio={selectedAspect || ''} setImageSize={setImageSize} setAspectRatio={setAspectRatio} referenceError={referenceError} refs={refs} removeRef={index => { continuation.beginSelection(); setRefs(refs.filter((_, i) => i !== index)) }} prompt={prompt} setPrompt={setPrompt} editing={Boolean(parent)} upload={files => void upload(files)} models={models} model={model} setModel={value => void changeModel(value)} busy={busy} submit={() => void submit()} batchMode={batchMode} setBatchMode={setBatchMode} />
    </dialog>
    <section className="studio-gallery" aria-label="图片画廊"><div className="studio-toolbar"><div className="studio-gallery-heading"><h2>{parent ? '连续改图' : '我的作品'}</h2><span className="studio-muted">{tasks.length} 个图片任务</span></div><div className="studio-toolbar-actions">{parent && <button onClick={() => { continuation.beginSelection(); setParent(null) }}>返回画廊</button>}<button disabled={loading} onClick={() => setRefresh(value => value + 1)}>刷新</button></div></div>
    <div className="studio-gallery-filters"><div className="studio-tabs" aria-label="筛选任务">{[['all', '全部'], ['pending', '生成中'], ['completed', '已完成'], ['failed', '失败']].map(([value, label]) => <button key={value} aria-pressed={filter === value} onClick={() => setFilter(value)}>{label}</button>)}</div><input aria-label="搜索图片任务" placeholder="搜索提示词或模型" value={search} onChange={event => setSearch(event.target.value)} /></div>
    {notice && <p className="studio-notice" role="status">{notice}</p>}
    {error && <div className="studio-error" role="alert">{error}</div>}
    {loading && !tasks.length ? <div className="studio-empty" role="status">正在读取任务…</div> : !visible.length ? <div className="studio-empty"><svg width="52" height="52" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="4" /><path d="m3 17 6-6 4 4 3-3 5 5M15 7h.01" /></svg><h2>{search || filter !== 'all' ? '没有匹配的任务' : '从一个想法开始'}</h2><p>写下画面描述，或上传参考图。<br />生成结果会保存在这里。</p></div> : parent ? <div className="studio-thread">{[...visible].sort((a,b) => a.job.created_at - b.job.created_at).map(task => <div key={task.id}><p className="studio-message">{task.item.prompt_preview}</p><TaskCard record={task} api={api.client} onOpen={() => setDetail(task)} onEditOutputs={() => void edit(task)} /></div>)}</div> : <TaskGrid tasks={[...visible].sort((a,b) => b.job.created_at - a.job.created_at)} api={api.client} open={task => { dismissAllTooltips(); setDetail(task) }} edit={task => void edit(task)} />}

    {itemContinuations.map(continuation => <button key={continuation.job.id} disabled={loading} onClick={() => void loadItems(continuation)}>加载批次 {continuation.job.id} 的更多图片任务</button>)}
    {nextCursor && <button disabled={loading} onClick={() => void loadMore()}>加载更早的批次</button>}
    </section>
    <button className="studio-mobile-create" onClick={() => { composer.current?.showModal(); composer.current?.querySelector<HTMLTextAreaElement>('textarea')?.focus() }}><span>{parent ? '继续修改这张图' : '描述想要的画面…'}</span><strong>展开 ↑</strong></button>
    {selectedDetail && <DetailModal task={selectedDetail} api={api.client} cancel={cancel} close={() => setDetail(null)} edit={index => void edit(selectedDetail, index)} />}
  </div>
}
