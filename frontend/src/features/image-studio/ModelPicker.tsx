import React, { useRef, useState } from 'react'
import type { BatchImageModel } from '@/api/batchImage'
import { modelIdentity } from './workspaceAPI'

const platforms: Readonly<Record<string, string>> = {
  gemini_api: 'Google Gemini', vertex: 'Google Vertex', openai_images: 'OpenAI',
  wan_images: '通义万相', seedream_images: '豆包 Seedream',
  muse_images: 'Muse', workbuddy_images: '腾讯混元',
}
export const modelPlatform = (provider: string) => platforms[provider] || provider

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

export function ModelPicker({ models, value, size, disabled, loading = false, onChange }: {
  readonly models: readonly BatchImageModel[]; readonly value: string; readonly size: string
  readonly disabled: boolean; readonly loading?: boolean; readonly onChange: (value: string) => void
}) {
  const dialog = useRef<HTMLDialogElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [platform, setPlatform] = useState('')
  const options = pickerModels(models)
  const original = models.find(model => modelIdentity(model) === value)
  const selected = original && !options.includes(original) ? options.find(model => model.provider === original.provider && model.id === original.id.replace(/-preview$/, '')) : original
  const query = search.trim().toLowerCase()
  const groups = new Map<string, BatchImageModel[]>()
  for (const model of options) {
    const name = modelPlatform(model.provider)
    if (query && !`${name} ${model.id}`.toLowerCase().includes(query)) continue
    const existing = groups.get(name) || []
    existing.push(model); groups.set(name, existing)
  }
  const active = groups.has(platform) ? platform : groups.keys().next().value
  const visible = active ? groups.get(active) || [] : []
  const selectedBadge = selected ? modelBadge(selected.provider, selected.id) : null
  function close() { dialog.current?.close(); setOpen(false); trigger.current?.focus() }
  return <div className="studio-model-picker">
    <span className="studio-field-label" id="studio-model-label">图片模型</span>
    {/* Fixed-height, single-line trigger: long model IDs must not change the rail height. */}
    <button ref={trigger} type="button" aria-label="图片模型" aria-haspopup="dialog" aria-expanded={open} title={selectedBadge?.title} className="studio-model-trigger" disabled={disabled || !models.length} onClick={() => {
      setSearch(''); setPlatform(selected ? modelPlatform(selected.provider) : '')
      setOpen(true); dialog.current?.showModal(); dialog.current?.querySelector<HTMLInputElement>('input')?.focus()
    }}>
      <span className="studio-model-mark" data-platform={selectedBadge?.key || 'generic'} aria-hidden="true">{selectedBadge?.mark || 'AI'}</span>
      <span className="studio-model-copy">
        <strong>{selectedBadge?.label || (loading ? '正在加载模型…' : '暂无可用模型')}</strong>
        <small>{selected ? `${modelPlatform(selected.provider)} · ${selected.id}` : '生图模型'}</small>
      </span>
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" aria-hidden="true"><path d="m7 10 5 5 5-5" /></svg>
    </button>
    <div className="studio-model-current-price" aria-live="polite"><span>{size || selected?.capabilities?.default_image_size || '1K'} 分辨率{selected && loading ? ' · 更新中' : ''}</span><span><strong>{!selected && loading ? '正在读取价格…' : modelPrice(selected, size)}</strong>{modelPrice(selected, size) !== '价格待配置' && ' 额度 / 张'}</span></div>
    <dialog ref={dialog} className="studio-model-dialog" aria-label="选择图片模型" onCancel={event => { event.preventDefault(); close() }} onClose={() => { setOpen(false); trigger.current?.focus() }} onClick={event => { if (event.target === event.currentTarget) close() }}>
      <div className="studio-model-dialog-heading"><h2>选择图片模型</h2><button type="button" aria-label="关闭模型选择" onClick={close}><svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" aria-hidden="true"><path d="m6 6 12 12M6 18 18 6" /></svg></button></div>
      <input type="search" autoFocus aria-label="搜索生图模型" placeholder="搜索平台或模型" value={search} onChange={event => setSearch(event.target.value)} />
      <div className="studio-model-browser">
        <nav className="studio-model-platforms" aria-label="模型平台">{[...groups].map(([name, entries]) => <button key={name} type="button" aria-pressed={active === name} onClick={() => setPlatform(name)}><span>{name}</span><small>{entries.length}</small></button>)}</nav>
        <div className="studio-model-options" role="listbox" aria-label="可用生图模型" onKeyDown={event => {
          if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
          const options = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="option"]'))
          const index = options.findIndex(option => option === document.activeElement)
          const next = event.key === 'Home' ? 0 : event.key === 'End' ? options.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length
          event.preventDefault(); options[next]?.focus()
        }}>
          {!visible.length && <p className="studio-model-empty">没有匹配的模型，试试其他名称。</p>}
          {visible.map(model => {
            const optionSize = model.capabilities?.image_sizes.includes(size) ? size : model.capabilities?.default_image_size || size || '1K'
            const price = modelPrice(model, optionSize)
            const badge = modelBadge(model.provider, model.id)
            return <button type="button" role="option" aria-selected={selected && modelIdentity(model) === modelIdentity(selected)} key={modelIdentity(model)} className="studio-model-option" title={badge.title} onClick={() => { onChange(modelIdentity(model)); close() }}>
            <span className="studio-model-mark" data-platform={badge.key} aria-hidden="true">{badge.mark}</span>
            <span className="studio-model-option-copy"><strong>{model.id}</strong><small>{modelPlatform(model.provider)}{model.capabilities?.max_reference_images ? ` · 最多 ${model.capabilities.max_reference_images} 张参考图` : ''}</small></span>
            <span className="studio-model-option-price"><strong>{price}</strong><small>{price === '价格待配置' ? `${optionSize} 分辨率` : `${optionSize} · 额度 / 张`}</small></span>
            <svg className="studio-model-check" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true"><path d="m5 12 4 4L19 6" /></svg>
          </button>})}
        </div>
      </div>
      <p className="studio-model-footnote">价格随分辨率更新，消耗以任务结算为准。</p>
    </dialog>
  </div>
}
