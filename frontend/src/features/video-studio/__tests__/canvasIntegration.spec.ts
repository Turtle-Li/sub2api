import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import type { PublicSettings } from '@/types'
import type { CreativeCanvas } from '@/features/creative-canvas/types'
import { clearCanvasDraftHandoff, stageCanvasDraftHandoff } from '@/features/creative-canvas/handoff'
import type { VideoJob, VideoModel } from '@/features/video-studio/api'
import { parseCanvasDraftQuery, verifyCanvasDraft } from '@/features/video-studio/canvasDraft'
import { clearImage, pendingImage } from '@/features/video-studio/source'
import VideoDelivery from '@/features/video-studio/VideoDelivery.vue'
import VideoGallery from '@/features/video-studio/VideoGallery.vue'
import VideoWorkspace from '@/features/video-studio/VideoWorkspace.vue'
import ImageStudioView from '@/views/user/ImageStudioView.vue'
import { createVideoPosterCache } from '@/features/video-studio/posterCache'

const { videoMock, canvasApiMock, readImageMock, studioPropsSpy } = vi.hoisted(() => ({
  videoMock: {
    models: vi.fn(),
    list: vi.fn(),
    get: vi.fn(),
    content: vi.fn(),
    poster: vi.fn(),
    create: vi.fn(),
  },
  canvasApiMock: {
    list: vi.fn(),
    create: vi.fn(),
    get: vi.fn(),
    save: vi.fn(),
    remove: vi.fn(),
    bindAttempt: vi.fn(),
  },
  readImageMock: vi.fn(),
  studioPropsSpy: vi.fn(),
}))

vi.mock('@/i18n', () => ({ getLocale: () => 'zh-CN' }))
vi.mock('@/features/video-studio/api', async importOriginal => ({
  ...await importOriginal<typeof import('@/features/video-studio/api')>(),
  videoStudio: videoMock,
}))
vi.mock('@/features/creative-canvas/api', async importOriginal => ({
  ...await importOriginal<typeof import('@/features/creative-canvas/api')>(),
  creativeCanvasApi: canvasApiMock,
}))
vi.mock('@/features/video-studio/source', async importOriginal => {
  const actual = await importOriginal<typeof import('@/features/video-studio/source')>()
  return {
    ...actual,
    readImage: (...args: Parameters<typeof actual.readImage>) => readImageMock(...args),
  }
})
vi.mock('@/features/image-studio/Studio', () => ({
  Studio: (props: Record<string, unknown>) => {
    studioPropsSpy(props)
    return null
  },
}))
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: defineComponent({
    name: 'AppLayoutStub',
    setup(_, { slots }) {
      return () => h('div', { class: 'app-layout-stub' }, slots.default?.())
    },
  }),
}))

const OWNER_ID = 42
const CANVAS_ID = '11111111-1111-4111-8111-111111111111'
const NODE_IMAGE_ID = '22222222-2222-4222-8222-222222222222'
const NODE_DRAFT_ID = '33333333-3333-4333-8333-333333333333'
const VIDEO_JOB_ID = '44444444-4444-4444-8444-444444444444'
const REQUEST_ID = '55555555-5555-4555-8555-555555555555'
const PNG_DATA_URL = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC'

const museModel: VideoModel = {
  id: 'muse-video',
  label: 'Muse Video',
  provider: 'muse',
  duration_mode: 'automatic',
  min_seconds: 5,
  max_seconds: 10,
  default_seconds: 6,
  sizes: ['16:9', '9:16'],
  supports_frames: false,
  supports_seed: false,
  max_references: 4,
  reference_kinds: ['image'],
  quotes: { '6': 1.2 },
  currency: 'USD',
  quote_id: 'q'.repeat(64),
}

