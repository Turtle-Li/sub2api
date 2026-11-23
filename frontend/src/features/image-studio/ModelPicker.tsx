import React, { useEffect, useId, useRef, useState } from 'react'
import type { BatchImageModel } from '@/api/batchImage'
import '@/styles/model-picker.css'
import { getModelThemeMeta, resolveImageModelTheme, type ModelThemeKey } from '@/features/studio-ui/modelThemes'
import { modelIdentity } from './workspaceAPI'

const platforms: Readonly<Record<string, string>> = {
  gemini_api: 'Gemini', vertex: 'Gemini', openai_images: 'OpenAI',
  wan_images: '通义万相', seedream_images: '豆包 Seedream',
  muse_images: 'Muse', workbuddy_images: '腾讯混元',
}
export const modelPlatform = (provider: string) => platforms[provider] || provider
const nativePlatforms = ['wan_images', 'seedream_images'] as const

const tierWords: Readonly<Record<string, string>> = {
  pro: 'Pro', flash: 'Flash', lite: 'Lite', alpha: 'Alpha', edit: 'Edit', image: 'Image',
}

export function shortModelName(model: string, provider = ''): string {
  const raw = model.trim()
  if (!raw) return modelPlatform(provider) || 'AI'
  const cleaned = raw
    .replace(/^(?:cn|global):/i, '')
    .replace(/-image-preview$/i, '')
    .replace(/-preview$/i, '')
    .replace(/-\d{6,8}$/, '')
  if (/^gemini-/i.test(cleaned)) {
    const core = cleaned.replace(/^gemini-/i, '').replace(/-image$/i, '').replace(/flash-lite/i, 'lite')
    return core.split('-').filter(Boolean).map(part => tierWords[part.toLowerCase()] || part).join(' ')
  }
  if (/^gpt-image-/i.test(cleaned)) {
    return `GPT ${cleaned.replace(/^gpt-image-/i, '')}`
  }
  if (/^dall-e-/i.test(cleaned)) {
    return `DALL·E ${cleaned.replace(/^dall-e-/i, '')}`
  }
  if (/^(?:doubao-)?seedream-/i.test(cleaned)) {
    const core = cleaned.replace(/^(?:doubao-)?seedream-/i, '').replace(/^(\d+)-(\d+)(?=-|$)/, '$1.$2')
    return core.split('-').filter(Boolean).map(part => tierWords[part.toLowerCase()] || part).join(' ')
  }
  if (/^wan/i.test(cleaned)) {
    const core = cleaned.replace(/^wanx?-?/i, '').replace(/-image(?=-|$)/i, '')
    return core.split('-').filter(Boolean).map(part => tierWords[part.toLowerCase()] || part).join(' ')
  }
  if (/^muse(?:-image)?$/i.test(cleaned)) return 'Muse'
  if (/^(?:hunyuan-image-|hy-image-|hunyuan-)/i.test(cleaned)) {
    const core = cleaned.replace(/^(?:hunyuan-image-|hy-image-|hunyuan-)/i, '')
    return core.split('-').filter(Boolean).map(part => tierWords[part.toLowerCase()] || part).join(' ')
  }
  const fallback = cleaned.replace(/-image$/i, '') || cleaned
  return fallback.split('-').filter(Boolean).map(part => tierWords[part.toLowerCase()] || part).join(' ')
}

/** Single public display-name resolver across trigger, option list, and detail pane. */
export function imageModelDisplayName(model: Pick<BatchImageModel, 'id' | 'provider'>): string {
  const raw = model.id.trim()
  return raw || modelPlatform(model.provider) || 'AI'
}

export function modelBadge(provider: string, model = ''): {
  readonly key: string; readonly mark: string; readonly label: string; readonly title: string
} {
  const platform = modelPlatform(provider)
  const lowerModel = model.trim().toLowerCase()
  const key = provider === 'gemini_api' || provider === 'vertex' || lowerModel.startsWith('gemini-')
    ? 'gemini'
    : provider === 'openai_images' || lowerModel.startsWith('gpt-image') || lowerModel.startsWith('dall-e')
      ? 'openai'
      : provider === 'seedream_images' || lowerModel.includes('seedream')
        ? 'seedream'
        : provider === 'wan_images' || lowerModel.startsWith('wan')
          ? 'wan'
          : provider === 'muse_images' || lowerModel.startsWith('muse')
            ? 'muse'
            : provider === 'workbuddy_images' || /(^|:)(hunyuan|hy-image)/.test(lowerModel)
              ? 'hunyuan'
              : 'generic'
  const marks: Readonly<Record<string, string>> = {
    gemini: 'G', openai: 'O', seedream: 'S', wan: 'W', muse: 'M', hunyuan: 'H',
  }
  const mark = marks[key] || (platform || model || 'AI').trim().slice(0, 1).toUpperCase()
  const label = shortModelName(model, provider)
  const title = model && platform ? `${platform} · ${model}` : model || platform || 'AI'
  return { key, mark, label, title }
}

