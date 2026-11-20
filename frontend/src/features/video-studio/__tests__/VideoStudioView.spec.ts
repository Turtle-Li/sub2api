import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick, reactive } from 'vue'
import VideoStudioView from '@/views/user/VideoStudioView.vue'
import VideoComposer from '@/features/video-studio/VideoComposer.vue'
import type { VideoJob, VideoModel, VideoSubmission } from '@/features/video-studio/api'
import type { VideoSource } from '@/features/video-studio/source'

const { video, sources } = vi.hoisted(() => ({
  video: { models: vi.fn(), list: vi.fn(), get: vi.fn(), content: vi.fn(), poster: vi.fn(), create: vi.fn() },
  sources: { pendingImage: vi.fn(), readImage: vi.fn(), clearImage: vi.fn() },
}))
vi.mock('@/features/video-studio/api', async importOriginal => ({ ...await importOriginal<typeof import('@/features/video-studio/api')>(), videoStudio: video }))
vi.mock('@/features/video-studio/source', () => sources)
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
const auth = reactive<{ user: { id: number } | null; isAuthenticated: boolean }>({ user: { id: 11 }, isAuthenticated: true })
const h3: VideoModel = { id: 'minimax/h3', label: 'H3', provider: 'goodput', min_seconds: 4, max_seconds: 15, default_seconds: 5,
  sizes: ['1344x768', '768x1344'], supports_frames: true, supports_seed: false, max_references: 2,
  quotes: Object.fromEntries(Array.from({ length: 12 }, (_, n) => [n + 4, (n + 4) * .25])), currency: 'USD', quote_id: 'a'.repeat(64) }
const muse: VideoModel = { ...h3, id: 'muse-video', label: 'Muse', provider: 'muse', min_seconds: 6, max_seconds: 6, default_seconds: 6,
  supports_frames: false, sizes: ['16:9', '9:16'], currency: 'CNY', quote_id: 'b'.repeat(64) }