function makeCanvas(overrides: Partial<CreativeCanvas> = {}): CreativeCanvas {
  return {
    id: CANVAS_ID,
    title: '海岸分镜画布',
    revision: 3,
    updated_at: '2026-11-21T04:00:00Z',
    document: {
      viewport: { x: 0, y: 0, zoom: 1 },
      nodes: [
        {
          id: NODE_IMAGE_ID,
          kind: 'image',
          x: 40,
          y: 40,
          title: '灯塔原图',
          image: { route: 12, batchId: 'imgbatch_01', itemId: 'item_01', index: 0 },
        },
        {
          id: NODE_DRAFT_ID,
          kind: 'video-draft',
          x: 360,
          y: 40,
          title: '分支 A · 推进镜头',
          prompt: '镜头缓慢推近灯塔，晨雾轻拂海面。',
        },
      ],
      edges: [{ source: NODE_IMAGE_ID, target: NODE_DRAFT_ID }],
    },
    assets: {
      [NODE_IMAGE_ID]: { available: true },
    },
    attempts: [],
    ...overrides,
  }
}

function makeCompletedJob(overrides: Partial<VideoJob> = {}): VideoJob {
  return {
    id: VIDEO_JOB_ID,
    model: 'muse-video',
    status: 'completed',
    seconds: 6,
    size: '16:9',
    created_at: '2026-11-21T04:00:00Z',
    has_poster: false,
    ...overrides,
  }
}

function signIn(userId: number) {
  localStorage.setItem('auth_token', 'test-token')
  localStorage.setItem('auth_user', JSON.stringify({ id: userId }))
  useAuthStore().$patch({ user: { id: userId } as never, token: 'test-token' })
}

function setFlags(flags: Partial<PublicSettings>) {
  useAppStore().cachedPublicSettings = {
    creative_canvas_enabled: false,
    video_studio_enabled: true,
    ...flags,
  } as PublicSettings
}

function makeTestRouter(initialPath = '/video-studio') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/image-studio', name: 'ImageStudio', component: { template: '<div />' } },
      { path: '/video-studio', name: 'VideoStudio', component: { template: '<div />' } },
      { path: '/creative-canvas', name: 'CreativeCanvas', component: { template: '<div />' } },
    ],
  })
  void router.push(initialPath)
  return router
}

beforeEach(() => {
  vi.resetAllMocks()
  setActivePinia(createPinia())
  signIn(OWNER_ID)
  setFlags({ creative_canvas_enabled: true, video_studio_enabled: true })
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  Object.defineProperties(HTMLDialogElement.prototype, {
    showModal: { configurable: true, value: vi.fn(function (this: HTMLDialogElement) { this.setAttribute('open', '') }) },
    close: { configurable: true, value: vi.fn(function (this: HTMLDialogElement) { this.removeAttribute('open'); this.dispatchEvent(new Event('close')) }) },
  })
  vi.stubGlobal('IntersectionObserver', class {
    observe() {}
    unobserve() {}
    disconnect() {}
  })
  vi.spyOn(crypto, 'randomUUID').mockReturnValue(REQUEST_ID)
  videoMock.models.mockResolvedValue({ enabled: true, items: [museModel] })
  videoMock.list.mockResolvedValue({ items: [] })
  videoMock.poster.mockRejectedValue({ status: 404 })
  canvasApiMock.list.mockResolvedValue([{ id: CANVAS_ID, title: '海岸分镜画布', revision: 3, updated_at: '2026-11-21T04:00:00Z' }])
  readImageMock.mockResolvedValue(PNG_DATA_URL)
})