function pickerModels(models: readonly BatchImageModel[]) {
  return models.filter(model => {
    if (!['gemini-3-pro-image-preview', 'gemini-3.1-flash-image-preview'].includes(model.id)) return true
    const stable = models.find(candidate => candidate.provider === model.provider && candidate.id === model.id.replace(/-preview$/, ''))
    return !stable?.prices || JSON.stringify(stable.capabilities ?? null) !== JSON.stringify(model.capabilities ?? null) || JSON.stringify(stable.prices) !== JSON.stringify(model.prices)
  })
}

export function modelPrice(model: BatchImageModel | undefined, size: string, count = 1): string {
  const price = model?.prices?.[size || model.capabilities?.default_image_size || '1K']
  if (!price || !Number.isFinite(price.min) || !Number.isFinite(price.max) || price.min < 0 || price.max < price.min) return '价格待配置'
  const format = (value: number) => new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 4 }).format(value * count)
  return price.min === price.max ? format(price.min) : `${format(price.min)}–${format(price.max)}`
}

function resolveOptionSize(model: BatchImageModel | undefined, size: string): string {
  if (!model) return size || '1K'
  return model.capabilities?.image_sizes.includes(size) ? size : model.capabilities?.default_image_size || size || '1K'
}

function imageCapabilitySummary(model: BatchImageModel, peers: readonly BatchImageModel[] = []): string {
  const caps = model.capabilities
  const parts: string[] = []
  if (caps?.image_sizes?.length) parts.push(caps.image_sizes.join('/'))
  if (caps?.max_reference_images !== undefined) {
    parts.push(caps.max_reference_images > 0 ? `最多 ${caps.max_reference_images} 张参考图` : '纯文字')
  }
  const peerAspectDiff = peers.length > 1 && new Set(peers.map(peer => (peer.capabilities?.aspect_ratios || []).join(','))).size > 1
  if ((!parts.length || peerAspectDiff) && caps?.aspect_ratios?.length) {
    parts.push(`${caps.aspect_ratios.length} 种比例`)
  }
  return parts.join(' · ') || '默认规格'
}

function imageTriggerSummary(model: BatchImageModel): string {
  const brand = modelPlatform(model.provider)
  const cap = imageCapabilitySummary(model)
  return brand ? `${brand} · ${cap}` : cap
}

function imageDetailSubtitle(model: BatchImageModel): string {
  const brand = modelPlatform(model.provider)
  const cap = imageCapabilitySummary(model)
  return brand ? `${brand} · ${cap}` : cap
}

function optionPriceUnit(price: string, optionSize: string): string {
  return price === '价格待配置' ? `${optionSize} 分辨率` : `${optionSize} · 额度 / 张`
}

function estimateVisualUnits(text: string): number {
  let units = 0
  for (const ch of text) {
    units += /[\u2e80-\u9fff\uf900-\ufaff\uff00-\uffef]/.test(ch) ? 1.75 : 1
  }
  return units
}