const museRich: VideoModel = {
  ...muse, duration_mode: 'automatic', max_references: 10, reference_kinds: ['image', 'video', 'audio', 'file'],
  muse_options: { resolution_max_bytes: 64, extra_max_bytes: 4096, timeout_max: 600, native_size_max_bytes: 64 },
}
const fast: VideoModel = { ...h3, id: 'minimax/h3-fast', label: 'H3 Fast', supports_seed: true, max_seconds: 15, quote_id: 'c'.repeat(64) }
const id = '126b7cb9-a15a-4fd1-b580-daedb64b9ab2'
const job = (other: Partial<VideoJob> = {}): VideoJob => ({ id, model: h3.id, status: 'queued', seconds: 5, size: '1344x768', created_at: '2026-11-20T18:22:01Z', ...other })
const png = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADElEQVR42mP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC'
const pngFile = (name = '真实图.png') => new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13, 0x49, 0x48, 0x44, 0x52])], name, { type: 'image/png' })
const pdfFile = (name = '脚本文档.pdf') => new File([new TextEncoder().encode('%PDF-1.4\n1 0 obj\n<<>>\nendobj\n')], name, { type: 'application/pdf' })
const mp4File = (name = '参考片段.mp4') => new File([new Uint8Array([0, 0, 0, 0x18, 0x66, 0x74, 0x79, 0x70, 0x69, 0x73, 0x6f, 0x6d, 0, 0, 0, 1])], name, { type: 'video/mp4' })
const jpg = new File([new Uint8Array([255, 216, 255, 224, 0, 16, 74, 70, 73, 70, 0, 1])], '结尾.jpg', { type: 'image/jpeg' })
const source: VideoSource = Object.freeze({ userId: 11, label: '生图作品 · 第 1 张' })
let current: ReturnType<typeof mount> | undefined
let desktop = true
function mountPage() { current = mount(VideoStudioView, { attachTo: document.body }); return current }
const get = (css: string) => current!.get(css)
function button(label: string, area = 'body') {
  const element = [...document.querySelectorAll<HTMLButtonElement>(`${area} button`)].find(b => b.textContent?.trim() === label || b.getAttribute('aria-label') === label)
  if (!element) throw new Error(`缺少按钮：${label}`)
  return element
}
const form = () => get('.vs-form')
async function submit() { await form().trigger('submit'); await flushPromises() }
async function describeShot() { await get('textarea').setValue('镜头拉远，露出山谷。') }
const generate = () => get('button[type=submit]').element as HTMLButtonElement
async function click(label: string, area?: string) { button(label, area).click(); await flushPromises() }
async function chooseModel(label: string) {
  await get('[aria-label="选择视频模型与渠道"]').trigger('click')
  await nextTick()
  const option = [...document.querySelectorAll<HTMLButtonElement>('.vs-model-dialog [role="option"]')]
    .find(node => node.querySelector('strong')?.textContent?.trim() === label)
  if (!option) throw new Error(`缺少模型选项：${label}`)
  option.click()
  await nextTick()
  button('使用此模型与渠道', '.vs-model-dialog').click()
  await flushPromises()
}
async function upload(label: string, file: File) {
  const input = get(`input[aria-label="${label}"]`)
  Object.defineProperty(input.element, 'files', { configurable: true, value: [file] })
  await input.trigger('change'); await vi.waitFor(() => expect(document.body.textContent).not.toContain('正在检查素材…'))
}
const modalShow = vi.fn(function (this: HTMLDialogElement) { this.setAttribute('open', '') })
const modalClose = vi.fn(function (this: HTMLDialogElement) { this.removeAttribute('open'); this.dispatchEvent(new Event('close')) })
let createdUrl: ReturnType<typeof vi.fn>
let revokedUrl: ReturnType<typeof vi.fn>
beforeEach(() => {
  vi.resetAllMocks(); sessionStorage.clear(); desktop = true; auth.isAuthenticated = true; auth.user = { id: 11 }
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  Object.defineProperty(window, 'matchMedia', { configurable: true, value: (media: string) => ({ media, matches: media.includes('821px') && desktop, addEventListener() {}, removeEventListener() {} }) })
  vi.stubGlobal('IntersectionObserver', class {
    constructor(private readonly callback: IntersectionObserverCallback) {}
    observe(target: Element) { this.callback([{ target, isIntersecting: true } as IntersectionObserverEntry], this as unknown as IntersectionObserver) }
    unobserve() {}
    disconnect() {}
  })
  Object.defineProperties(HTMLDialogElement.prototype, { showModal: { configurable: true, value: modalShow }, close: { configurable: true, value: modalClose } })
  modalShow.mockImplementation(function () { this.setAttribute('open', '') }); modalClose.mockImplementation(function () { this.removeAttribute('open'); this.dispatchEvent(new Event('close')) })
  createdUrl = vi.fn((_blob: Blob) => `blob:studio-${createdUrl.mock.calls.length}`); revokedUrl = vi.fn()
  Object.defineProperties(URL, { createObjectURL: { configurable: true, value: createdUrl }, revokeObjectURL: { configurable: true, value: revokedUrl } })
  sources.pendingImage.mockReturnValue(null); video.models.mockResolvedValue({ enabled: true, items: [h3, muse, fast] }); video.list.mockResolvedValue({ items: [] })
  video.create.mockImplementation(async (body: VideoSubmission) => job({ id: body.request_id, model: body.model, seconds: body.seconds ?? 6 }))
  video.get.mockImplementation(async (key: string) => job({ id: key }))
})
afterEach(() => { current?.unmount(); current = undefined; document.body.replaceChildren(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers() })

describe('创作栏：能力、素材和原图归属', () => {
  it('打开只读目录和元数据；示例填入草稿；真实币种与时长报价联动（Muse 自动时长无滑块）', async () => {
    mountPage(); await flushPromises()
    expect(current!.text()).toContain('还没有视频作品'); expect(current!.text()).toContain('最高冻结1.25 USD')
    expect(video.create).not.toHaveBeenCalled(); expect(video.get).not.toHaveBeenCalled(); expect(video.content).not.toHaveBeenCalled()
    await click('使用示例描述'); expect((get('textarea').element as HTMLTextAreaElement).value).toContain('蒸汽')
    await get('[aria-label="请求时长"]').setValue('7'); expect(current!.text()).toContain('1.75 USD')
    await chooseModel('Muse'); expect(current!.text()).toContain('预计消耗1.5 CNY')
    expect(current!.find('[aria-label="请求时长"]').exists()).toBe(false)
    expect(current!.text()).toContain('渠道自动'); expect(current!.text()).toContain('按当前渠道默认档报价')
    expect(current!.find('[aria-label="上传首帧"]').exists()).toBe(false); expect(current!.find('[aria-label="上传参考图 1"]').exists()).toBe(true)
    expect(video.create).not.toHaveBeenCalled()
  })
  it('只在付费确认之后按原授权读取原图；两张帧切到 Muse 必须同意，取消不切模型，提交省略 seconds 且可调整顺序', async () => {
    sources.pendingImage.mockReturnValue(source); sources.readImage.mockResolvedValue(png)
    mountPage(); await flushPromises(); expect(current!.text()).toContain(source.label)
    await upload('上传尾帧', jpg)
    await chooseModel('Muse'); expect(get('.vs-model-trigger').text()).toContain('H3')
    expect(generate().disabled).toBe(true); await click('取消切换'); expect(get('.vs-model-trigger').text()).toContain('H3')
    expect(current!.find('[aria-label="替换尾帧"]').exists()).toBe(true)
    await chooseModel('Muse'); await click('确认改作参考图'); await click('交换参考图顺序')
    expect(sources.readImage).not.toHaveBeenCalled(); expect(video.create).not.toHaveBeenCalled()
    await submit(); await vi.waitFor(() => expect(video.create).toHaveBeenCalledTimes(1))
    expect(sources.readImage).toHaveBeenCalledWith(source, expect.any(AbortSignal))
    const body: VideoSubmission = video.create.mock.calls[0][0]
    expect(body.references).toEqual([expect.stringMatching(/^data:image\/jpeg;base64,/), png]); expect(body.model).toBe(muse.id)
    expect('seconds' in body).toBe(false); expect(body.seconds).toBeUndefined()
    expect(body.first_frame).toBeUndefined(); expect(body.last_frame).toBeUndefined(); expect(revokedUrl).toHaveBeenCalledTimes(1)
  })
  it('H3 不允许只有尾帧；重新放入首帧后只提交两个帧字段并保留整数 seconds', async () => {
    sources.pendingImage.mockReturnValue(source); mountPage(); await flushPromises(); await upload('上传尾帧', jpg)
    await get('[aria-label="移除首帧"]').trigger('click'); expect(generate().disabled).toBe(true); expect(current!.text()).toContain('尾帧需要搭配首帧')
    await submit(); expect(video.create).not.toHaveBeenCalled(); await upload('上传首帧', jpg); await submit()
    await vi.waitFor(() => expect(video.create).toHaveBeenCalledTimes(1)); expect(video.create.mock.calls[0][0]).toMatchObject({ first_frame: expect.any(String), last_frame: expect.any(String), seconds: 5 })
    expect(video.create.mock.calls[0][0].references).toBeUndefined(); expect(sources.readImage).not.toHaveBeenCalled()
  })
  it('Muse 转 H3 可显式保留参考模式；H3 不混发帧字段并发送整数 seconds', async () => {
    video.models.mockResolvedValue({ enabled: true, items: [{ ...muse, max_references: 9 }, h3] }); sources.pendingImage.mockReturnValue(source); sources.readImage.mockResolvedValue(png)
    mountPage(); await flushPromises(); expect(current!.findAll('input[type=file]')).toHaveLength(2)
    await chooseModel('H3'); await click('沿用参考图'); expect(get('.vs-model-trigger').text()).toContain('H3')
    await submit(); const body: VideoSubmission = video.create.mock.calls[0][0]; expect(body.references).toEqual([png]); expect(body.seconds).toBe(5); expect(body.first_frame).toBeUndefined(); expect(body.last_frame).toBeUndefined()
  })
  it('带入图片本身合规、但加上提示词后 H3 请求超过 8 MiB 时仍不 POST', async () => {
    const head = 'iVBORw0KGgoA'
    sources.pendingImage.mockReturnValue(source); sources.readImage.mockResolvedValue('data:image/png;base64,' + head + 'A'.repeat(8 * 1024 * 1024 - 1000 - head.length))
    mountPage(); await flushPromises(); await get('textarea').setValue('镜'.repeat(1000)); await submit()
    expect(current!.text()).toContain('编码后的请求超过 8 MiB'); expect(sources.readImage).toHaveBeenCalledTimes(1); expect(video.create).not.toHaveBeenCalled()
  })
  it('Fast 即使拿到 15 秒也不出售；种子校验仅限 Fast', async () => {
    mountPage(); await flushPromises(); await describeShot(); expect(current!.find('[aria-label="随机种子"]').exists()).toBe(false)
    await chooseModel('H3 Fast')
    const slider = get('input[type=range][aria-label="请求时长"]')
    expect(slider.attributes('min')).toBe('5'); expect(slider.attributes('max')).toBe('14')
    await get('[aria-label="随机种子"]').setValue('2147483648'); expect(generate().disabled).toBe(true)
    await get('[aria-label="随机种子"]').setValue('0'); await submit(); expect(video.create.mock.calls[0][0]).toMatchObject({ seed: 0, model: fast.id, seconds: 5 })
  })
  it.each([
    ['非图像', new File(['x'], '说明.txt', { type: 'text/plain' }), 'PNG、JPEG 或 WebP'],
    ['伪装 PNG', new File(['not png'], '错误.png', { type: 'image/png' }), '原图数据无效'],
    ['超过编码预算', new File([new Uint8Array(6 * 1024 * 1024)], '大图.jpg', { type: 'image/jpeg' }), 'H3 整份请求'],
    ['超过单张上限', new File([new Uint8Array(8 * 1024 * 1024 + 1)], '大图.png', { type: 'image/png' }), '单张图片超过'],
  ] as const)('在浏览器阻止%s；不得降级为纯文字生成', async (_, file, message) => {
    mountPage(); await flushPromises(); await describeShot(); await upload('上传首帧', file)
    expect(current!.text()).toContain(message); expect(generate().disabled).toBe(true); await submit(); expect(video.create).not.toHaveBeenCalled()
  })
  it('Muse 接受真实 PNG 参考素材（保留切片 MIME），并支持排序且提交不带 seconds', async () => {
    video.models.mockResolvedValue({ enabled: true, items: [museRich, h3] })
    mountPage(); await flushPromises(); await describeShot()
    await upload('上传参考素材 1', pngFile('第一张.png'))
    expect(current!.text()).not.toContain('格式不符'); expect(current!.text()).toContain('第一张.png')
    await upload('上传参考素材 2', jpg)
    await upload('上传参考素材 3', pdfFile('分镜.pdf'))
    await get('[aria-label="前移参考素材 3"]').trigger('click'); await nextTick()
    await submit(); await vi.waitFor(() => expect(video.create).toHaveBeenCalledTimes(1))
    const body: VideoSubmission = video.create.mock.calls[0][0]
    expect('seconds' in body).toBe(false)
    expect(body.references).toEqual([
      expect.stringMatching(/^data:image\/png;base64,/),
      expect.stringMatching(/^data:application\/pdf;base64,/),
      expect.stringMatching(/^data:image\/jpeg;base64,/),
    ])
  })
  it('超过 2 个参考或包含非图片素材时禁止静默转为首尾帧或切换不兼容模型', async () => {
    const h3Media: VideoModel = { ...h3, max_references: 10, reference_kinds: ['image', 'video', 'audio'] }
    video.models.mockResolvedValue({ enabled: true, items: [museRich, h3Media] })
    mountPage(); await flushPromises(); await describeShot()
    await upload('上传参考素材 1', pngFile('图1.png'))
    await upload('上传参考素材 2', pdfFile('说明.pdf'))
    await chooseModel('H3')
    expect(get('.vs-model-trigger').text()).toContain('Muse')
    expect(current!.text()).toContain('未切换模型')
    await get('[aria-label="移除参考素材 2"]').trigger('click'); await nextTick()
    await upload('上传参考素材 2', mp4File('镜头.mp4'))
    await chooseModel('H3')
    expect(get('.vs-model-trigger').text()).toContain('H3')
    await click('首尾帧', '.vs-segment')
    expect(current!.text()).toContain('首尾帧仅支持图片')
    expect(generate().disabled).toBe(true)
  })
  it('Muse 原生画布意图与“渠道自动”在刷新同一报价目录时保持不变，不冲突可正常提交', async () => {
    video.models.mockResolvedValue({ enabled: true, items: [museRich] })
    mountPage(); await flushPromises(); await describeShot()
    await get('[aria-label="原生画布意图"]').setValue('1280x720')
    await submit(); await vi.waitFor(() => expect(video.create).toHaveBeenCalledTimes(1))
    const firstBody: VideoSubmission = video.create.mock.calls[0][0]
    expect(firstBody.native_size).toBe('1280x720'); expect(firstBody.size).toBeUndefined(); expect('seconds' in firstBody).toBe(false)

    await click('另起新草稿'); await describeShot()
    await click('渠道自动', '.vs-choice-grid')
    await submit(); await vi.waitFor(() => expect(video.create).toHaveBeenCalledTimes(2))
    const secondBody: VideoSubmission = video.create.mock.calls[1][0]
    expect(secondBody.size).toBeUndefined(); expect(secondBody.native_size).toBeUndefined(); expect('seconds' in secondBody).toBe(false)
  })
  it('Muse 等待上限禁止 0 和 601；留空省略，1 和 600 有效', async () => {
    video.models.mockResolvedValue({ enabled: true, items: [museRich] })
    mountPage(); await flushPromises(); await describeShot()
    const input = get('[aria-label="原生任务等待上限"]')
    await input.setValue('0'); expect(generate().disabled).toBe(true); expect(current!.text()).toContain('1–600')
    await submit(); expect(video.create).not.toHaveBeenCalled()
    await input.setValue('601'); expect(generate().disabled).toBe(true)
    await input.setValue('1'); expect(generate().disabled).toBe(false)
    await submit(); await vi.waitFor(() => expect(video.create).toHaveBeenCalledTimes(1))
    expect(video.create.mock.calls[0][0].timeout).toBe(1)
    await click('另起新草稿'); await describeShot()
    await input.setValue('600'); await submit(); await vi.waitFor(() => expect(video.create).toHaveBeenCalledTimes(2))
    expect(video.create.mock.calls[1][0].timeout).toBe(600)
    await click('另起新草稿'); await describeShot()
    await input.setValue(''); await submit(); await vi.waitFor(() => expect(video.create).toHaveBeenCalledTimes(3))
    expect('timeout' in video.create.mock.calls[2][0]).toBe(false)
  })
  it('模型选择器聚焦取消按 Enter 不切换模型且回焦；手机端不显示键盘提示', async () => {
    video.models.mockResolvedValue({ enabled: true, items: [museRich, fast] })
    mountPage(); await flushPromises()
    const trigger = get('[aria-label="选择视频模型与渠道"]').element as HTMLButtonElement
    trigger.focus(); trigger.click(); await nextTick()
    expect(current!.find('.vs-picker-kbd').exists()).toBe(true)
    const fastOption = [...document.querySelectorAll<HTMLButtonElement>('.vs-model-dialog [role="option"]')]
      .find(node => node.querySelector('strong')?.textContent?.trim() === 'H3 Fast')!
    fastOption.click(); await nextTick()
    const cancelBtn = button('取消', '.vs-model-dialog')
    cancelBtn.focus()
    const enterEvent = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })
    cancelBtn.dispatchEvent(enterEvent)
    expect(enterEvent.defaultPrevented).toBe(false)
    cancelBtn.click(); await flushPromises()
    expect(get('.vs-model-trigger').text()).toContain('Muse')
    expect(document.activeElement).toBe(trigger)

    current!.unmount(); desktop = false
    mountPage(); await flushPromises()
    await click('展开 ↑')
    await get('[aria-label="选择视频模型与渠道"]').trigger('click'); await nextTick()
    expect(current!.find('.vs-picker-kbd').exists()).toBe(false)
  })
  it('从 H3 切回 Muse 不带入时长；历史 Muse 参数复用也不带入 seconds', async () => {
    video.models.mockResolvedValue({ enabled: true, items: [h3, museRich] })
    mountPage(); await flushPromises(); await describeShot()
    await get('[aria-label="请求时长"]').setValue('10')
    await chooseModel('Muse')
    expect(current!.find('[aria-label="请求时长"]').exists()).toBe(false)
    await submit(); await vi.waitFor(() => expect(video.create).toHaveBeenCalledTimes(1))
    expect('seconds' in video.create.mock.calls[0][0]).toBe(false)

    current!.findComponent(VideoComposer).vm.reuse(job({
      model: museRich.id, seconds: 6, size: '',
      parameters: { material_mode: 'text', reference_count: 0, duration_mode: 'automatic', native_size: '1280x720', timeout: 120 },
    }))
    await flushPromises(); await describeShot()
    expect(current!.find('[aria-label="请求时长"]').exists()).toBe(false)
    await submit(); await vi.waitFor(() => expect(video.create).toHaveBeenCalledTimes(2))
    const reusedBody: VideoSubmission = video.create.mock.calls[1][0]
    expect('seconds' in reusedBody).toBe(false)
    expect(reusedBody.native_size).toBe('1280x720')
    expect(reusedBody.timeout).toBe(120)
  })
  it('新带入的原图打开独立的未付费草稿，仍保留旧意图的查单入口', async () => {
    sessionStorage.setItem('sub2:video-studio:intents:11', JSON.stringify({ held: id, pending: [id] })); sources.pendingImage.mockReturnValue(source)
    mountPage(); await flushPromises(); expect(current!.text()).toContain(source.label); expect(get('.vs-outstanding').text()).toContain(id)
    expect(current!.find('.vs-receipt').exists()).toBe(false); expect(generate().disabled).toBe(false); expect(sources.readImage).not.toHaveBeenCalled(); expect(video.create).not.toHaveBeenCalled()
  })
  it('生图草稿明确提示未提交；清除只释放原图，不读图也不生成', async () => {
    sources.pendingImage.mockReturnValue(source); mountPage(); await flushPromises()
    expect(get('.vs-draft').text()).toContain('尚未提交'); await click('清除草稿')
    expect(current!.find('.vs-draft').exists()).toBe(false); expect(current!.text()).not.toContain(source.label); expect(sources.clearImage).toHaveBeenCalledTimes(1)
    expect(sources.readImage).not.toHaveBeenCalled(); expect(video.create).not.toHaveBeenCalled()
  })
  it('原件授权过期不提交，不使用旧缩略图或站外链接代替', async () => {
    sources.pendingImage.mockReturnValue(source); sources.readImage.mockRejectedValue({ status: 410 }); mountPage(); await flushPromises(); await submit()
    expect(sources.readImage).toHaveBeenCalledTimes(1); expect(current!.text()).toContain('无法读取带入的原图'); expect(video.create).not.toHaveBeenCalled()
  })
})

