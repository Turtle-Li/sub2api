import React, { useState } from 'react'
// InputBar rendering adapted from 88lin 5523a592 lines 1815–2050, MIT.
import type { BatchImageModel, BatchImageModelCapabilities } from '@/api/batchImage'
import { CloseIcon } from './icons'
import { modelIdentity } from '../../workspaceAPI'
import { ModelPicker, modelPrice } from '../../ModelPicker'
import { LiquidSelect } from '@/features/studio-ui/LiquidSelect'
interface Props {
 readonly capabilities?: BatchImageModelCapabilities; readonly imageSize: string; readonly aspectRatio: string;
 readonly setImageSize: (size: string) => void; readonly setAspectRatio: (ratio: string) => void; readonly referenceError: string;
 refs: string[]; removeRef: (index: number) => void; prompt: string; setPrompt: (text: string) => void;
 editing: boolean; upload: (files: FileList | null) => void; models: BatchImageModel[];
 model: string; modelLoading?: boolean; setModel: (model: string) => void; busy: boolean; submitDisabled?: boolean; submit: () => void;
 batchMode: boolean; setBatchMode: (batch: boolean) => void;
}
// Model-dependent controls keep fixed slots so switching models never shifts the rail.
function FixedField({ label, text }: { readonly label: string; readonly text: string }) {
 return <div className="studio-field wb-field"><span className="wb-label">{label}</span><div className="studio-field-static" aria-disabled="true">{text}</div></div>
}
export default function InputBar({ refs, removeRef, prompt, setPrompt, editing, upload, models, modelLoading, model, setModel, busy, submitDisabled, submit, capabilities, imageSize, aspectRatio, setImageSize, setAspectRatio, referenceError, batchMode, setBatchMode }: Props) {
 const [expandedPrompt, setExpandedPrompt] = useState(false)
 const count = batchMode && !editing ? prompt.split(/\n\s*---\s*\n/).filter(text => text.trim()).length : prompt.trim() ? 1 : 0
 const selected = models.find(item => modelIdentity(item) === model)
 const estimate = modelPrice(selected, imageSize, Math.max(1, count))
 const maxRefs = capabilities?.max_reference_images
 return <section data-input-bar className="studio-input">
  <div className="studio-input-fields wb-composer-body wb-field-stack">
   <div className="studio-model-group">
    <ModelPicker models={models} value={model} size={imageSize} disabled={busy} loading={modelLoading} onChange={setModel} />
    <div className="studio-parameters">
     {capabilities?.aspect_ratios.length ? <div className="studio-field wb-field"><span className="wb-label">画面比例</span><LiquidSelect ariaLabel="图片比例" value={aspectRatio} options={capabilities.aspect_ratios.map(ratio => ({ value: ratio, label: ratio }))} onChange={setAspectRatio} /></div> : <FixedField label="画面比例" text="模型默认" />}
     {capabilities?.image_sizes.length ? <div className="studio-field wb-field"><span className="wb-label">分辨率</span><LiquidSelect ariaLabel="图片尺寸" value={imageSize} options={capabilities.image_sizes.map(size => ({ value: size, label: size }))} onChange={setImageSize} /></div> : <FixedField label="分辨率" text="模型默认" />}
    </div>
   </div>
   {!models.length && <p className="studio-muted">管理员配置可用模型后，即可在这里开始创作。</p>}
   {!editing && <div className="studio-tabs studio-mode" aria-label="创作模式"><button disabled={busy} aria-pressed={!batchMode} onClick={() => setBatchMode(false)}>单张创作</button><button disabled={busy} aria-pressed={batchMode} onClick={() => setBatchMode(true)}>批量创作</button></div>}
   <div className="studio-prompt-heading wb-label-row"><label className="wb-label" htmlFor="studio-prompt">{editing ? '修改描述' : '画面描述'}</label><span className="studio-prompt-tools">{prompt.length > 80 && <button type="button" className="studio-prompt-expand" onClick={() => setExpandedPrompt(v => !v)}>{expandedPrompt ? '收起' : '展开编辑'}</button>}<span className="studio-muted wb-label-meta">{prompt.length} 字</span></span></div>
   <div className={`studio-prompt-field${expandedPrompt ? ' is-expanded' : ''}`}>
    <textarea id="studio-prompt" data-prompt-input aria-label="画面描述" placeholder={editing ? '描述你想怎样修改这张图…' : batchMode ? '每段描述生成一张图片，用单独一行 --- 分隔。' : '描述画面主体、风格、构图和光线…'} value={prompt} onChange={event => setPrompt(event.target.value)} />
    {prompt.length > 0 && <button type="button" className="studio-clear absolute right-3 top-3" onClick={() => setPrompt('')} aria-label="清空文本"><CloseIcon className="w-3.5 h-3.5" /></button>}
   </div>
   {batchMode && !editing && <p className="studio-muted">用单独一行 --- 分隔提示词，每段生成一张，共 {count} 张。</p>}
   <div className="studio-prompt-heading wb-label-row"><span className="wb-label">参考图 <span className="studio-muted wb-label-meta">可选</span></span><span className={referenceError ? 'studio-ref-count is-over' : 'studio-muted wb-label-meta'}>{refs.length}{capabilities ? ` / ${maxRefs}` : ''}</span></div>
   <div className="studio-references">{refs.map((src, index) => <button key={index} onClick={() => removeRef(index)} aria-label={`移除参考图 ${index + 1}`}><img src={src} alt={`参考图 ${index + 1}`} /><span>移除</span></button>)}</div>
   <label className="studio-upload" data-disabled={maxRefs === 0 || undefined}><svg width="20" height="20" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true"><path strokeLinecap="round" strokeWidth="1.6" d="M12 5v14M5 12h14" /></svg><span>添加参考图<small>{maxRefs === 0 ? '当前模型不支持参考图' : 'PNG、JPG、WebP · 每张不超过 8 MB'}</small></span><input aria-label="添加参考图" type="file" multiple accept="image/png,image/jpeg,image/webp" disabled={maxRefs === 0} onChange={event => { upload(event.target.files); event.target.value = '' }} /></label>
   {referenceError ? <p role="alert" className="studio-input-hint is-error">{referenceError}</p> : <p className="studio-input-hint studio-muted">{capabilities ? '尺寸与比例为生成目标，实际尺寸以下载图片为准。' : '该模型使用服务端默认尺寸与比例。'}</p>}
  </div>
  <div className="studio-submit-area wb-composer-foot">
   <div className="studio-estimate"><span>预计消耗{count > 1 ? ` · ${count} 张` : ''}</span><strong>{estimate}{estimate !== '价格待配置' && ' 额度'}</strong></div>
   <p className="studio-muted">按模型价格预估，以任务结算为准</p>
   <button className="studio-submit" aria-label={editing ? '生成修改图' : '开始生成'} disabled={busy || submitDisabled || !model || !prompt.trim() || Boolean(referenceError) || count === 0} onClick={submit}>{busy ? '正在提交…' : editing ? '生成修改图' : `开始生成${count > 1 ? ` · ${count} 张` : ''}`}<svg width="18" height="18" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 7l5 5m0 0l-5 5m5-5H6" /></svg></button>
   </div>
 </section>
}
