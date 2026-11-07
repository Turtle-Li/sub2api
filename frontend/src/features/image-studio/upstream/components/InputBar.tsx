import React from 'react'
// InputBar rendering adapted from 88lin 5523a592 lines 1815–2050, MIT.
import type { BatchImageModel, BatchImageModelCapabilities } from '@/api/batchImage'
import { CloseIcon } from './icons'
import { modelIdentity } from '../../workspaceAPI'
import { ModelPicker, modelPrice } from '../../ModelPicker'
interface Props {
 readonly capabilities?: BatchImageModelCapabilities; readonly imageSize: string; readonly aspectRatio: string;
 readonly setImageSize: (size: string) => void; readonly setAspectRatio: (ratio: string) => void; readonly referenceError: string;
 refs: string[]; removeRef: (index: number) => void; prompt: string; setPrompt: (text: string) => void;
 editing: boolean; upload: (files: FileList | null) => void; models: BatchImageModel[];
 model: string; setModel: (model: string) => void; busy: boolean; submit: () => void;
 batchMode: boolean; setBatchMode: (batch: boolean) => void;
}
export default function InputBar({ refs, removeRef, prompt, setPrompt, editing, upload, models, model, setModel, busy, submit, capabilities, imageSize, aspectRatio, setImageSize, setAspectRatio, referenceError, batchMode, setBatchMode }: Props) {
 const count = batchMode && !editing ? prompt.split(/\n\s*---\s*\n/).filter(text => text.trim()).length : prompt.trim() ? 1 : 0
 const selected = models.find(item => modelIdentity(item) === model)
 const estimate = modelPrice(selected, imageSize, Math.max(1, count))
 return <section data-input-bar className="studio-input">
  <div className="studio-input-fields">
   <ModelPicker models={models} value={model} size={imageSize} disabled={busy} onChange={setModel} />
   {!models.length && <p className="studio-muted">管理员配置可用模型后，即可在这里开始创作。</p>}
   {!editing && <div className="studio-tabs studio-mode" aria-label="创作模式"><button disabled={busy} aria-pressed={!batchMode} onClick={() => setBatchMode(false)}>单张创作</button><button disabled={busy} aria-pressed={batchMode} onClick={() => setBatchMode(true)}>批量创作</button></div>}
   <div className="studio-prompt-heading"><label htmlFor="studio-prompt">{editing ? '修改描述' : '画面描述'}</label><span className="studio-muted">{prompt.length} 字</span></div>
   <div className="studio-prompt-field">
    <textarea id="studio-prompt" data-prompt-input aria-label="画面描述" placeholder={editing ? '描述你想怎样修改这张图…' : batchMode ? '每段描述生成一张图片，用单独一行 --- 分隔。' : '描述画面主体、风格、构图和光线…'} value={prompt} onChange={event => setPrompt(event.target.value)} />
    {prompt.length > 0 && <button type="button" className="studio-clear absolute right-3 top-3" onClick={() => setPrompt('')} aria-label="清空文本"><CloseIcon className="w-3.5 h-3.5" /></button>}
   </div>
   {batchMode && !editing && <p className="studio-muted">用单独一行 --- 分隔提示词，每段生成一张，共 {count} 张。</p>}
   <div className="studio-prompt-heading"><span>参考图 <span className="studio-muted">可选</span></span><span className="studio-muted">{refs.length}{capabilities ? ` / ${capabilities.max_reference_images}` : ''}</span></div>
   <div className="studio-references">{refs.map((src, index) => <button key={index} onClick={() => removeRef(index)} aria-label={`移除参考图 ${index + 1}`}><img src={src} alt={`参考图 ${index + 1}`} /><span>移除</span></button>)}</div>
   <label className="studio-upload"><svg width="20" height="20" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true"><path strokeLinecap="round" strokeWidth="1.6" d="M12 5v14M5 12h14" /></svg><span>添加参考图<small>PNG、JPG、WebP · 每张不超过 8 MB</small></span><input aria-label="添加参考图" type="file" multiple accept="image/png,image/jpeg,image/webp" disabled={capabilities?.max_reference_images === 0} onChange={event => { upload(event.target.files); event.target.value = '' }} /></label>
   {capabilities && <div className="studio-parameters">
    {capabilities.aspect_ratios.length > 0 && <label className="studio-field"><span>画面比例</span><select aria-label="图片比例" value={aspectRatio} onChange={event => setAspectRatio(event.target.value)}>{capabilities.aspect_ratios.map(ratio => <option key={ratio} value={ratio}>{ratio}</option>)}</select></label>}
    {capabilities.image_sizes.length > 0 && <label className="studio-field"><span>分辨率</span><select aria-label="图片尺寸" value={imageSize} onChange={event => setImageSize(event.target.value)}>{capabilities.image_sizes.map(size => <option key={size} value={size}>{size}</option>)}</select></label>}
   </div>}
   {referenceError && <p role="alert" className="studio-error">{referenceError}</p>}
   {capabilities && <p className="studio-muted">尺寸与比例为生成目标，实际尺寸以下载图片为准。</p>}
  </div>
  <div className="studio-submit-area">
   <div className="studio-estimate"><span>预计消耗{count > 1 ? ` · ${count} 张` : ''}</span><strong>{estimate}{estimate !== '价格待配置' && ' 额度'}</strong></div>
   <p className="studio-muted">按模型价格预估，以任务结算为准</p>
   <button className="studio-submit" aria-label={editing ? '生成修改图' : '开始生成'} disabled={busy || !model || !prompt.trim() || Boolean(referenceError) || count === 0} onClick={submit}>{busy ? '正在提交…' : editing ? '生成修改图' : `开始生成${count > 1 ? ` · ${count} 张` : ''}`}<svg width="18" height="18" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 7l5 5m0 0l-5 5m5-5H6" /></svg></button>
   </div>
 </section>
}