describe('收费意图与恢复', () => {
  it.each([{ currency: undefined }, { currency: 'EUR' }, { quotes: { '5': 0 } }, { quotes: {} }, { quote_id: '' }])('不接受不完整报价 %j', async (invalid) => {
    video.models.mockResolvedValue({ enabled: true, items: [{ ...h3, ...invalid }] }); mountPage(); await flushPromises(); await describeShot()
    expect(current!.text()).toContain('报价不可用'); expect(generate().disabled).toBe(true); await submit(); expect(video.create).not.toHaveBeenCalled()
  })
  it.each([{ quote_id: 'd'.repeat(64) }, { currency: 'CNY' }, { quotes: { '5': 7.5 } }])('报价标识、币种或金额单独变化均须重新确认 %j', async changed => {
    sources.pendingImage.mockReturnValue(source); sources.readImage.mockResolvedValue(png); mountPage(); await flushPromises()
    video.models.mockResolvedValue({ enabled: true, items: [{ ...h3, ...changed }] }); await submit()
    expect(current!.text()).toContain('再次确认生成'); expect(video.create).not.toHaveBeenCalled(); expect(sources.readImage).not.toHaveBeenCalled()
    await submit(); expect(video.create).toHaveBeenCalledTimes(1); expect(video.create.mock.calls[0][0].quote_id).toBe(changed.quote_id ?? h3.quote_id)
  })
  it('服务器二次拒绝旧报价：更新展示，不跟发 POST', async () => {
    mountPage(); await flushPromises(); await describeShot()
    video.create.mockRejectedValueOnce({ status: 409, message: '视频报价或通道已变化，请刷新并重新确认' }); video.models.mockResolvedValueOnce({ enabled: true, items: [h3] }).mockResolvedValue({ enabled: true, items: [{ ...h3, quotes: { '5': 9 }, currency: 'CNY' }] })
    await submit(); expect(current!.text()).toContain('重新确认'); expect(current!.text()).toContain('9 CNY'); expect(video.create).toHaveBeenCalledTimes(1)
    const old = video.create.mock.calls[0][0].request_id; await submit(); expect(video.create.mock.calls[1][0].request_id).not.toBe(old)
  })
  it.each([400, 401, 402, 403, 413, 415, 422, 429])('明确拒绝 %i 不标记为待核对，也不自动重发', async status => {
    mountPage(); await flushPromises(); await describeShot(); video.create.mockRejectedValue({ status, message: '请求被拒绝' }); await submit()
    expect(current!.text()).toContain('拒绝'); expect(current!.text()).not.toContain('尚未确认的提交'); expect(video.create).toHaveBeenCalledTimes(1)
  })
  it('双击、超时、重载和原号查无记录均不重发；明确分类后才可另起全新 UUID', async () => {
    mountPage(); await flushPromises(); await describeShot(); video.create.mockRejectedValueOnce(new Error('timeout'))
    await Promise.all([form().trigger('submit'), form().trigger('submit')]); await flushPromises()
    expect(video.create).toHaveBeenCalledTimes(1); const old = video.create.mock.calls[0][0].request_id
    expect(old).toMatch(/^[0-9a-f-]{36}$/); expect(sessionStorage.getItem('sub2:video-studio:intents:11')).not.toContain('山谷')
    current!.unmount(); mountPage(); await flushPromises(); expect(current!.text()).toContain(old); expect(video.get).not.toHaveBeenCalled(); expect(video.create).toHaveBeenCalledTimes(1)
    video.get.mockRejectedValueOnce({ status: 404 }); await click('查询原任务', '.vs-receipt'); expect(video.get).toHaveBeenCalledWith(old, expect.any(AbortSignal)); expect(current!.text()).toContain('暂未找到原任务')
    expect(current!.find('.vs-receipt').text()).not.toContain('另起新草稿'); expect(video.create).toHaveBeenCalledTimes(1)
    video.get.mockResolvedValueOnce(job({ id: old, status: 'failed' })); await click('查询原任务', '.vs-receipt'); expect(current!.text()).toContain(old)
    await click('另起新草稿'); await describeShot(); await submit(); expect(video.create).toHaveBeenCalledTimes(2); expect(video.create.mock.calls[1][0].request_id).not.toBe(old)
    expect(get('.vs-card.is-failed').text()).not.toContain(old)
    await get('.vs-card.is-failed .vs-details-button').trigger('click'); expect(get('.vs-job-id').text()).toContain(old)
  })
  it('503 回执不能当作失败断言：按原 ID 查询到 failed 后展示后端结果，不重发', async () => {
    mountPage(); await flushPromises(); await describeShot()
    video.create.mockRejectedValueOnce({ status: 503, message: '暂时无法确认准入或报价' }); await submit()
    const original = video.create.mock.calls[0][0].request_id
    expect(current!.text()).toContain('未收到服务端回执'); video.get.mockResolvedValueOnce(job({ id: original, status: 'failed' }))
    await click('查询原任务', '.vs-receipt'); expect(video.get).toHaveBeenCalledWith(original, expect.any(AbortSignal))
    expect(get('.vs-status').text()).toBe('生成失败'); expect(get('.vs-receipt').text()).toContain('本次生成失败'); expect(video.create).toHaveBeenCalledTimes(1)
  })
  it('中途暂停准入后，原图不读取，新提交为零，已有视频仍显示', async () => {
    video.list.mockResolvedValue({ items: [job({ status: 'completed', billing_review: true })] }); sources.pendingImage.mockReturnValue(source)
    mountPage(); await flushPromises(); video.models.mockResolvedValue({ enabled: false, items: [] }); await submit()
    expect(current!.text()).toContain('新建视频已暂停'); expect(generate().disabled).toBe(true); expect(sources.readImage).not.toHaveBeenCalled(); expect(video.create).not.toHaveBeenCalled()
    expect(button('加载播放')).toBeDefined(); expect(current!.text()).toContain('账务待核对')
  })
})

