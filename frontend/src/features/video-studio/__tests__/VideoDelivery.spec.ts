import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import VideoDelivery from '@/features/video-studio/VideoDelivery.vue'
import VideoGallery from '@/features/video-studio/VideoGallery.vue'
import type { VideoJob } from '@/features/video-studio/api'
import { createWorkspaceDurationStore, formatVideoDuration, measuredDurationMs } from '@/features/video-studio/mediaDuration'
import { createVideoPosterCache } from '@/features/video-studio/posterCache'

const { video } = vi.hoisted(() => ({
  video: { models: vi.fn(), list: vi.fn(), get: vi.fn(), content: vi.fn(), poster: vi.fn(), create: vi.fn() },
}))
vi.mock('@/features/video-studio/api', async importOriginal => ({
  ...await importOriginal<typeof import('@/features/video-studio/api')>(),
  videoStudio: video,
}))

const mp4Blob = () => new Blob(['\u0000\u0000\u0000\u0018ftypisom'], { type: 'video/mp4' })
const makeJob = (overrides: Partial<VideoJob> = {}): VideoJob => ({
  id: '10000000-0000-4000-8000-000000000001',
  model: 'muse-video',
  status: 'completed',
  seconds: 6,
  size: '16:9',
  created_at: '2026-11-21T00:05:00Z',
  expires_at: '2026-11-21T21:05:00Z',
  has_poster: true,
  ...overrides,
})

const modalShow = vi.fn(function (this: HTMLDialogElement) { this.setAttribute('open', '') })
const modalClose = vi.fn(function (this: HTMLDialogElement) { this.removeAttribute('open'); this.dispatchEvent(new Event('close')) })
let createdUrl: ReturnType<typeof vi.fn>
let revokedUrl: ReturnType<typeof vi.fn>

beforeEach(() => {
  vi.resetAllMocks()
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  vi.stubGlobal('IntersectionObserver', class {
    constructor(private readonly cb: IntersectionObserverCallback) {}
    observe(target: Element) { this.cb([{ target, isIntersecting: true } as IntersectionObserverEntry], this as unknown as IntersectionObserver) }
    unobserve() {}
    disconnect() {}
  })
  Object.defineProperties(HTMLDialogElement.prototype, {
    showModal: { configurable: true, value: modalShow },
    close: { configurable: true, value: modalClose },
  })
  modalShow.mockImplementation(function () { this.setAttribute('open', '') })
  modalClose.mockImplementation(function () { this.removeAttribute('open'); this.dispatchEvent(new Event('close')) })
  createdUrl = vi.fn((_blob: Blob) => `blob:delivery-${createdUrl.mock.calls.length}`)
  revokedUrl = vi.fn()
  Object.defineProperties(URL, {
    createObjectURL: { configurable: true, value: createdUrl },
    revokeObjectURL: { configurable: true, value: revokedUrl },
  })
})

