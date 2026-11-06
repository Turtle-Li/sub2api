import React from 'react'
// InputBar rendering adapted from 88lin 5523a592 lines 1815–2050, MIT.
import type { BatchImageModel, BatchImageModelCapabilities } from '@/api/batchImage'
import { CloseIcon } from './icons'
interface Props {
 readonly capabilities?: BatchImageModelCapabilities; readonly imageSize: string; readonly aspectRatio: string;
 readonly setImageSize: (size: string) => void; readonly setAspectRatio: (ratio: string) => void; readonly referenceError: string;
 refs: string[]; removeRef: (index: number) => void; prompt: string; setPrompt: (text: string) => void;
 editing: boolean; upload: (files: FileList | null) => void; models: BatchImageModel[];
 model: string; setModel: (model: string) => void; busy: boolean; submit: () => void;
}
export default function InputBar({ refs, removeRef, prompt, setPrompt, editing, upload, models, model, setModel, busy, submit, capabilities, imageSize, aspectRatio, setImageSize, setAspectRatio, referenceError }: Props) {
 return <section data-input-bar className="studio-input">
  <div className="bg-white/70 dark:bg-gray-900/70 backdrop-blur-2xl border border-white/50 dark:border-white/[0.08] shadow-[0_8px_30px_rgb(0,0,0,0.08)] dark:shadow-[0_8px_30px_rgb(0,0,0,0.3)] rounded-2xl sm:rounded-3xl p-3 sm:p-4 ring-1 ring-black/5 dark:ring-white/10">
   <div className="studio-references">{refs.map((src, index) => <button key={index} onClick={() => removeRef(index)} aria-label={`移除参考图 ${index + 1}`}><img src={src} alt={`参考图 ${index + 1}`} /><span>移除</span></button>)}</div>
   <div className="relative grid">
    <textarea data-prompt-input aria-label="画面描述" className="col-start-1 row-start-1 min-h-[42px] w-full overflow-hidden ios-rounded-scroll-fix whitespace-pre-wrap break-words rounded-2xl border border-gray-200/60 bg-white/50 pl-4 pr-10 py-3 text-sm leading-relaxed shadow-sm outline-none transition-[border-color,box-shadow] duration-200 focus:ring-1 focus:ring-blue-300/40 dark:border-white/[0.08] dark:bg-white/[0.03] dark:text-gray-100 dark:focus:ring-blue-500/30" placeholder={editing ? '描述你想怎样修改这张图…' : '描述想要的画面。批量提示词用单独一行 --- 分隔。'} value={prompt} onChange={event => setPrompt(event.target.value)} />
    {prompt.length > 0 && <button type="button" className="studio-clear absolute right-3 top-3" onClick={() => setPrompt('')} aria-label="清空文本"><CloseIcon className="w-3.5 h-3.5" /></button>}
   </div>
   <div className="mt-3 flex items-center justify-between gap-3 flex-wrap">
    <select aria-label="图片模型" value={model} onChange={event => setModel(event.target.value)}>{!models.length && <option value="">此分组没有可用生图模型</option>}{models.map(item => <option key={`${item.provider}:${item.id}`} value={item.id}>{item.id}</option>)}</select>
    {capabilities && <div className="flex items-center gap-2 flex-wrap">
     {capabilities.image_sizes.length > 0 && <label className="studio-muted">尺寸 <select aria-label="图片尺寸" className="studio-parameter" value={imageSize} onChange={event => setImageSize(event.target.value)}>{capabilities.image_sizes.map(size => <option key={size} value={size}>{size}</option>)}</select></label>}
     {capabilities.aspect_ratios.length > 0 && <label className="studio-muted">比例 <select aria-label="图片比例" className="studio-parameter" value={aspectRatio} onChange={event => setAspectRatio(event.target.value)}>{capabilities.aspect_ratios.map(ratio => <option key={ratio} value={ratio}>{ratio}</option>)}</select></label>}
    </div>}
    <div className="flex gap-2 flex-shrink-0">
     <label className="studio-upload" aria-label="添加参考图"><svg width="20" height="20" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeWidth="2" d="M12 5v14M5 12h14" /></svg><input aria-label="添加参考图" type="file" multiple accept="image/png,image/jpeg,image/webp" onChange={event => { upload(event.target.files); event.target.value = '' }} /></label>
     <button className="studio-submit p-2.5 rounded-xl" aria-label={editing ? '生成修改图' : '开始生成'} disabled={busy || !model || !prompt.trim() || Boolean(referenceError)} onClick={submit}>{busy ? '提交中…' : <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 7l5 5m0 0l-5 5m5-5H6" /></svg>}</button>
    </div>
   </div>
   {referenceError && <p role="alert" className="studio-error">{referenceError}</p>}
   {capabilities && <p className="studio-muted">最多 {capabilities.max_reference_images} 张参考图。尺寸与比例为生成目标，实际尺寸以下载图片为准。</p>}
   <p className="studio-muted mt-2">每条提示词生成一张，按所选分组计费。</p>
  </div>
 </section>
}