describe('作品、跟踪、取片与移动端', () => {
  it('卡片不显示编号，详情保留完整原任务 ID，查询仍使用原 ID', async () => {
    const ids = ['10000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000002']
    video.list.mockResolvedValue({ items: ids.map(taskId => job({ id: taskId })) })
    mountPage(); await flushPromises()
    expect(current!.findAll('.vs-card-head code')).toHaveLength(0)
    expect(current!.findAll('.vs-card-stage')).toHaveLength(2)
    for (const [index, taskId] of ids.entries()) {
      expect(current!.findAll('.vs-card')[index].text()).not.toContain(taskId)
      await current!.findAll('.vs-card')[index].get('.vs-details-button').trigger('click')
      expect(get('.vs-job-id code').text()).toBe(taskId)
      await get('[aria-label="关闭详情"]').trigger('click'); await flushPromises()
    }
    await current!.findAll('.vs-card')[0].get('.vs-query-button').trigger('click')
    expect(video.get).toHaveBeenCalledWith(ids[0], expect.any(AbortSignal)); expect(video.create).not.toHaveBeenCalled()
  })
  it('完成、运行、排队分别提供对勾、动态菱形和空心圆的状态图形挂载点', async () => {
    video.list.mockResolvedValue({ items: [job({ status: 'completed' }), job({ id: `${id}-running`, status: 'running' }), job({ id: `${id}-queued`, status: 'queued' })] })
    mountPage(); await flushPromises()
    for (const state of ['completed', 'running', 'queued']) expect(get(`.vs-status[data-state=${state}] > i[aria-hidden=true]`).exists()).toBe(true)
  })
  it('VIDEO 焦点下的非冒泡 Escape 收起审片弹层，释放媒体并回实际加载按钮', async () => {
    video.list.mockResolvedValue({ items: [job({ status: 'completed', billing_review: true })] }); video.content.mockResolvedValue(new Blob(['\u0000\u0000\u0000\u0018ftypisom'], { type: 'video/mp4' }))
    mountPage(); await flushPromises(); const opener = button('加载播放'); opener.focus(); opener.click()
    await vi.waitFor(() => expect(current!.find('video').exists()).toBe(true)); const media = get('video').element as HTMLVideoElement
    media.tabIndex = 0; media.focus(); expect(document.activeElement).toBe(media)
    const escape = new KeyboardEvent('keydown', { key: 'Escape', bubbles: false, cancelable: true })
    media.dispatchEvent(escape); await nextTick()
    expect(escape.defaultPrevented).toBe(true); expect(current!.find('.vs-player[open]').exists()).toBe(false)
    expect(document.activeElement).toBe(opener); expect(video.content).toHaveBeenCalledTimes(1); expect(revokedUrl).toHaveBeenCalledWith('blob:studio-1')
  })
  it('原生全屏时 Escape 不由审片区处理，独立关闭按钮仍收起并回焦', async () => {
    video.list.mockResolvedValue({ items: [job({ status: 'completed' })] }); video.content.mockResolvedValue(new Blob(['\u0000\u0000\u0000\u0018ftypisom'], { type: 'video/mp4' }))
    mountPage(); await flushPromises(); const opener = button('加载播放'); opener.click()
    await vi.waitFor(() => expect(current!.find('video').exists()).toBe(true)); const media = get('video').element as HTMLVideoElement
    Object.defineProperty(document, 'fullscreenElement', { configurable: true, value: media }); media.tabIndex = 0; media.focus()
    const escape = new KeyboardEvent('keydown', { key: 'Escape', bubbles: false, cancelable: true })
    media.dispatchEvent(escape); await nextTick()
    expect(escape.defaultPrevented).toBe(false); expect(current!.find('.vs-player[open]').exists()).toBe(true); expect(document.activeElement).toBe(media)
    Object.defineProperty(document, 'fullscreenElement', { configurable: true, value: null })
    await get('[aria-label="收起审片"]').trigger('click'); expect(current!.find('.vs-player[open]').exists()).toBe(false); expect(document.activeElement).toBe(opener)
  })
  it('分页仅取元数据、去重；筛选如实限定已加载记录，失败与过期各自展示', async () => {
    video.list.mockResolvedValueOnce({ items: [job({ status: 'failed', detail: '内容检查未通过' })], next_cursor: id })
      .mockResolvedValueOnce({ items: [job({ status: 'failed' }), job({ id: 'older', status: 'expired' })] })
    mountPage(); await flushPromises(); await click('加载更多记录')
    expect(video.list.mock.calls.map(args => args[0])).toEqual([undefined, id]); expect(current!.findAll('.vs-card')).toHaveLength(2)
    expect(current!.text()).not.toContain('内容检查未通过'); await click('详情', '.vs-card.is-failed')
    expect(get('.vs-job-details').text()).toContain('内容检查未通过'); await get('[aria-label="关闭详情"]').trigger('click'); await flushPromises()
    expect(current!.text()).toContain('已过期'); expect(current!.text()).toContain('仅筛选已加载的 2 条记录')
    await click('异常与过期'); expect(current!.findAll('.vs-card')).toHaveLength(2); await click('已完成'); expect(current!.findAll('.vs-card')).toHaveLength(0)
    expect(video.get).not.toHaveBeenCalled(); expect(video.content).not.toHaveBeenCalled(); expect(video.list).toHaveBeenCalledTimes(2)
  })
  it('只读当前选中的任务；隐藏时中止在途查询，恢复只查原任务，登出后全部停止', async () => {
    vi.useFakeTimers(); video.list.mockResolvedValue({ items: [job(), job({ id: 'another' })] })
    mountPage(); await flushPromises(); await vi.advanceTimersByTimeAsync(15_000); expect(video.get).not.toHaveBeenCalled()
    await click('查询状态'); expect(video.get.mock.calls.map(c => c[0])).toEqual([id]); video.get.mockImplementationOnce(() => new Promise(() => {}))
    await vi.advanceTimersByTimeAsync(6000); const signal = video.get.mock.calls[1][1] as AbortSignal
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' }); document.dispatchEvent(new Event('visibilitychange')); expect(signal.aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(24_000); expect(video.get).toHaveBeenCalledTimes(2)
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' }); document.dispatchEvent(new Event('visibilitychange')); await flushPromises()
    expect(video.get.mock.calls.map(c => c[0])).toEqual([id, id, id]); auth.user = null; await nextTick(); await vi.advanceTimersByTimeAsync(24_000)
    expect(video.get).toHaveBeenCalledTimes(3); expect(video.list.mock.calls[0][1].aborted).toBe(true); expect(video.content).not.toHaveBeenCalled()
  })
  it('桌面只在明确打开后取一次 Blob；按需审片解码失败可下载，收起释放并还焦', async () => {
    video.list.mockResolvedValue({ items: [job({ status: 'completed', billing_review: true })] }); video.content.mockResolvedValue(new Blob([new Uint8Array([0x1a, 0x45, 0xdf, 0xa3, 0, 0, 0])], { type: 'video/mp4' }))
    mountPage(); await flushPromises(); expect(video.content).not.toHaveBeenCalled(); expect(current!.find('.vs-player[open]').exists()).toBe(false)
    const opener = button('加载播放'); opener.focus(); opener.click()
    await vi.waitFor(() => expect(current!.find('video').exists()).toBe(true)); expect(get('.vs-player').element.tagName).toBe('DIALOG'); expect(document.activeElement).toBe(get('[aria-label="收起审片"]').element)
    expect(get('video').attributes('autoplay')).toBeUndefined(); expect(get('video').attributes('src')).toMatch(/^blob:/); expect(current!.text()).toContain('不影响播放与下载')
    expect(get('a[download]').attributes('download')).toBe(`video-${id}.webm`); await get('video').trigger('error'); expect(current!.text()).toContain('浏览器无法解码'); expect(current!.find('a[download]').exists()).toBe(true)
    await get('[aria-label="收起审片"]').trigger('click'); expect(revokedUrl).toHaveBeenCalledWith('blob:studio-1'); expect(document.activeElement).toBe(opener); expect(video.content).toHaveBeenCalledTimes(1)
  })
  it('下载是独立授权动作，不实例化播放器；过期响应不是解码失败', async () => {
    video.list.mockResolvedValue({ items: [job({ status: 'completed' })] }); video.content.mockResolvedValueOnce(new Blob(['\u0000\u0000\u0000\u0018ftypqt  '], { type: 'video/quicktime' })).mockRejectedValueOnce({ status: 410 })
    const save = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    mountPage(); await flushPromises(); await click('下载', '.vs-card'); await vi.waitFor(() => expect(save).toHaveBeenCalledTimes(1))
    expect(current!.find('video').exists()).toBe(false); expect(get('a[download]').attributes('download')).toBe(`video-${id}.mov`)
    await get('[aria-label="收起审片"]').trigger('click'); const opener = button('加载播放'); opener.focus(); opener.click(); await flushPromises()
    expect(current!.text()).toContain('视频文件已过期'); expect(current!.text()).not.toContain('无法解码'); expect(button('详情', '.vs-card')).toBeDefined()
    expect(current!.find('.vs-card-play').exists()).toBe(false)
    await get('.vs-player').trigger('keydown', { key: 'Escape' }); expect(document.activeElement).toBe(opener)
  })
  it('若完成筛选在 410 后移除了整条记录，Esc 收起审片时焦点回到记录标题', async () => {
    video.list.mockResolvedValue({ items: [job({ status: 'completed' })] }); video.content.mockRejectedValue({ status: 410 })
    mountPage(); await flushPromises(); await click('已完成'); await click('加载播放')
    expect(current!.find('.vs-card').exists()).toBe(false); await get('.vs-player').trigger('keydown', { key: 'Escape' })
    expect(document.activeElement).toBe(get('#vs-gallery-title').element)
  })
  it('另一标签页退出会撤销当前打开的 Blob，不能再用旧会话提交', async () => {
    video.list.mockResolvedValue({ items: [job({ status: 'completed' })] }); video.content.mockResolvedValue(new Blob(['\u0000\u0000\u0000\u0018ftypisom'], { type: 'video/mp4' }))
    mountPage(); await flushPromises(); await click('加载播放'); await vi.waitFor(() => expect(current!.find('video').exists()).toBe(true))
    window.dispatchEvent(new StorageEvent('storage', { key: 'auth_token', newValue: null })); await nextTick()
    expect(revokedUrl).toHaveBeenCalledTimes(1); expect(current!.find('.vs-form').exists()).toBe(false); expect(video.create).not.toHaveBeenCalled()
  })
  it('超出预览预算只允许受控下载；超出读取预算不创建 Blob URL', async () => {
    video.list.mockResolvedValue({ items: [job({ status: 'completed' })] })
    const mockSize = (size: number) => { const blob = new Blob(['\u0000\u0000\u0000\u0018ftypisom'], { type: 'video/mp4' }); Object.defineProperty(blob, 'size', { value: size }); return blob }
    video.content.mockResolvedValueOnce(mockSize(51_000_000)).mockResolvedValueOnce(mockSize(100_000_001))
    mountPage(); await flushPromises(); await click('加载播放'); await vi.waitFor(() => expect(current!.text()).toContain('文件较大'))
    expect(current!.find('video').exists()).toBe(false); expect(current!.find('a[download]').exists()).toBe(true); await get('[aria-label="收起审片"]').trigger('click')
    await click('加载播放'); expect(current!.text()).toContain('100 MB 读取上限'); expect(createdUrl).toHaveBeenCalledTimes(1); expect(revokedUrl).toHaveBeenCalledTimes(1)
  })
  it('换号后中止旧媒体响应，不显示或留存前一用户的成片', async () => {
    video.list.mockResolvedValueOnce({ items: [job({ status: 'completed' })] }); let finish!: (b: Blob) => void
    video.content.mockImplementation(() => new Promise<Blob>(r => { finish = r })); mountPage(); await flushPromises(); await click('加载播放')
    const signal: AbortSignal = video.content.mock.calls[0][1]; auth.user = { id: 12 }; await flushPromises(); expect(signal.aborted).toBe(true)
    finish(new Blob(['data'], { type: 'video/mp4' })); await flushPromises(); expect(createdUrl).not.toHaveBeenCalled(); expect(current!.text()).not.toContain(id)
  })
  it('真实封面只在可见时读取；详情共享原 URL，参数 0 保留且关闭详情不提前回收', async () => {
    let intersect!: IntersectionObserverCallback
    const disconnect = vi.fn()
    vi.stubGlobal('IntersectionObserver', class {
      constructor(callback: IntersectionObserverCallback) { intersect = callback }
      observe() {}
      unobserve() {}
      disconnect() { disconnect() }
    })
    const completed = job({ status: 'completed', has_poster: true, parameters: { material_mode: 'references', reference_count: 0, seed: 0 } })
    video.list.mockResolvedValue({ items: [completed] })
    video.poster.mockResolvedValue(new Blob(['real-private-frame'], { type: 'image/jpeg' }))
    mountPage(); await flushPromises()
    expect(video.poster).not.toHaveBeenCalled(); expect(video.content).not.toHaveBeenCalled(); expect(get('.vs-card-stage').text()).toContain('7:4')
    intersect([{ isIntersecting: false } as IntersectionObserverEntry], null as unknown as IntersectionObserver)
    expect(video.poster).not.toHaveBeenCalled()
    intersect([{ isIntersecting: true } as IntersectionObserverEntry], null as unknown as IntersectionObserver)
    await vi.waitFor(() => expect(get('.vs-poster img').attributes('src')).toBe('blob:studio-1'))
    expect(disconnect).toHaveBeenCalled(); expect(video.poster).toHaveBeenCalledWith(id, expect.any(AbortSignal))
    await click('详情', '.vs-card')
    expect(get('.vs-player').attributes('open')).toBeDefined(); expect(get('.vs-job-details').text()).toContain('提交尺寸1344 × 768')
    expect(get('.vs-job-details').text()).not.toContain('请求时长'); expect(get('.vs-job-details').text()).toContain('0 个参考素材')
    expect(get('.vs-job-details').text()).toContain('Seed0'); expect(get('.vs-job-details').text()).not.toContain('成片尺寸')
    expect(get('.vs-details-poster').attributes('src')).toBe('blob:studio-1'); expect(current!.find('.vs-player-foot').exists()).toBe(false)
    expect(video.poster).toHaveBeenCalledTimes(1); expect(video.get).not.toHaveBeenCalled(); expect(video.content).not.toHaveBeenCalled()
    await get('[aria-label="关闭详情"]').trigger('click'); await flushPromises(); expect(revokedUrl).not.toHaveBeenCalled()
    expect(get('.vs-poster img').attributes('src')).toBe('blob:studio-1')
    current!.unmount(); current = undefined
    expect(revokedUrl).toHaveBeenCalledWith('blob:studio-1')
  })
  it('无封面老记录使用诚实占位，仍可手动播放；老参数不倒推', async () => {
    video.list.mockResolvedValue({ items: [job({ status: 'completed', has_poster: false, size: '' })] })
    video.content.mockResolvedValue(new Blob(['\u0000\u0000\u0000\u0018ftypisom'], { type: 'video/mp4' }))
    mountPage(); await flushPromises()
    expect(current!.text()).toContain('历史成片暂无封面'); expect(current!.text()).toContain('仍可手动加载播放'); expect(video.poster).not.toHaveBeenCalled()
    await click('详情', '.vs-card'); expect(get('.vs-job-details').text()).toContain('未指定'); expect(get('.vs-job-details').text()).toContain('未记录')
    await get('[aria-label="关闭详情"]').trigger('click'); await click('加载播放')
    await vi.waitFor(() => expect(current!.find('video').exists()).toBe(true)); expect(video.content).toHaveBeenCalledTimes(1)
  })
  it('排队、生成、失败、过期与待核实都不伪造封面或进度', async () => {
    const statuses = ['submitting', 'queued', 'running', 'failed', 'uncertain', 'expired'] as const
    video.list.mockResolvedValue({ items: statuses.map((status, index) => job({ id: `${id}-${index}`, status, has_poster: true })) })
    mountPage(); await flushPromises()
    expect(current!.findAll('.vs-poster-placeholder')).toHaveLength(statuses.length); expect(current!.find('.vs-poster img').exists()).toBe(false)
    expect(video.poster).not.toHaveBeenCalled(); expect(video.content).not.toHaveBeenCalled(); expect(current!.text()).not.toContain('%')
  })
  it('单张封面过期不读取整片，也不影响其他真实帧', async () => {
    video.list.mockResolvedValue({ items: [job({ status: 'completed', has_poster: true }), job({ id: `${id}-next`, status: 'completed', has_poster: true })] })
    video.poster.mockRejectedValueOnce({ status: 410 }).mockResolvedValueOnce(new Blob(['frame'], { type: 'image/jpeg' }))
    mountPage(); await vi.waitFor(() => expect(current!.findAll('.vs-poster img')).toHaveLength(1))
    expect(current!.text()).toContain('封面已过期'); expect(video.poster).toHaveBeenCalledTimes(2); expect(video.content).not.toHaveBeenCalled()
    expect(current!.findAll('.vs-stage-play')).toHaveLength(2); expect(current!.find('.vs-card-play').exists()).toBe(false)
  })
  it('卡片仅保留下载和详情图标，已完成封面直接按需播放', async () => {
    video.list.mockResolvedValue({ items: [job({ status: 'completed' })] })
    video.content.mockResolvedValue(new Blob(['\u0000\u0000\u0000\u0018ftypisom'], { type: 'video/mp4' }))
    mountPage(); await flushPromises()
    expect(current!.findAll('.vs-card-actions button').map(node => node.attributes('aria-label'))).toEqual(['下载', '详情'])
    expect(current!.findAll('.vs-card button').filter(node => node.text().trim() === '加载播放')).toHaveLength(0)
    expect(get('.vs-card-stage').attributes('aria-label')).toBe('加载播放')
    await get('.vs-card-stage').trigger('click'); await vi.waitFor(() => expect(current!.find('video').exists()).toBe(true))
    expect(video.content).toHaveBeenCalledTimes(1); expect(video.get).not.toHaveBeenCalled()
  })
  it('JPEG 解码失败立即撤掉卡片及详情的裂图，回收 URL 且不自动重试', async () => {
    video.list.mockResolvedValue({ items: [job({ status: 'completed', has_poster: true })] })
    video.poster.mockResolvedValue(new Blob(['broken-jpeg'], { type: 'image/jpeg' }))
    mountPage(); await vi.waitFor(() => expect(current!.find('.vs-poster img').exists()).toBe(true))
    await click('详情', '.vs-card'); expect(get('.vs-details-poster').attributes('src')).toBe('blob:studio-1')
    await get('.vs-poster img').trigger('error'); await flushPromises()
    expect(current!.find('.vs-poster img').exists()).toBe(false); expect(current!.find('.vs-details-poster').exists()).toBe(false)
    expect(current!.text()).toContain('封面图片无法解码'); expect(revokedUrl).toHaveBeenCalledWith('blob:studio-1')
    expect(video.poster).toHaveBeenCalledTimes(1); expect(video.get).not.toHaveBeenCalled(); expect(video.content).not.toHaveBeenCalled()
  })
  it('同值新对象与异步观察器下，手动刷新仍重验封面；403 同步撤销详情共享帧', async () => {
    let intersect!: IntersectionObserverCallback
    vi.stubGlobal('IntersectionObserver', class {
      constructor(callback: IntersectionObserverCallback) { intersect = callback }
      observe() {}
      unobserve() {}
      disconnect() {}
    })
    const completed = job({ status: 'completed', has_poster: true })
    video.list.mockImplementation(async () => ({ items: [{ ...completed }] }))
    video.poster.mockResolvedValueOnce(new Blob(['frame'], { type: 'image/jpeg' })).mockRejectedValueOnce({ status: 403 })
    mountPage(); await flushPromises(); expect(video.poster).not.toHaveBeenCalled()
    await new Promise(resolve => setTimeout(resolve, 0))
    intersect([{ isIntersecting: true } as IntersectionObserverEntry], null as unknown as IntersectionObserver)
    await vi.waitFor(() => expect(current!.find('.vs-poster img').exists()).toBe(true))
    await click('详情', '.vs-card'); expect(current!.find('.vs-details-poster').exists()).toBe(true)
    await click('刷新索引'); await vi.waitFor(() => expect(video.poster).toHaveBeenCalledTimes(2)); await flushPromises()
    expect(current!.find('.vs-poster img').exists()).toBe(false); expect(current!.find('.vs-details-poster').exists()).toBe(false)
    expect(current!.text()).toContain('封面授权不可用'); expect(current!.text()).toContain('当前登录或原任务授权已失效')
    expect(get('.vs-card-stage').text()).not.toContain('不影响播放'); expect(revokedUrl).toHaveBeenCalledWith('blob:studio-1')
    expect(video.get).not.toHaveBeenCalled(); expect(video.content).not.toHaveBeenCalled()
  })
  it.each([{ status: 410, message: '封面已过期' }, { status: 404, message: '暂无可用封面帧' }])('刷新收到 $status 时撤图并准确区分 $message', async ({ status, message }) => {
    video.list.mockResolvedValue({ items: [job({ status: 'completed', has_poster: true })] })
    video.poster.mockResolvedValueOnce(new Blob(['frame'], { type: 'image/jpeg' })).mockRejectedValueOnce({ status })
    mountPage(); await vi.waitFor(() => expect(current!.find('.vs-poster img').exists()).toBe(true))
    await click('刷新索引'); await vi.waitFor(() => expect(video.poster).toHaveBeenCalledTimes(2)); await flushPromises()
    expect(current!.find('.vs-poster img').exists()).toBe(false); expect(current!.text()).toContain(message)
    expect(current!.text()).not.toContain('封面授权不可用'); expect(revokedUrl).toHaveBeenCalledWith('blob:studio-1')
    expect(video.get).not.toHaveBeenCalled(); expect(video.content).not.toHaveBeenCalled()
  })
  it.each(['1970-01-01T00:00:00.000Z', '2020-01-01T00:00:00.000Z'])('已过去的封面有效期 %s 不请求也不展示', async expires => {
    video.list.mockResolvedValue({ items: [job({ status: 'completed', has_poster: true, poster_expires_at: expires })] })
    mountPage(); await flushPromises()
    expect(current!.text()).toContain('封面已过期'); expect(current!.find('.vs-poster img').exists()).toBe(false)
    expect(video.poster).not.toHaveBeenCalled(); expect(createdUrl).not.toHaveBeenCalled(); expect(video.content).not.toHaveBeenCalled()
  })
  it('封面到期即时撤销卡片与详情，并在引用释放后回收对象 URL', async () => {
    vi.useFakeTimers(); vi.setSystemTime(new Date('2026-11-21T00:00:00.000Z'))
    video.list.mockResolvedValue({ items: [job({ status: 'completed', has_poster: true, poster_expires_at: '2026-11-21T00:00:05.000Z' })] })
    video.poster.mockResolvedValue(new Blob(['frame'], { type: 'image/jpeg' }))
    mountPage(); await vi.waitFor(() => expect(current!.find('.vs-poster img').exists()).toBe(true))
    await click('详情', '.vs-card'); expect(current!.find('.vs-details-poster').exists()).toBe(true)
    await vi.advanceTimersByTimeAsync(5001); await flushPromises()
    expect(current!.find('.vs-poster img').exists()).toBe(false); expect(current!.find('.vs-details-poster').exists()).toBe(false)
    expect(current!.text()).toContain('封面已过期'); expect(revokedUrl).toHaveBeenCalledWith('blob:studio-1')
    expect(video.poster).toHaveBeenCalledTimes(1); expect(video.get).not.toHaveBeenCalled(); expect(video.content).not.toHaveBeenCalled()
  })
  it('记录切换为失效状态时回收真实帧 URL，不重复读取或额外取片', async () => {
    video.list.mockResolvedValueOnce({ items: [job({ status: 'completed', has_poster: true })] })
      .mockResolvedValueOnce({ items: [job({ status: 'expired', has_poster: false })] })
    video.poster.mockResolvedValue(new Blob(['frame'], { type: 'image/jpeg' }))
    mountPage(); await vi.waitFor(() => expect(current!.find('.vs-poster img').exists()).toBe(true))
    await click('刷新索引')
    expect(revokedUrl).toHaveBeenCalledWith('blob:studio-1'); expect(current!.find('.vs-poster img').exists()).toBe(false)
    expect(current!.text()).toContain('成片已经过期'); expect(video.poster).toHaveBeenCalledTimes(1); expect(video.content).not.toHaveBeenCalled(); expect(video.get).not.toHaveBeenCalled()
  })
  it('卸载中止在途封面请求，迟到字节不会创建或保留跨会话 URL', async () => {
    let finish!: (blob: Blob) => void
    video.list.mockResolvedValue({ items: [job({ status: 'completed', has_poster: true })] })
    video.poster.mockImplementation(() => new Promise<Blob>(resolve => { finish = resolve }))
    mountPage(); await vi.waitFor(() => expect(video.poster).toHaveBeenCalledTimes(1))
    const signal = video.poster.mock.calls[0][1] as AbortSignal
    current!.unmount(); current = undefined
    expect(signal.aborted).toBe(true); finish(new Blob(['late-frame'], { type: 'image/jpeg' })); await flushPromises()
    expect(createdUrl).not.toHaveBeenCalled(); expect(revokedUrl).not.toHaveBeenCalled(); expect(video.content).not.toHaveBeenCalled()
  })
  it('切换用户后回收旧封面并重新授权读取，不跨用户复用对象 URL', async () => {
    video.list.mockResolvedValue({ items: [job({ status: 'completed', has_poster: true })] })
    video.poster.mockResolvedValue(new Blob(['frame'], { type: 'image/jpeg' }))
    mountPage(); await vi.waitFor(() => expect(get('.vs-poster img').attributes('src')).toBe('blob:studio-1'))
    auth.user = { id: 12 }; await vi.waitFor(() => expect(get('.vs-poster img').attributes('src')).toBe('blob:studio-2'))
    expect(revokedUrl).toHaveBeenCalledWith('blob:studio-1'); expect(video.poster).toHaveBeenCalledTimes(2)
    expect(video.get).not.toHaveBeenCalled(); expect(video.content).not.toHaveBeenCalled()
  })
  it('手机先看作品；创作抽屉支持 Esc 回焦且不丢失未提交描述', async () => {
    desktop = false; mountPage(); await flushPromises(); const launcher = button('展开 ↑'); expect(get('#vs-composer').attributes('open')).toBeUndefined()
    launcher.focus(); launcher.click(); await flushPromises(); expect(modalShow).toHaveBeenCalledTimes(1); expect(document.activeElement).toBe(get('textarea').element)
    await describeShot(); await get('#vs-composer').trigger('keydown', { key: 'Escape' }); expect(document.activeElement).toBe(launcher)
    launcher.click(); await flushPromises(); expect((get('textarea').element as HTMLTextAreaElement).value).toContain('山谷'); expect(video.create).not.toHaveBeenCalled()
    await get('[aria-label="关闭创作栏"]').trigger('click'); expect(document.activeElement).toBe(launcher)
  })
  it('手机明确点击才用模态框取片，Esc 退出后还焦且释放媒体', async () => {
    desktop = false; video.list.mockResolvedValue({ items: [job({ status: 'completed' })] }); video.content.mockResolvedValue(new Blob(['\u0000\u0000\u0000\u0018ftypisom'], { type: 'video/mp4' }))
    mountPage(); await flushPromises(); expect(video.content).not.toHaveBeenCalled(); const opener = button('加载播放'); opener.focus(); opener.click()
    await vi.waitFor(() => expect(current!.find('video').exists()).toBe(true)); expect(get('.vs-player').element.tagName).toBe('DIALOG'); expect(get('.vs-player').attributes('open')).toBeDefined()
    await get('.vs-player').trigger('keydown', { key: 'Escape' }); expect(document.activeElement).toBe(opener); expect(revokedUrl).toHaveBeenCalledTimes(1); expect(video.content).toHaveBeenCalledTimes(1)
  })
})