afterEach(() => {
  document.body.replaceChildren()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('VideoDelivery 与画廊真实视频时长', () => {
  it('只显示真实视频时长：旧请求5成片10只显10，新 Muse 6 和 H3 无测量均不显示秒数与对照话术', async () => {
    const legacyMuseWithActual = makeJob({
      id: '10000000-0000-4000-8000-000000000001',
      model: 'muse-video',
      seconds: 5,
      actual_duration_ms: 10_000,
      parameters: { material_mode: 'references', reference_count: 2, reference_kinds: ['image', 'video'] },
    })
    const newMuseUnmeasured = makeJob({
      id: '10000000-0000-4000-8000-000000000002',
      model: 'muse-video',
      seconds: 6,
      parameters: { duration_mode: 'automatic', material_mode: 'text', reference_count: 0 },
    })
    const h3Unmeasured = makeJob({
      id: '10000000-0000-4000-8000-000000000003',
      model: 'minimax/h3',
      seconds: 8,
      size: '1344x768',
      parameters: { material_mode: 'first_last_frame', reference_count: 0 },
    })
    const h3FastMeasured = makeJob({
      id: '10000000-0000-4000-8000-000000000004',
      model: 'minimax/h3-fast',
      seconds: 8,
      actual_duration_ms: 7_500,
      size: '768x1344',
      parameters: { material_mode: 'first_last_frame', reference_count: 0, seed: 0 },
    })
    video.list.mockResolvedValue({ items: [legacyMuseWithActual, newMuseUnmeasured, h3Unmeasured, h3FastMeasured] })
    video.poster.mockRejectedValue({ status: 404 })

    const wrapper = mount(VideoGallery, {
      props: { outstanding: [], example: '示例', mobile: false },
      attachTo: document.body,
    })
    await flushPromises()

    const cards = wrapper.findAll('.vs-card')
    expect(cards).toHaveLength(4)

    // 1. Legacy Muse: requested 5s, actual 10s -> only 10s
    expect(cards[0].get('.vs-stage-bottom').text()).toContain('10 秒')
    expect(cards[0].get('.vs-stage-bottom').text()).not.toContain('5 秒')
    expect(cards[0].get('.vs-card-flags').text()).toContain('2 个参考素材')
    expect(cards[0].get('.vs-card-flags').text()).not.toContain('张参考图')

    // 2. New Muse (seconds: 6, automatic, no actual_duration_ms) -> no seconds or 自动时长
    expect(cards[1].get('.vs-stage-bottom').text()).not.toContain('秒')
    expect(cards[1].get('.vs-stage-bottom').text()).not.toContain('自动时长')

    // 3. H3 unmeasured (seconds: 8, no actual_duration_ms) -> no seconds
    expect(cards[2].get('.vs-stage-bottom').text()).not.toContain('秒')

    // 4. H3 Fast with actual_duration_ms: 7500 -> 7.5 秒
    expect(cards[3].get('.vs-stage-bottom').text()).toContain('7.5 秒')
    expect(cards[3].get('.vs-stage-bottom').text()).not.toContain('8 秒')

    // Details for legacy Muse (actual 10s, requested 5s): only 视频时长10 秒, no contrast phrasing
    await cards[0].get('.vs-details-button').trigger('click')
    await flushPromises()
    const detail1 = wrapper.get('.vs-delivery-dialog').text()
    expect(detail1).toContain('视频时长10 秒')
    expect(detail1).toContain('提交画幅16:9')
    expect(detail1).toContain('2 个参考素材（图片、视频）')
    expect(detail1).not.toContain('5 秒')
    expect(detail1).not.toContain('曾请求')
    expect(detail1).not.toContain('非实长')
    expect(detail1).not.toContain('实测')
    expect(detail1).not.toContain('Seed')
    expect(video.content).not.toHaveBeenCalled()
    expect(video.get).not.toHaveBeenCalled()

    // Details for new Muse unmeasured: no 视频时长, no 6 秒, no 自动时长
    await wrapper.get('[aria-label="下一条作品"]').trigger('click')
    await flushPromises()
    const detail2 = wrapper.get('.vs-delivery-dialog').text()
    expect(detail2).toContain('提交画幅16:9')
    expect(detail2).not.toContain('视频时长')
    expect(detail2).not.toContain('6 秒')
    expect(detail2).not.toContain('自动时长')
    expect(detail2).not.toContain('请求时长')

    // Details for H3 unmeasured: pixel size keeps 提交尺寸, no duration shown
    await wrapper.get('[aria-label="下一条作品"]').trigger('click')
    await flushPromises()
    const detail3 = wrapper.get('.vs-delivery-dialog').text()
    expect(detail3).toContain('提交尺寸1344 × 768')
    expect(detail3).not.toContain('视频时长')
    expect(detail3).not.toContain('8 秒')
    expect(detail3).not.toContain('请求时长')
    expect(detail3).not.toContain('Seed')

    // Details for H3 Fast measured: 7.5 秒 + Seed0
    await wrapper.get('[aria-label="下一条作品"]').trigger('click')
    await flushPromises()
    const detail4 = wrapper.get('.vs-delivery-dialog').text()
    expect(wrapper.get('#vs-player-title').text()).toBe('H3 Fast')
    expect(detail4).toContain('视频时长7.5 秒')
    expect(detail4).toContain('提交尺寸768 × 1344')
    expect(detail4).toContain('Seed0')
    expect(video.content).not.toHaveBeenCalled()

    wrapper.unmount()
  })

  it('本地播放 loadedmetadata 测量立即回写画廊与详情且无额外 content GET；不串其他作品，切用户重挂后清零', async () => {
    const jobA = makeJob({
      id: 'job-a',
      model: 'muse-video',
      seconds: 6,
      parameters: { duration_mode: 'automatic', material_mode: 'text', reference_count: 0 },
    })
    const jobB = makeJob({
      id: 'job-b',
      model: 'minimax/h3',
      seconds: 5,
      size: '1344x768',
      has_poster: false,
    })

    video.list.mockResolvedValue({ items: [jobA, jobB] })
    video.poster.mockRejectedValue({ status: 404 })
    video.content.mockResolvedValue(mp4Blob())

    const galleryA = mount(VideoGallery, {
      props: { outstanding: [], example: '示例', mobile: false },
      attachTo: document.body,
    })
    await flushPromises()

    // Initially neither card displays duration
    expect(galleryA.findAll('.vs-card')[0].get('.vs-stage-bottom').text()).not.toContain('秒')
    expect(galleryA.findAll('.vs-card')[1].get('.vs-stage-bottom').text()).not.toContain('秒')
    expect(video.content).not.toHaveBeenCalled()

    // Open jobA details -> play -> loadedmetadata reports 10s, 1280x720
    await galleryA.findAll('.vs-card')[0].get('.vs-details-button').trigger('click')
    await flushPromises()
    expect(video.content).not.toHaveBeenCalled()

    await galleryA.get('.vs-stage-play-btn').trigger('click')
    await vi.waitFor(() => expect(galleryA.find('video').exists()).toBe(true))
    const videoEl = galleryA.get('video').element as HTMLVideoElement
    Object.defineProperty(videoEl, 'duration', { configurable: true, value: 10 })
    Object.defineProperty(videoEl, 'videoWidth', { configurable: true, value: 1280 })
    Object.defineProperty(videoEl, 'videoHeight', { configurable: true, value: 720 })
    await galleryA.get('video').trigger('loadedmetadata')

    expect(galleryA.get('.vs-stage-meta-pill').text()).toBe('10 秒 · 1280 × 720')
    expect(galleryA.get('.vs-job-details').text()).toContain('视频时长10 秒')
    expect(galleryA.get('.vs-job-details').text()).toContain('视频尺寸1280 × 720')
    expect(video.content).toHaveBeenCalledTimes(1)

    // Switch to jobB: revokes blob, does NOT leak jobA's duration or dimensions, no extra content fetch
    await galleryA.get('[aria-label="下一条作品"]').trigger('click')
    await flushPromises()
    expect(revokedUrl).toHaveBeenCalledWith('blob:delivery-1')
    expect(galleryA.get('.vs-job-details').text()).not.toContain('视频时长')
    expect(galleryA.get('.vs-job-details').text()).not.toContain('10 秒')
    expect(galleryA.get('.vs-job-details').text()).not.toContain('1280 × 720')
    expect(video.content).toHaveBeenCalledTimes(1)

    // Close dialog: jobA card now shows 10 秒 immediately; jobB still has no duration
    await galleryA.get('[aria-label="关闭详情"]').trigger('click')
    await flushPromises()
    expect(galleryA.findAll('.vs-card')[0].get('.vs-stage-bottom').text()).toContain('10 秒')
    expect(galleryA.findAll('.vs-card')[1].get('.vs-stage-bottom').text()).not.toContain('秒')

    // Reopen jobA details: reuses workspace measurement without calling videoStudio.content again
    await galleryA.findAll('.vs-card')[0].get('.vs-details-button').trigger('click')
    await flushPromises()
    expect(galleryA.get('.vs-job-details').text()).toContain('视频时长10 秒')
    expect(video.content).toHaveBeenCalledTimes(1)
    expect(video.get).not.toHaveBeenCalled()

    // Unmount (e.g. user switch remounts workspace) -> fresh gallery does NOT inherit old user's local measurement
    galleryA.unmount()
    const galleryNextUser = mount(VideoGallery, {
      props: { outstanding: [], example: '示例', mobile: false },
      attachTo: document.body,
    })
    await flushPromises()
    expect(galleryNextUser.findAll('.vs-card')[0].get('.vs-stage-bottom').text()).not.toContain('秒')
    expect(video.content).toHaveBeenCalledTimes(1)
    galleryNextUser.unmount()
  })

  it('loadedmetadata 未知 duration 不显示假 0 秒，且可与有效像素尺寸独立展示', async () => {
    const cache = createVideoPosterCache()
    const durations = createWorkspaceDurationStore()
    const unmeasured = makeJob({ id: 'job-nan', model: 'muse-video', seconds: 6 })
    video.content.mockResolvedValue(mp4Blob())

    const wrapper = mount(VideoDelivery, {
      props: { mobile: false, cache, durations, jobs: [unmeasured] },
      attachTo: document.body,
    })
    const opener = document.createElement('button')
    document.body.appendChild(opener)

    await (wrapper.vm as InstanceType<typeof VideoDelivery>).open(unmeasured, 'play', opener)
    await vi.waitFor(() => expect(wrapper.find('video').exists()).toBe(true))
    const videoEl = wrapper.get('video').element as HTMLVideoElement
    Object.defineProperty(videoEl, 'duration', { configurable: true, value: Number.NaN })
    Object.defineProperty(videoEl, 'videoWidth', { configurable: true, value: 1280 })
    Object.defineProperty(videoEl, 'videoHeight', { configurable: true, value: 720 })
    await wrapper.get('video').trigger('loadedmetadata')

    expect(wrapper.get('.vs-job-details').text()).toContain('视频尺寸1280 × 720')
    expect(wrapper.get('.vs-job-details').text()).not.toContain('视频时长')
    expect(wrapper.get('.vs-job-details').text()).not.toContain('0 秒')
    expect(durations.get('job-nan')).toBeNull()
    expect(formatVideoDuration(0)).toBe('')
    expect(measuredDurationMs({ id: 'x', actual_duration_ms: 0 }, null)).toBeNull()

    wrapper.unmount()
    cache.dispose()
  })

  it('区分纯图片参考、非图片/缺类型参考素材，以及各模型 Seed 与真实扣费规则', async () => {
    const cache = createVideoPosterCache()
    const pureImages = makeJob({
      id: 'ref-pure-img',
      model: 'muse-video',
      size: '16:9',
      charged_amount: 0.75,
      charged_currency: 'USD',
      parameters: { duration_mode: 'automatic', material_mode: 'references', reference_count: 2, reference_kinds: ['image', 'image'], native_size: '1280x720' },
    })
    const pdfOnly = makeJob({
      id: 'ref-pdf',
      model: 'muse-video',
      size: '9:16',
      charged_amount: 0.75,
      parameters: { duration_mode: 'automatic', material_mode: 'references', reference_count: 1, reference_kinds: ['file'] },
    })
    const missingKindsH3 = makeJob({
      id: 'ref-missing-kinds',
      model: 'minimax/h3',
      seconds: 5,
      size: '1344x768',
      parameters: { material_mode: 'references', reference_count: 2 },
    })
    const historicalSeedH3 = makeJob({
      id: 'h3-legacy-seed',
      model: 'minimax/h3',
      seconds: 5,
      size: '1344x768',
      parameters: { material_mode: 'text', reference_count: 0, seed: 0 },
    })
    const fastMissingSeed = makeJob({
      id: 'fast-no-seed',
      model: 'minimax/h3-fast',
      seconds: 6,
      size: '1344x768',
      parameters: { material_mode: 'text', reference_count: 0 },
    })
    const wrapper = mount(VideoDelivery, {
      props: { mobile: false, cache, jobs: [pureImages, pdfOnly, missingKindsH3, historicalSeedH3, fastMissingSeed] },
      attachTo: document.body,
    })
    const opener = document.createElement('button')
    document.body.appendChild(opener)

    await (wrapper.vm as InstanceType<typeof VideoDelivery>).open(pureImages, 'details', opener)
    await flushPromises()
    expect(wrapper.get('.vs-job-details').text()).toContain('2 张参考图')
    expect(wrapper.get('.vs-job-details').text()).toContain('提交画幅16:9')
    expect(wrapper.get('.vs-job-details').text()).toContain('原生画布意图1280x720')
    expect(wrapper.get('.vs-job-details').text()).toContain('0.75 USD · 已结算')
    expect(wrapper.get('.vs-job-details').text()).not.toContain('Seed')

    await wrapper.get('[aria-label="下一条作品"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('.vs-job-details').text()).toContain('1 个参考素材（文件）')
    expect(wrapper.get('.vs-job-details').text()).toContain('未知（未返回真实结算）')
    expect(wrapper.get('.vs-job-details').text()).not.toContain('0.75')

    await wrapper.get('[aria-label="下一条作品"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('.vs-job-details').text()).toContain('提交尺寸1344 × 768')
    expect(wrapper.get('.vs-job-details').text()).toContain('2 个参考素材')
    expect(wrapper.get('.vs-job-details').text()).not.toContain('Seed')

    await wrapper.get('[aria-label="下一条作品"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('.vs-job-details').text()).toContain('Seed0')

    await wrapper.get('[aria-label="下一条作品"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('.vs-job-details').text()).toContain('Seed未返回')

    wrapper.unmount()
    cache.dispose()
  })

  it('私有封面共享、支持取消在途加载；过期/待核实禁用下载且不查上游；快捷键避开输入控件并支持复用', async () => {
    const cache = createVideoPosterCache()
    cache.put('job-ready', 'blob:private-poster')
    const ready = makeJob({ id: 'job-ready' })
    const expired = makeJob({ id: 'job-expired', status: 'expired', has_poster: false })
    const uncertain = makeJob({ id: 'job-uncertain', status: 'uncertain', detail: '上游提交回执未明', has_poster: false })

    let resolvePending!: (blob: Blob) => void
    video.content.mockImplementationOnce(() => new Promise<Blob>(resolve => { resolvePending = resolve }))

    const wrapper = mount(VideoDelivery, {
      props: { mobile: false, cache, jobs: [ready, expired, uncertain] },
      attachTo: document.body,
    })
    const opener = document.createElement('button')
    document.body.appendChild(opener)

    await (wrapper.vm as InstanceType<typeof VideoDelivery>).open(ready, 'details', opener)
    await flushPromises()
    expect(wrapper.get('.vs-details-poster').attributes('src')).toBe('blob:private-poster')
    expect(video.content).not.toHaveBeenCalled()

    await wrapper.get('.vs-stage-play-btn').trigger('click')
    await flushPromises()
    const signal = video.content.mock.calls[0][1] as AbortSignal
    expect(signal.aborted).toBe(false)
    await wrapper.get('.vs-cinema-ghost-btn').trigger('click')
    await flushPromises()
    expect(signal.aborted).toBe(true)
    resolvePending(mp4Blob())
    await flushPromises()
    expect(createdUrl).not.toHaveBeenCalled()

    await wrapper.get('[aria-label="下一条作品"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.vs-delivery-actions button')[0].attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('成片已过期')

    const input = document.createElement('input')
    wrapper.get('dialog').element.appendChild(input)
    input.focus()
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }))
    await nextTick()
    expect(wrapper.get('.vs-job-id code').text()).toBe('job-expired')
    input.remove()

    await wrapper.get('dialog').trigger('keydown', { key: 'ArrowRight' })
    await flushPromises()
    expect(wrapper.get('.vs-job-id code').text()).toBe('job-uncertain')
    expect(wrapper.text()).toContain('上游提交回执未明')
    expect(video.get).not.toHaveBeenCalled()

    const reuseBtn = wrapper.findAll('.vs-delivery-actions button').find(b => b.text().trim() === '复用参数')!
    await reuseBtn.trigger('click')
    await flushPromises()
    expect(wrapper.emitted('reuse')?.[0]?.[0]).toEqual(uncertain)

    wrapper.unmount()
    cache.dispose()
  })
})