function formatMimeTypes(types?: readonly string[]): string {
  if (!types?.length) return ''
  return types
    .map(mime => mime.replace(/^image\//i, '').toUpperCase())
    .join(' · ')
}

function ModelThemeGlyph({ theme }: { readonly theme: ModelThemeKey }) {
  const meta = getModelThemeMeta(theme)
  return (
    <svg viewBox={meta.viewBox} fill="currentColor" aria-hidden="true" focusable="false">
      {meta.paths.map((d, index) => <path key={index} d={d} />)}
    </svg>
  )
}

export function ModelPicker({ models, value, size, disabled, loading = false, onChange }: {
  readonly models: readonly BatchImageModel[]; readonly value: string; readonly size: string
  readonly disabled: boolean; readonly loading?: boolean; readonly onChange: (value: string) => void
}) {
  const dialog = useRef<HTMLDialogElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const popoverRef = useRef<HTMLDivElement>(null)
  const infoTriggerRefs = useRef<Record<string, HTMLButtonElement | null>>({})
  const suppressFocusOpen = useRef(false)
  const popoverId = useId()
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [platform, setPlatform] = useState('')
  const [draftId, setDraftId] = useState('')
  const [infoId, setInfoId] = useState<string | null>(null)
  const [infoPinned, setInfoPinned] = useState(false)
  const [popoverPos, setPopoverPos] = useState<{ readonly top: number; readonly left: number }>({ top: 72, left: 16 })
  const options = pickerModels(models)
  const original = models.find(model => modelIdentity(model) === value)
  const selected = original && !options.includes(original) ? options.find(model => model.provider === original.provider && model.id === original.id.replace(/-preview$/, '')) : original
  const selectedId = selected ? modelIdentity(selected) : ''
  const query = search.trim().toLowerCase()
  const groups = new Map<string, BatchImageModel[]>()
  for (const model of options) {
    const brand = modelPlatform(model.provider)
    const displayName = imageModelDisplayName(model)
    const capText = imageCapabilitySummary(model)
    if (query && !`${brand} ${displayName} ${capText}`.toLowerCase().includes(query)) continue
    const existing = groups.get(brand) || []
    existing.push(model); groups.set(brand, existing)
  }
  if (!loading) for (const provider of nativePlatforms) {
    const brand = modelPlatform(provider)
    if (brand.toLowerCase().includes(query) && !options.some(model => model.provider === provider)) groups.set(brand, [])
  }
  const active = groups.has(platform) ? platform : groups.keys().next().value
  const visible = active ? groups.get(active) || [] : []
  const draft = visible.find(item => modelIdentity(item) === draftId)
    || visible.find(item => modelIdentity(item) === selectedId)
    || visible[0]
  const draftIdentity = draft ? modelIdentity(draft) : ''
  const infoModel = infoId ? visible.find(item => modelIdentity(item) === infoId) ?? null : null
  const infoIdentity = infoModel ? modelIdentity(infoModel) : ''
  const infoSize = resolveOptionSize(infoModel ?? undefined, size)
  const infoPrice = modelPrice(infoModel ?? undefined, infoSize)
  const infoTheme: ModelThemeKey = infoModel ? resolveImageModelTheme(infoModel.provider, infoModel.id) : 'neutral'
  const selectedBadge = selected ? modelBadge(selected.provider, selected.id) : null
  const selectedTheme: ModelThemeKey = selected ? resolveImageModelTheme(selected.provider, selected.id) : 'neutral'
  const selectedDisplayName = selected ? imageModelDisplayName(selected) : ''
  const stackPrices = visible.some(model => {
    const optionSize = resolveOptionSize(model, size)
    const price = modelPrice(model, optionSize)
    const unit = optionPriceUnit(price, optionSize)
    return estimateVisualUnits(`${price} ${unit}`) > 19
  })

  function computePopoverPos(anchor: HTMLElement | null) {
    if (!anchor || typeof window === 'undefined') return { top: 72, left: 16 }
    const rect = anchor.getBoundingClientRect()
    if (rect.width === 0 && rect.height === 0) return { top: 72, left: 16 }
    const margin = 12
    const popWidth = Math.min(352, window.innerWidth - margin * 2)
    const popHeight = 320
    const preferRight = rect.right + 8 + popWidth <= window.innerWidth - margin
    const rawLeft = preferRight ? rect.right + 8 : rect.right - popWidth
    const left = Math.max(margin, Math.min(rawLeft, window.innerWidth - popWidth - margin))
    const rawTop = rect.bottom + 6 + popHeight <= window.innerHeight - margin
      ? rect.bottom + 6
      : Math.max(margin, rect.top - popHeight - 6)
    const top = Math.max(margin, Math.min(rawTop, window.innerHeight - Math.min(popHeight, window.innerHeight - margin * 2) - margin))
    return { top, left }
  }

  function closeInfo(restoreFocus = false) {
    const previousId = infoId
    setInfoId(null)
    setInfoPinned(false)
    if (restoreFocus && previousId) {
      suppressFocusOpen.current = true
      infoTriggerRefs.current[previousId]?.focus()
      suppressFocusOpen.current = false
    }
  }

  function openInfo(id: string, anchor: HTMLElement | null, pin = false) {
    setPopoverPos(computePopoverPos(anchor))
    setInfoId(id)
    if (pin) setInfoPinned(true)
  }

  function toggleInfoPin(id: string, anchor: HTMLElement | null) {
    if (infoPinned && infoId === id) {
      closeInfo()
      return
    }
    setPopoverPos(computePopoverPos(anchor))
    setInfoId(id)
    setInfoPinned(true)
  }

  function handleInfoLeave(id: string, relatedTarget: EventTarget | null) {
    if (infoPinned) return
    const next = relatedTarget instanceof Node ? relatedTarget : null
    if (next && (popoverRef.current?.contains(next) || infoTriggerRefs.current[id]?.contains(next))) return
    setInfoId(current => (current === id ? null : current))
  }

  useEffect(() => {
    if (infoId && !visible.some(item => modelIdentity(item) === infoId)) {
      setInfoId(null)
      setInfoPinned(false)
    }
  }, [infoId, visible])

  function close() { closeInfo(); dialog.current?.close(); setOpen(false); trigger.current?.focus() }
  function confirm() {
    if (!draft) return
    onChange(modelIdentity(draft))
    close()
  }

  return <div className="studio-model-picker wb-field">
    <span className="studio-field-label wb-label" id="studio-model-label">图片模型</span>
    {/* Fixed-height, single-line trigger: long model names must not change the rail height. */}
    <button
      ref={trigger}
      type="button"
      aria-label="图片模型"
      aria-haspopup="dialog"
      aria-expanded={open}
      title={selected ? selectedDisplayName : undefined}
      className="studio-model-trigger mp-trigger"
      data-model-theme={selectedTheme}
      disabled={disabled || (loading && !models.length)}
      onClick={() => {
        const initialPlatform = selected ? modelPlatform(selected.provider) : ''
        setSearch(''); setPlatform(initialPlatform); setDraftId(selectedId)
        setOpen(true); dialog.current?.showModal(); dialog.current?.querySelector<HTMLInputElement>('input')?.focus()
      }}
    >
      <span className="studio-model-mark mp-trigger-mark" data-platform={selectedBadge?.key || 'generic'} aria-hidden="true">
        <ModelThemeGlyph theme={selectedTheme} />
        <span className="mp-sr-only">{selectedBadge?.mark || 'AI'}</span>
      </span>
      <span className="studio-model-copy mp-trigger-copy">
        <strong>{selected ? selectedDisplayName : (loading ? '正在加载模型…' : '暂无可用模型')}</strong>
        <small>{selected ? imageTriggerSummary(selected) : '生图模型'}</small>
      </span>
      <svg className="mp-trigger-chevron" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" aria-hidden="true"><path d="m7 10 5 5 5-5" /></svg>
    </button>
    <div className="studio-model-current-price" aria-live="polite">
      <span>{size || selected?.capabilities?.default_image_size || '1K'} 分辨率{selected && loading ? ' · 更新中' : ''}</span>
      <span><strong>{!selected && loading ? '正在读取价格…' : modelPrice(selected, size)}</strong>{modelPrice(selected, size) !== '价格待配置' && ' 额度 / 张'}</span>
    </div>
    <dialog
      ref={dialog}
      className="studio-model-dialog mp-dialog"
      aria-label="选择图片模型"
      onCancel={event => { event.preventDefault(); close() }}
      onClose={() => { closeInfo(); setOpen(false); trigger.current?.focus() }}
      onClick={event => {
        if (event.target === event.currentTarget) { close(); return }
        const target = event.target instanceof Node ? event.target : null
        if (infoId && target && !popoverRef.current?.contains(target) && !(target instanceof Element && target.closest('.mp-info-wrap'))) {
          closeInfo()
        }
      }}
      onKeyDown={event => {
        if (event.key === 'Escape') {
          event.preventDefault()
          if (infoId) { closeInfo(true); return }
          close()
          return
        }
        if (event.key === 'Enter' && draft) {
          const target = event.target instanceof HTMLElement ? event.target : null
          if (target?.getAttribute('role') === 'option') { event.preventDefault(); event.stopPropagation(); confirm() }
        }
      }}
    >
      <div className="studio-model-dialog-heading mp-head">
        <h2>选择图片模型</h2>
        <button type="button" className="mp-close" aria-label="关闭模型选择" onClick={close}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true"><path d="m6 6 12 12M6 18 18 6" /></svg>
        </button>
      </div>
      <div className="studio-model-filter-bar mp-toolbar">
        <input
          type="search"
          autoFocus
          className="mp-search-input"
          aria-label="搜索生图模型"
          placeholder="搜索模型名称、品牌或规格…"
          value={search}
          onChange={event => setSearch(event.target.value)}
          onKeyDown={event => {
            if ((event.key === 'ArrowDown' || event.key === 'ArrowUp') && visible.length) {
              event.preventDefault()
              const items = Array.from(dialog.current?.querySelectorAll<HTMLButtonElement>('[role="option"]') ?? [])
              const currentIndex = visible.findIndex(item => modelIdentity(item) === draftIdentity)
              const offset = event.key === 'ArrowDown' ? 1 : -1
              const next = currentIndex >= 0
                ? (currentIndex + offset + visible.length) % visible.length
                : (event.key === 'ArrowDown' ? 0 : visible.length - 1)
              const targetModel = visible[next]
              if (targetModel) setDraftId(modelIdentity(targetModel))
              items[next]?.focus()
            }
          }}
        />
        <nav className="studio-model-platforms mp-brands" aria-label="模型品牌">
          {[...groups].map(([name, entries]) => (
            <button key={name} type="button" className="mp-brand-btn" aria-pressed={active === name} onClick={() => { closeInfo(); setPlatform(name) }}>
              <span>{name}</span>
              <small>{entries.length}</small>
            </button>
          ))}
        </nav>
      </div>
      <div className="studio-model-browser mp-layout">
        {visible.length ? <>
          <div
            className={`studio-model-options mp-list${stackPrices ? ' is-stacked-price' : ''}`}
            role="listbox"
            aria-label="可用生图模型"
            onKeyDown={event => {
              if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
              const target = event.target instanceof HTMLElement ? event.target : null
              if (target?.getAttribute('role') !== 'option') return
              const items = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="option"]'))
              const index = items.findIndex(option => option === document.activeElement)
              const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length
              event.preventDefault()
              const targetModel = visible[next]
              if (targetModel) setDraftId(modelIdentity(targetModel))
              closeInfo()
              items[next]?.focus()
            }}
          >
            {visible.map(model => {
              const id = modelIdentity(model)
              const displayName = imageModelDisplayName(model)
              const optionSize = resolveOptionSize(model, size)
              const price = modelPrice(model, optionSize)
              const unit = optionPriceUnit(price, optionSize)
              const capSummary = imageCapabilitySummary(
                model,
                visible.filter(candidate => imageModelDisplayName(candidate) === displayName),
              )
              const isCurrent = id === selectedId
              const isSelected = id === draftIdentity
              const isInfoOpen = infoId === id
              const optionTheme = resolveImageModelTheme(model.provider, model.id)
              const accessibleLabel = `${displayName}，${isCurrent ? '当前模型，' : ''}${capSummary}，${price} ${unit}`
              return <div
                key={id}
                className="mp-option-row"
                role="presentation"
                data-model-theme={optionTheme}
                data-selected={isSelected ? 'true' : 'false'}
                data-info-open={isInfoOpen ? 'true' : 'false'}
              >
                <button
                  type="button"
                  role="option"
                  aria-selected={isSelected}
                  aria-label={accessibleLabel}
                  className="studio-model-option mp-option"
                  data-model-theme={optionTheme}
                  title={displayName}
                  onClick={() => { setDraftId(id); closeInfo() }}
                >
                  <span className="mp-option-head">
                    <span className="mp-option-mark" aria-hidden="true"><ModelThemeGlyph theme={optionTheme} /></span>
                    <strong className="mp-option-name">{displayName}</strong>
                    <span className="mp-option-status">
                      {isCurrent && <span className="studio-model-current-pill mp-current-tag">当前</span>}
                    </span>
                  </span>
                  <span className="mp-option-meta">
                    <span className="studio-model-option-price mp-option-price" data-price-state={price === '价格待配置' ? 'unavailable' : 'ready'}>
                      <strong>{price}</strong>
                      <small>{unit}</small>
                    </span>
                  </span>
                </button>
                <div
                  className="mp-info-wrap"
                  onPointerEnter={event => {
                    if (event.pointerType === 'mouse' && (!infoPinned || infoId === id)) {
                      openInfo(id, infoTriggerRefs.current[id] ?? null)
                    }
                  }}
                  onPointerLeave={event => {
                    if (event.pointerType === 'mouse') handleInfoLeave(id, event.relatedTarget)
                  }}
                >
                  <button
                    ref={el => { infoTriggerRefs.current[id] = el }}
                    type="button"
                    className="mp-info-btn"
                    data-model-id={id}
                    aria-label={`查看 ${displayName} 详情`}
                    aria-expanded={isInfoOpen}
                    aria-controls={isInfoOpen ? popoverId : undefined}
                    onFocus={event => {
                      if (suppressFocusOpen.current || !event.currentTarget.matches(':focus-visible')) return
                      openInfo(id, event.currentTarget)
                    }}
                    onBlur={event => handleInfoLeave(id, event.relatedTarget)}
                    onClick={event => {
                      event.stopPropagation()
                      toggleInfoPin(id, event.currentTarget)
                    }}
                  >
                    ?
                  </button>
                </div>
              </div>
            })}
          </div>
          {infoModel && <div
            ref={popoverRef}
            id={popoverId}
            role="region"
            aria-label={`${imageModelDisplayName(infoModel)} 模型详情`}
            className="studio-model-detail mp-detail mp-detail-popover"
            data-model-theme={infoTheme}
            style={{ top: `${popoverPos.top}px`, left: `${popoverPos.left}px` }}
            onPointerEnter={event => {
              if (event.pointerType === 'mouse' && !infoPinned) setInfoId(infoIdentity)
            }}
            onPointerLeave={event => {
              if (event.pointerType === 'mouse') handleInfoLeave(infoIdentity, event.relatedTarget)
            }}
            onBlur={event => handleInfoLeave(infoIdentity, event.relatedTarget)}
          >
            <div className="mp-detail-head">
              <div className="mp-detail-title-row">
                <h3>{imageModelDisplayName(infoModel)}</h3>
                <div className="mp-detail-head-actions">
                  <span className={`studio-model-state-pill mp-state-label${infoIdentity === selectedId ? ' is-current' : ''}`}>
                    {infoIdentity === selectedId ? '当前模型' : (infoIdentity === draftIdentity ? '候选预览' : '模型详情')}
                  </span>
                  <button type="button" className="mp-detail-close" aria-label="收起模型详情" onClick={() => closeInfo(true)}>
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true"><path d="m6 6 12 12M6 18 18 6" /></svg>
                  </button>
                </div>
              </div>
              <p className="mp-detail-sub">{imageDetailSubtitle(infoModel)}</p>
            </div>
            <dl className="studio-capability-grid mp-spec-list">
              <div className="mp-spec-row">
                <dt>计价参考</dt>
                <dd>{infoPrice === '价格待配置' ? `价格待配置（${infoSize}）` : `${infoPrice} 额度 / 张（${infoSize}，随分辨率更新，以结算为准）`}</dd>
              </div>
              <div className="mp-spec-row">
                <dt>参考图能力</dt>
                <dd>{infoModel.capabilities?.max_reference_images !== undefined ? (infoModel.capabilities.max_reference_images > 0 ? `最多 ${infoModel.capabilities.max_reference_images} 张参考图` : '纯文字生成（不支持参考图）') : '模型默认'}</dd>
              </div>
              <div className="mp-spec-row">
                <dt>可选分辨率</dt>
                <dd>{infoModel.capabilities?.image_sizes?.length ? infoModel.capabilities.image_sizes.join(' · ') : '模型默认'}</dd>
              </div>
              <div className="mp-spec-row">
                <dt>可选画幅比例</dt>
                <dd>{infoModel.capabilities?.aspect_ratios?.length ? infoModel.capabilities.aspect_ratios.join(' · ') : '模型默认'}</dd>
              </div>
              {infoModel.capabilities?.response_mime_types?.length ? (
                <div className="mp-spec-row">
                  <dt>输出格式</dt>
                  <dd>{formatMimeTypes(infoModel.capabilities.response_mime_types)}</dd>
                </div>
              ) : null}
            </dl>
          </div>}
        </> : <div className="studio-model-empty mp-empty" role="status">
          {active
            ? <><strong>{active} 暂无可用模型</strong><p>请联系管理员确认工作台的模型和生图权限。</p></>
            : <><strong>{loading ? '正在读取可用模型…' : '没有匹配的生图模型'}</strong><p>{loading ? '请稍候…' : '请尝试其他品牌筛选或清空搜索关键词。'}</p></>}
        </div>}
      </div>
      <div className="studio-model-dialog-foot mp-foot">
        <span className="studio-picker-kbd mp-kbd" aria-hidden="true">
          <kbd>↑</kbd><kbd>↓</kbd> 预览 · <kbd>Enter</kbd> 应用 · <kbd>Esc</kbd> 取消
        </span>
        <div className="studio-model-dialog-actions mp-foot-actions">
          <button type="button" className="studio-picker-cancel mp-btn-cancel" onClick={close}>取消</button>
          <button type="button" className="studio-picker-confirm mp-btn-confirm" disabled={!draft} onClick={confirm}>使用此模型</button>
        </div>
      </div>
    </dialog>
  </div>
}