afterEach(() => {
  clearCanvasDraftHandoff()
  clearImage()
  document.body.replaceChildren()
  localStorage.clear()
  sessionStorage.clear()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('parseCanvasDraftQuery', () => {
  it('supports canvas/node and aliases canvasId/nodeId/board/draft while ignoring prompt/locator/media', () => {
    expect(parseCanvasDraftQuery({})).toEqual({ present: false, valid: false, canvasId: '', nodeId: '' })
    expect(parseCanvasDraftQuery({
      canvas: CANVAS_ID,
      node: NODE_DRAFT_ID,
      prompt: 'injected prompt',
      image: 'https://evil.invalid/a.png',
    })).toEqual({ present: true, valid: true, canvasId: CANVAS_ID, nodeId: NODE_DRAFT_ID })

    expect(parseCanvasDraftQuery({ canvasId: CANVAS_ID, nodeId: NODE_DRAFT_ID }).valid).toBe(true)
    expect(parseCanvasDraftQuery({ board: CANVAS_ID, draft: NODE_DRAFT_ID }).valid).toBe(true)
    expect(parseCanvasDraftQuery({ canvas: 'not-a-uuid', node: NODE_DRAFT_ID })).toEqual({
      present: true,
      valid: false,
      canvasId: 'not-a-uuid',
      nodeId: NODE_DRAFT_ID,
    })
  })
})

describe('ImageStudioView -> AddToCanvasDialog', () => {
  it('hides onCanvas when creative_canvas_enabled is off, and opens AddToCanvasDialog with a Promise when on', async () => {
    setFlags({ creative_canvas_enabled: false, video_studio_enabled: true })
    const router = makeTestRouter('/image-studio')
    await router.isReady()

    const wrapper = mount(ImageStudioView, {
      global: { plugins: [router] },
      attachTo: document.body,
    })
    await flushPromises()
    await vi.waitFor(() => expect(studioPropsSpy).toHaveBeenCalled())
    expect(studioPropsSpy.mock.lastCall?.[0]?.onCanvas).toBeUndefined()

    setFlags({ creative_canvas_enabled: true, video_studio_enabled: true })
    await flushPromises()
    await vi.waitFor(() => expect(typeof studioPropsSpy.mock.lastCall?.[0]?.onCanvas).toBe('function'))
    const onCanvas = studioPropsSpy.mock.lastCall?.[0]?.onCanvas as ((orig: unknown) => Promise<void>) | undefined
    expect(typeof onCanvas).toBe('function')

    const promise = onCanvas!({ route: 12, batchId: 'imgbatch_01', itemId: 'item_01', index: 0 })
    expect(promise).toBeInstanceOf(Promise)
    await promise
    await flushPromises()
    expect(wrapper.text()).toContain('加入创作画布')

    // Switching account closes the dialog immediately
    signIn(99)
    await flushPromises()
    expect(wrapper.text()).not.toContain('加入创作画布')
    wrapper.unmount()
  })
})

describe('VideoGallery & VideoDelivery AddToCanvas entry', () => {
  it('shows 加入画布 only for completed jobs when creative_canvas_enabled is true', async () => {
    const completed = makeCompletedJob()
    const running = makeCompletedJob({ id: '66666666-6666-4666-8666-666666666666', status: 'running' })
    videoMock.list.mockResolvedValue({ items: [completed, running] })

    const offGallery = mount(VideoGallery, {
      props: { outstanding: [], example: '示例', mobile: false, canvasEnabled: false },
      attachTo: document.body,
    })
    await flushPromises()
    expect(offGallery.find('.vs-canvas-button').exists()).toBe(false)
    offGallery.unmount()

    const onGallery = mount(VideoGallery, {
      props: { outstanding: [], example: '示例', mobile: false, canvasEnabled: true },
      attachTo: document.body,
    })
    await flushPromises()
    const canvasButtons = onGallery.findAll('.vs-canvas-button')
    expect(canvasButtons).toHaveLength(1)
    await canvasButtons[0].trigger('click')
    expect(onGallery.emitted('canvas')?.[0]?.[0]).toEqual(completed)
    onGallery.unmount()

    const cache = createVideoPosterCache()
    const delivery = mount(VideoDelivery, {
      props: { mobile: false, cache, jobs: [completed, running], canvasEnabled: true },
      attachTo: document.body,
    })
    const opener = document.createElement('button')
    document.body.appendChild(opener)
    await (delivery.vm as InstanceType<typeof VideoDelivery>).open(completed, 'details', opener)
    await flushPromises()
    const deliveryCanvasBtn = delivery.get('.vs-delivery-canvas-btn')
    await deliveryCanvasBtn.trigger('click')
    expect(delivery.emitted('canvas')?.[0]?.[0]).toEqual(completed)
    delivery.unmount()
    cache.dispose()
  })
})

describe('Canvas -> Video Studio round-trip verification & safe bind-before-create', () => {
  it('re-verifies board on refresh, restores original via prepareImage, fills draft.prompt, binds revision before Muse create (without seconds)', async () => {
    const board = makeCanvas()
    canvasApiMock.get.mockResolvedValue(board)
    canvasApiMock.bindAttempt.mockResolvedValue(undefined)
    videoMock.create.mockResolvedValue(makeCompletedJob({ id: REQUEST_ID, status: 'queued' }))

    const router = makeTestRouter(`/video-studio?canvas=${CANVAS_ID}&node=${NODE_DRAFT_ID}`)
    await router.isReady()

    const wrapper = mount(VideoWorkspace, {
      props: { userId: OWNER_ID },
      global: { plugins: [router] },
      attachTo: document.body,
    })
    await flushPromises()

    expect(canvasApiMock.get).toHaveBeenCalledWith(CANVAS_ID, expect.any(AbortSignal))
    expect(pendingImage(OWNER_ID)).not.toBeNull()
    expect((wrapper.get('textarea[aria-label="画面与运动"]').element as HTMLTextAreaElement).value).toBe('镜头缓慢推近灯塔，晨雾轻拂海面。')
    expect(wrapper.text()).toContain('返回画布')

    const callOrder: string[] = []
    canvasApiMock.bindAttempt.mockImplementationOnce(async () => { callOrder.push('bind') })
    videoMock.create.mockImplementationOnce(async (body: Record<string, unknown>) => {
      callOrder.push('create')
      expect(body.request_id).toBe(REQUEST_ID)
      expect('seconds' in body).toBe(false)
      return makeCompletedJob({ id: REQUEST_ID, status: 'queued' })
    })

    await wrapper.get('form.vs-form').trigger('submit')
    await flushPromises()

    expect(callOrder).toEqual(['bind', 'create'])
    expect(canvasApiMock.bindAttempt).toHaveBeenCalledWith(
      CANVAS_ID,
      NODE_DRAFT_ID,
      REQUEST_ID,
      3,
      expect.any(AbortSignal),
    )
    expect(videoMock.create).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('refuses on bindAttempt 409 conflict without calling videoStudio.create or retrying', async () => {
    canvasApiMock.get.mockResolvedValue(makeCanvas())
    canvasApiMock.bindAttempt.mockRejectedValueOnce({ status: 409, message: 'revision conflict' })

    const router = makeTestRouter(`/video-studio?canvas=${CANVAS_ID}&node=${NODE_DRAFT_ID}`)
    await router.isReady()

    const wrapper = mount(VideoWorkspace, {
      props: { userId: OWNER_ID },
      global: { plugins: [router] },
      attachTo: document.body,
    })
    await flushPromises()

    await wrapper.get('form.vs-form').trigger('submit')
    await flushPromises()

    expect(canvasApiMock.bindAttempt).toHaveBeenCalledTimes(1)
    expect(videoMock.create).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('修订号冲突')
    wrapper.unmount()
  })

  it('blocks submit while verifying, when source image is unavailable, or when board returns 403, and hides 返回画布', async () => {
    let resolveBoard!: (canvas: CreativeCanvas) => void
    canvasApiMock.get.mockImplementationOnce(() => new Promise<CreativeCanvas>(resolve => { resolveBoard = resolve }))

    const router = makeTestRouter(`/video-studio?canvas=${CANVAS_ID}&node=${NODE_DRAFT_ID}`)
    await router.isReady()

    const wrapper = mount(VideoWorkspace, {
      props: { userId: OWNER_ID },
      global: { plugins: [router] },
      attachTo: document.body,
    })
    await flushPromises()

    // While slow/verifying: cannot submit, no 返回画布
    expect(wrapper.text()).toContain('正在验证画布草稿')
    expect(wrapper.text()).not.toContain('返回画布')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()

    // Resolve with unavailable source image
    resolveBoard(makeCanvas({ assets: { [NODE_IMAGE_ID]: { available: false } } }))
    await flushPromises()
    expect(wrapper.text()).toContain('画布来源原图不可用')
    expect(wrapper.text()).not.toContain('返回画布')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    expect(pendingImage(OWNER_ID)).toBeNull()
    wrapper.unmount()

    // 403 private reference denied
    canvasApiMock.get.mockRejectedValueOnce({ status: 403 })
    const result403 = await verifyCanvasDraft({
      userId: OWNER_ID,
      canvasId: CANVAS_ID,
      nodeId: NODE_DRAFT_ID,
      canvasEnabled: true,
      signal: new AbortController().signal,
    })
    expect(result403.state.status).toBe('source-unavailable')
    if (result403.state.status === 'source-unavailable') {
      expect(result403.state.message).toContain('私有引用')
    }
  })

  it('keeps heldId query-only on refresh (never starts a new paid task) and warns on attempt.unavailable', async () => {
    sessionStorage.setItem(
      `sub2:video-studio:intents:${OWNER_ID}`,
      JSON.stringify({ held: REQUEST_ID, pending: [REQUEST_ID] }),
    )
    canvasApiMock.get.mockResolvedValue(makeCanvas({
      attempts: [
        { node_id: NODE_DRAFT_ID, request_id: REQUEST_ID, created_at: '2026-11-21T04:01:00Z', unavailable: true },
      ],
    }))

    const router = makeTestRouter(`/video-studio?canvas=${CANVAS_ID}&node=${NODE_DRAFT_ID}`)
    await router.isReady()

    const wrapper = mount(VideoWorkspace, {
      props: { userId: OWNER_ID },
      global: { plugins: [router] },
      attachTo: document.body,
    })
    await flushPromises()

    expect(wrapper.text()).toContain('已锁定原任务号')
    expect(wrapper.text()).toContain(REQUEST_ID)
    expect(wrapper.text()).toContain('私有任务已过期或授权已失效')
    expect(wrapper.find('button[type="submit"]').exists()).toBe(false)
    expect(videoMock.create).not.toHaveBeenCalled()
    expect(canvasApiMock.bindAttempt).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('detaches canvas draft on 清除草稿 so subsequent submissions never mis-bind', async () => {
    stageCanvasDraftHandoff(OWNER_ID, {
      canvasId: CANVAS_ID,
      nodeId: NODE_DRAFT_ID,
      revision: 3,
      canvasTitle: '海岸分镜画布',
      draftTitle: '分支 A',
      prompt: '草稿提示词',
      expectsSourceImage: true,
    })
    canvasApiMock.get.mockResolvedValue(makeCanvas())
    videoMock.create.mockResolvedValue(makeCompletedJob({ id: REQUEST_ID, status: 'queued' }))

    const router = makeTestRouter(`/video-studio?canvas=${CANVAS_ID}&node=${NODE_DRAFT_ID}`)
    await router.isReady()

    const wrapper = mount(VideoWorkspace, {
      props: { userId: OWNER_ID },
      global: { plugins: [router] },
      attachTo: document.body,
    })
    await flushPromises()

    const clearBtn = wrapper.findAll('.vs-draft button').find(b => b.text().trim() === '清除草稿')!
    await clearBtn.trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.query.canvas).toBeUndefined()
    expect(pendingImage(OWNER_ID)).toBeNull()

    await wrapper.get('textarea[aria-label="画面与运动"]').setValue('普通工作台新提示词')
    await wrapper.get('form.vs-form').trigger('submit')
    await flushPromises()

    expect(canvasApiMock.bindAttempt).not.toHaveBeenCalled()
    expect(videoMock.create).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})
