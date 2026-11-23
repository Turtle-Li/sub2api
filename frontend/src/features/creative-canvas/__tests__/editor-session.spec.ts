import { effectScope, type EffectScope } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  CANVAS_AUTOSAVE_DEBOUNCE_MS,
  CANVAS_AUTOSAVE_MAX_WAIT_MS,
  useCanvasEditor,
} from '../useCanvasEditor'
import { createCanvasImageThumbCache } from '../imageThumbCache'
import type { CreativeCanvas, CanvasDocument } from '../types'

const mocks = vi.hoisted(() => ({
  list: vi.fn(),
  get: vi.fn(),
  save: vi.fn(),
  push: vi.fn(),
  thumbnail: vi.fn(),
  image: vi.fn(),
}))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: mocks.push }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAuthenticated: true, user: { id: 9 } }) }))
vi.mock('../api', async original => ({ ...await original<typeof import('../api')>(), creativeCanvasApi: mocks }))
vi.mock('@/features/image-studio/api', async original => ({
  ...await original<typeof import('@/features/image-studio/api')>(),
  studioAPI: () => ({ thumbnail: mocks.thumbnail, image: mocks.image }),
}))
const uuid = (n: number) => `b0000000-0000-4000-8000-${String(n).padStart(12, '0')}`
const imageId = uuid(3), draftId = uuid(4)
const board = (id = uuid(1)): CreativeCanvas => ({
  id, revision: 2, title: '工作画布', updated_at: '2026-07-01T10:00:00Z', attempts: [], assets: { [imageId]: { available: true } },
  document: { viewport: { x: 80, y: 20, zoom: 0.8 }, nodes: [{ id: imageId, kind: 'image', x: 10, y: 20, image: { route: 0, batchId: 'one', itemId: 'one', index: 0 } }, { id: draftId, kind: 'video-draft', x: 400, y: 0, prompt: '原镜头' }], edges: [{ source: imageId, target: draftId }] },
})
let scope: EffectScope
const init = () => scope.run(() => useCanvasEditor())!
beforeEach(() => {
  scope = effectScope()
  vi.resetAllMocks()
  localStorage.setItem('auth_user', JSON.stringify({ id: 9 })); localStorage.setItem('auth_token', 'test-session')
  mocks.list.mockResolvedValue([]); mocks.get.mockImplementation(async (id: string) => board(id))
})
afterEach(() => { scope.stop(); vi.useRealTimers(); localStorage.clear(); sessionStorage.clear() })

describe('canvas edit lifetime', () => {
  it('undoes the first edit to the loaded document, not an empty board; histories never cross board boundaries', async () => {
    const editor = init()
    for (const id of [uuid(1), uuid(2)]) {
      await editor.open(id); expect(editor.canUndo.value).toBe(false)
      expect(editor.newDraft(imageId)).toBeTruthy(); expect(editor.doc.value.nodes).toHaveLength(3)
      editor.undo(); expect(editor.doc.value).toEqual(board(id).document)
      expect(editor.canUndo.value).toBe(false); editor.redo(); expect(editor.doc.value.nodes).toHaveLength(3)
    }
  })
  it('retains edits made during a save and pins the next write to the revision actually acknowledged', async () => {
    const editor = init(); const current = board(); await editor.open(current.id)
    editor.changeNode(draftId, { prompt: '已提交版本' }); editor.commit()
    let resolve!: (reply: CreativeCanvas) => void
    let snapshot!: CanvasDocument
    mocks.save.mockImplementationOnce((_id: string, request: { document: CanvasDocument }) => {
      snapshot = JSON.parse(JSON.stringify(request.document))
      return new Promise<CreativeCanvas>(r => { resolve = r })
    })
    const saving = editor.save()
    await vi.waitFor(() => expect(mocks.save).toHaveBeenCalledOnce())
    editor.changeNode(draftId, { prompt: '保存时继续输入' }); editor.commit()
    resolve({ ...current, document: snapshot, revision: 3 }); await saving
    expect(editor.doc.value.nodes.find(n => n.id === draftId)?.prompt).toBe('保存时继续输入')
    expect(editor.board.value?.revision).toBe(3); expect(editor.dirty.value).toBe(true)
    mocks.save.mockResolvedValueOnce({ ...current, document: editor.doc.value, revision: 4 })
    await editor.save(); expect(mocks.save.mock.calls[1]?.[1].revision).toBe(3)
  })
  it('does not branch or hand off an image without an affirmative server asset grant', async () => {
    const editor = init(); const current = board(); mocks.get.mockResolvedValueOnce({ ...current, assets: {} })
    await editor.open(current.id); expect(editor.newDraft(imageId)).toBeNull(); await editor.toStudio(draftId)
    expect(mocks.push).not.toHaveBeenCalled(); expect(mocks.save).not.toHaveBeenCalled()
  })

  it('branches v2 continuations (pinning take_id only on completed video-draft, omitting on unrendered draft & imported video) and enforces server inputs[] before handoff', async () => {
    const takeB1 = uuid(11)
    const takeB2 = uuid(12)
    const importedVideoNodeId = uuid(20)
    const importedJobId = uuid(21)
    const unrenderedDraftId = uuid(22)

    const v2Board: CreativeCanvas = {
      ...board(),
      document: {
        viewport: { x: 80, y: 20, zoom: 0.8 },
        nodes: [
          { id: imageId, kind: 'image', x: 10, y: 20, image: { route: 0, batchId: 'one', itemId: 'one', index: 0 } },
          { id: draftId, kind: 'video-draft', x: 400, y: 0, prompt: '第二镜 B', pinned_id: takeB1 },
          { id: unrenderedDraftId, kind: 'video-draft', x: 400, y: 340, prompt: '未出片草稿' },
          { id: importedVideoNodeId, kind: 'video', x: 10, y: 340, video_id: importedJobId },
        ],
        edges: [
          { source: imageId, target: draftId },
          { source: imageId, target: unrenderedDraftId },
        ],
      },
      assets: {
        [imageId]: { available: true },
        [importedVideoNodeId]: {
          available: true,
          job: { id: importedJobId, model: 'muse-video', status: 'completed', seconds: 6, size: '16:9', created_at: '2026-07-01T10:00:00Z' },
        },
      },
      attempts: [
        {
          node_id: draftId,
          request_id: takeB1,
          created_at: '2026-07-01T10:01:00Z',
          job: { id: takeB1, model: 'muse-video', status: 'completed', seconds: 6, size: '16:9', created_at: '2026-07-01T10:01:00Z' },
        },
        {
          node_id: draftId,
          request_id: takeB2,
          created_at: '2026-07-01T10:02:00Z',
          job: { id: takeB2, model: 'muse-video', status: 'completed', seconds: 6, size: '16:9', created_at: '2026-07-01T10:02:00Z' },
        },
      ],
    }

    const editor = init()
    mocks.get.mockResolvedValueOnce(v2Board)
    await editor.open(v2Board.id)

    // 1. Branching from completed video-draft pins take_id = takeB1
    const shotDId = editor.newDraft(draftId)
    expect(shotDId).toBeTruthy()
    const edgeToD = editor.doc.value.edges.find(e => e.target === shotDId)
    expect(edgeToD).toEqual({ source: draftId, target: shotDId!, take_id: takeB1 })

    // 2. Branching from unrendered video-draft omits take_id
    const shotInheritId = editor.newDraft(unrenderedDraftId)
    expect(shotInheritId).toBeTruthy()
    expect(editor.doc.value.edges.find(e => e.target === shotInheritId)).toEqual({
      source: unrenderedDraftId,
      target: shotInheritId!,
    })

    // 3. Branching from imported completed video omits take_id
    const shotFromVideoId = editor.newDraft(importedVideoNodeId)
    expect(shotFromVideoId).toBeTruthy()
    expect(editor.doc.value.edges.find(e => e.target === shotFromVideoId)).toEqual({
      source: importedVideoNodeId,
      target: shotFromVideoId!,
    })

    // 4. Handoff fails closed when server save omits or marks inputs[shotDId] unavailable
    mocks.save.mockResolvedValueOnce({
      ...v2Board,
      revision: 3,
      document: editor.doc.value,
      inputs: { [shotDId!]: { video_id: takeB1, available: false } },
    })
    await editor.toStudio(shotDId!)
    expect(mocks.push).not.toHaveBeenCalled()
    expect(editor.error.value).toContain('不可用')

    // 5. Handoff succeeds when server affirms inputs[shotDId] = { video_id: takeB1, available: true }
    editor.changeNode(shotDId!, { prompt: '第三镜推进' })
    editor.commit()
    mocks.save.mockResolvedValueOnce({
      ...v2Board,
      revision: 4,
      document: editor.doc.value,
      inputs: { [shotDId!]: { video_id: takeB1, available: true } },
    })
    await editor.toStudio(shotDId!)
    expect(mocks.push).toHaveBeenCalledWith({
      path: '/video-studio',
      query: { canvas: v2Board.id, node: shotDId! },
    })
  })

  it('P2: auto-saves and hands off multi-level inherited imported video (A -> unrendered B -> C) when canvas has unsaved viewport edits', async () => {
    const importedNodeA = uuid(30)
    const importedJobA = uuid(31)
    const draftB = uuid(32)
    const draftC = uuid(33)

    const chainBoard: CreativeCanvas = {
      ...board(),
      document: {
        viewport: { x: 80, y: 20, zoom: 0.8 },
        nodes: [
          { id: importedNodeA, kind: 'video', x: 10, y: 20, video_id: importedJobA },
          { id: draftB, kind: 'video-draft', x: 340, y: 20, prompt: '草稿 B（尚未出片）' },
          { id: draftC, kind: 'video-draft', x: 680, y: 20, prompt: '继承草稿 C' },
        ],
        edges: [
          { source: importedNodeA, target: draftB },
          { source: draftB, target: draftC },
        ],
      },
      assets: {
        [importedNodeA]: {
          available: true,
          job: { id: importedJobA, model: 'muse-video', status: 'completed', seconds: 6, size: '16:9', created_at: '2026-07-01T10:00:00Z' },
        },
      },
      attempts: [],
      inputs: {
        [draftB]: { video_id: importedJobA, available: true },
        [draftC]: { video_id: importedJobA, available: true },
      },
    }

    const editor = init()
    mocks.get.mockResolvedValueOnce(chainBoard)
    await editor.open(chainBoard.id)

    // Make canvas dirty via unsaved viewport pan
    editor.changeViewport({ x: 120, y: 40, zoom: 0.85 })
    expect(editor.dirty.value).toBe(true)

    mocks.save.mockImplementationOnce(async (_id: string, req: { document: CanvasDocument }) => ({
      ...chainBoard,
      revision: 3,
      document: req.document,
      updated_at: '2026-07-01T10:05:00Z',
    }))

    await editor.toStudio(draftC)
    expect(editor.error.value).toBe('')
    expect(mocks.save).toHaveBeenCalledTimes(1)
    expect(mocks.push).toHaveBeenCalledWith({
      path: '/video-studio',
      query: { canvas: chainBoard.id, node: draftC },
    })
  })

  it('U5: autosaves after 1.5s debounce / 10s maxWait with server updated_at, never writes when clean, queues mid-save edits, and handles offline & 409', async () => {
    vi.useFakeTimers()
    const editor = init()
    const current = board()
    await editor.open(current.id)

    // 1. Clean board sends 0 PUTs over 20s
    await vi.advanceTimersByTimeAsync(20_000)
    expect(mocks.save).not.toHaveBeenCalled()

    // 2. Rename -> saves once after 1.5s debounce, records server updated_at
    mocks.save.mockImplementationOnce(async (_id: string, req: { title: string; document: CanvasDocument }) => ({
      ...current,
      revision: 3,
      title: req.title,
      document: req.document,
      updated_at: '2026-11-22T06:32:00Z',
    }))
    editor.rename('海岸新分镜')
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_DEBOUNCE_MS - 100)
    expect(mocks.save).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(150)
    expect(mocks.save).toHaveBeenCalledTimes(1)
    expect(editor.hasSavedInSession.value).toBe(true)
    expect(editor.lastSavedAt.value).toBe('2026-11-22T06:32:00Z')
    expect(editor.dirty.value).toBe(false)

    // 3. Continuous typing every 1000ms (< 1500ms debounce) -> snapshot fires at 10s maxWait
    mocks.save.mockImplementationOnce(async (_id: string, req: { title: string; document: CanvasDocument }) => ({
      ...current,
      revision: 4,
      title: req.title,
      document: req.document,
      updated_at: '2026-11-22T06:33:00Z',
    }))
    for (let step = 1; step <= 10; step++) {
      editor.rename(`连续输入-${step}`)
      await vi.advanceTimersByTimeAsync(1000)
    }
    expect(mocks.save).toHaveBeenCalledTimes(2)
    expect(editor.board.value?.revision).toBe(4)

    // 4. Offline error marks saveErrorKind = 'offline' without retry spam; manual retry recovers
    mocks.save.mockRejectedValueOnce({ status: 0, code: 'ERR_NETWORK', message: 'Network Error' })
    editor.rename('离线期间改动')
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_DEBOUNCE_MS + 50)
    expect(editor.saveErrorKind.value).toBe('offline')
    expect(editor.dirty.value).toBe(true)
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_MAX_WAIT_MS)
    expect(mocks.save).toHaveBeenCalledTimes(3) // no spam while offline

    mocks.save.mockImplementationOnce(async (_id: string, req: { title: string; document: CanvasDocument }) => ({
      ...current,
      revision: 5,
      title: req.title,
      document: req.document,
      updated_at: '2026-11-22T06:35:00Z',
    }))
    await editor.save()
    expect(editor.saveErrorKind.value).toBeNull()
    expect(editor.dirty.value).toBe(false)
    expect(editor.board.value?.revision).toBe(5)

    // 5. 409 conflict preserves local edits and pauses autosave
    mocks.save.mockRejectedValueOnce({ status: 409, message: 'Conflict' })
    editor.rename('本地保留版本')
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_DEBOUNCE_MS + 50)
    expect(editor.conflict.value).toBe(true)
    expect(editor.saveErrorKind.value).toBe('conflict')
    expect(editor.title.value).toBe('本地保留版本')
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_MAX_WAIT_MS)
    expect(mocks.save).toHaveBeenCalledTimes(5) // paused on conflict
  })

  it('U5 P1 regression: A -> B -> A while PUT(B) is in flight still syncs A with adjacent revision and single-flight PUTs', async () => {
    vi.useFakeTimers()
    const editor = init()
    const current = board()
    await editor.open(current.id)
    const initialTitle = editor.title.value

    let inFlight = 0
    let maxConcurrent = 0
    const sent: Array<{ revision: number; title: string }> = []
    let resolveFirst!: (reply: CreativeCanvas) => void
    let firstReq!: { revision: number; title: string; document: CanvasDocument }

    mocks.save.mockImplementationOnce((_id: string, req: { revision: number; title: string; document: CanvasDocument }) => {
      inFlight += 1
      maxConcurrent = Math.max(maxConcurrent, inFlight)
      sent.push({ revision: req.revision, title: req.title })
      firstReq = JSON.parse(JSON.stringify(req))
      return new Promise<CreativeCanvas>(resolve => {
        resolveFirst = reply => {
          inFlight -= 1
          resolve(reply)
        }
      })
    })

    // 1. A -> B triggers PUT #1 after 1.5s debounce
    editor.rename('中间标题 B')
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_DEBOUNCE_MS + 20)
    expect(mocks.save).toHaveBeenCalledTimes(1)
    expect(sent[0]).toEqual({ revision: 2, title: '中间标题 B' })
    expect(editor.saving.value).toBe(true)

    // 2. While PUT #1 is in flight, revert B -> A; dirty temporarily becomes false against old baseline A
    editor.rename(initialTitle)
    expect(editor.dirty.value).toBe(false)
    await vi.advanceTimersByTimeAsync(200)
    expect(mocks.save).toHaveBeenCalledTimes(1)

    // 3. PUT #1 resolves -> baseline updates to rev 3 ('中间标题 B'), so current 'A' is now dirty
    mocks.save.mockImplementationOnce(async (_id: string, req: { revision: number; title: string; document: CanvasDocument }) => {
      inFlight += 1
      maxConcurrent = Math.max(maxConcurrent, inFlight)
      sent.push({ revision: req.revision, title: req.title })
      const reply: CreativeCanvas = {
        ...current,
        revision: req.revision + 1,
        title: req.title,
        document: req.document,
        updated_at: '2026-11-22T06:41:00Z',
      }
      inFlight -= 1
      return reply
    })

    resolveFirst({
      ...current,
      revision: 3,
      title: firstReq.title,
      document: firstReq.document,
      updated_at: '2026-11-22T06:40:00Z',
    })
    await Promise.resolve()
    await Promise.resolve()

    expect(editor.board.value?.revision).toBe(3)
    expect(editor.board.value?.title).toBe('中间标题 B')
    expect(editor.title.value).toBe(initialTitle)
    expect(editor.dirty.value).toBe(true)
    expect(mocks.save).toHaveBeenCalledTimes(1)

    // 4. Without any further user input, reconciled autosave sends PUT #2 (rev 3 -> rev 4, title A)
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_DEBOUNCE_MS + 50)
    expect(mocks.save).toHaveBeenCalledTimes(2)
    expect(maxConcurrent).toBe(1)
    expect(sent).toEqual([
      { revision: 2, title: '中间标题 B' },
      { revision: 3, title: initialTitle },
    ])
    expect(editor.board.value?.revision).toBe(4)
    expect(editor.board.value?.title).toBe(initialTitle)
    expect(editor.title.value).toBe(initialTitle)
    expect(editor.dirty.value).toBe(false)
    expect(editor.lastSavedAt.value).toBe('2026-11-22T06:41:00Z')

    // 5. No extra PUTs once clean
    await vi.advanceTimersByTimeAsync(20_000)
    expect(mocks.save).toHaveBeenCalledTimes(2)
  })

  it('U5 P1 regression: document prompt revert (P1 -> P2 -> P1) and hidden-tab flush while PUT #1 is in flight both sync final state', async () => {
    vi.useFakeTimers()
    const editor = init()
    const current = board()
    await editor.open(current.id)

    let resolveFirst!: (reply: CreativeCanvas) => void
    let firstDoc!: CanvasDocument
    mocks.save.mockImplementationOnce((_id: string, req: { revision: number; document: CanvasDocument }) => {
      firstDoc = JSON.parse(JSON.stringify(req.document))
      return new Promise<CreativeCanvas>(resolve => {
        resolveFirst = resolve
      })
    })

    editor.changeNode(draftId, { prompt: '临时提示词 P2' })
    editor.commit()
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_DEBOUNCE_MS + 20)
    expect(mocks.save).toHaveBeenCalledTimes(1)

    // Undo back to P1 while PUT #1 is in flight, then hide tab
    editor.undo()
    expect(editor.doc.value.nodes.find(n => n.id === draftId)?.prompt).toBe('原镜头')
    expect(editor.dirty.value).toBe(false)

    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
    document.dispatchEvent(new Event('visibilitychange'))

    mocks.save.mockImplementationOnce(async (_id: string, req: { revision: number; document: CanvasDocument }) => ({
      ...current,
      revision: req.revision + 1,
      document: req.document,
      updated_at: '2026-11-22T06:45:00Z',
    }))

    try {
      resolveFirst({ ...current, revision: 3, document: firstDoc, updated_at: '2026-11-22T06:44:00Z' })
      await vi.waitFor(() => expect(mocks.save).toHaveBeenCalledTimes(2))
    } finally {
      delete (document as { visibilityState?: DocumentVisibilityState }).visibilityState
    }

    expect(mocks.save.mock.calls[1]?.[1].revision).toBe(3)
    expect(mocks.save.mock.calls[1]?.[1].document.nodes.find((n: { id: string }) => n.id === draftId)?.prompt).toBe('原镜头')
    expect(editor.board.value?.revision).toBe(4)
    expect(editor.dirty.value).toBe(false)
  })

  it('U5 P1 regression: A -> B -> C mid-flight keeps single-flight PUTs and adjacent revisions', async () => {
    vi.useFakeTimers()
    const editor = init()
    const current = board()
    await editor.open(current.id)

    let inFlight = 0
    let maxConcurrent = 0
    let resolveFirst!: (reply: CreativeCanvas) => void
    let firstReq!: { revision: number; title: string; document: CanvasDocument }

    mocks.save.mockImplementationOnce((_id: string, req: { revision: number; title: string; document: CanvasDocument }) => {
      inFlight += 1
      maxConcurrent = Math.max(maxConcurrent, inFlight)
      firstReq = JSON.parse(JSON.stringify(req))
      return new Promise<CreativeCanvas>(resolve => {
        resolveFirst = reply => {
          inFlight -= 1
          resolve(reply)
        }
      })
    })

    editor.rename('中间值 B')
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_DEBOUNCE_MS + 20)
    expect(mocks.save).toHaveBeenCalledTimes(1)

    // Edit B -> C and wait past debounce while PUT #1 is still in flight
    editor.rename('最终值 C')
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_DEBOUNCE_MS + 200)
    expect(mocks.save).toHaveBeenCalledTimes(1)
    expect(maxConcurrent).toBe(1)

    mocks.save.mockImplementationOnce(async (_id: string, req: { revision: number; title: string; document: CanvasDocument }) => {
      inFlight += 1
      maxConcurrent = Math.max(maxConcurrent, inFlight)
      const reply: CreativeCanvas = {
        ...current,
        revision: req.revision + 1,
        title: req.title,
        document: req.document,
        updated_at: '2026-11-22T06:51:00Z',
      }
      inFlight -= 1
      return reply
    })

    resolveFirst({
      ...current,
      revision: 3,
      title: firstReq.title,
      document: firstReq.document,
      updated_at: '2026-11-22T06:50:00Z',
    })
    await vi.advanceTimersByTimeAsync(100)

    expect(mocks.save).toHaveBeenCalledTimes(2)
    expect(maxConcurrent).toBe(1)
    expect(mocks.save.mock.calls[0]?.[1]).toMatchObject({ revision: 2, title: '中间值 B' })
    expect(mocks.save.mock.calls[1]?.[1]).toMatchObject({ revision: 3, title: '最终值 C' })
    expect(editor.board.value?.revision).toBe(4)
    expect(editor.board.value?.title).toBe('最终值 C')
    expect(editor.dirty.value).toBe(false)
  })

  it('U5 P1 regression: concurrent manual saves + handoff join one single-flight follow-up PUT (C and reverted A) and 409 does not overwrite', async () => {
    const editor = init()
    const current = board()
    await editor.open(current.id)

    let inFlight = 0
    let maxConcurrent = 0
    let resolveFirst!: (reply: CreativeCanvas) => void
    let resolveSecond!: (reply: CreativeCanvas) => void
    let firstReq!: { revision: number; title: string; document: CanvasDocument }
    let secondReq!: { revision: number; title: string; document: CanvasDocument }

    mocks.save
      .mockImplementationOnce((_id: string, req: { revision: number; title: string; document: CanvasDocument }) => {
        inFlight += 1
        maxConcurrent = Math.max(maxConcurrent, inFlight)
        firstReq = JSON.parse(JSON.stringify(req))
        return new Promise<CreativeCanvas>(resolve => {
          resolveFirst = reply => {
            inFlight -= 1
            resolve(reply)
          }
        })
      })
      .mockImplementationOnce((_id: string, req: { revision: number; title: string; document: CanvasDocument }) => {
        inFlight += 1
        maxConcurrent = Math.max(maxConcurrent, inFlight)
        secondReq = JSON.parse(JSON.stringify(req))
        return new Promise<CreativeCanvas>(resolve => {
          resolveSecond = reply => {
            inFlight -= 1
            resolve(reply)
          }
        })
      })

    editor.rename('在途 PUT 1')
    const p1 = editor.save()
    await vi.waitFor(() => expect(mocks.save).toHaveBeenCalledTimes(1))

    // Concurrent edits + two manual saves + handoff while PUT #1 is in flight
    editor.rename('最终交接标题')
    const manual1 = editor.save()
    const manual2 = editor.save()
    const handoffPromise = editor.toStudio(draftId)

    expect(mocks.save).toHaveBeenCalledTimes(1)
    expect(maxConcurrent).toBe(1)

    resolveFirst({
      ...current,
      revision: 3,
      title: firstReq.title,
      document: firstReq.document,
      updated_at: '2026-11-22T07:00:00Z',
    })

    await vi.waitFor(() => expect(mocks.save).toHaveBeenCalledTimes(2))
    expect(maxConcurrent).toBe(1)
    expect(secondReq).toMatchObject({ revision: 3, title: '最终交接标题' })

    resolveSecond({
      ...current,
      revision: 4,
      title: secondReq.title,
      document: secondReq.document,
      updated_at: '2026-11-22T07:01:00Z',
    })

    const [ok1, ok2, ok3] = await Promise.all([p1, manual1, manual2, handoffPromise])
    expect([ok1, ok2, ok3]).toEqual([true, true, true])
    expect(mocks.save).toHaveBeenCalledTimes(2)
    expect(maxConcurrent).toBe(1)
    expect(mocks.push).toHaveBeenCalledTimes(1)
    expect(editor.board.value?.revision).toBe(4)
    expect(editor.board.value?.title).toBe('最终交接标题')

    // Now verify A -> B (in flight) -> A revert joined by handoff hits 409 safely without overwriting or navigating
    let resolveThird!: (reply: CreativeCanvas) => void
    let thirdReq!: { revision: number; title: string; document: CanvasDocument }
    mocks.save
      .mockImplementationOnce((_id: string, req: { revision: number; title: string; document: CanvasDocument }) => {
        thirdReq = JSON.parse(JSON.stringify(req))
        return new Promise<CreativeCanvas>(resolve => {
          resolveThird = resolve
        })
      })
      .mockRejectedValueOnce({ status: 409, message: 'Conflict' })

    editor.rename('冲突前中间标题')
    const p3 = editor.save()
    await vi.waitFor(() => expect(mocks.save).toHaveBeenCalledTimes(3))
    editor.rename('最终交接标题') // revert back to rev 4 baseline
    const conflictedHandoff = editor.toStudio(draftId)

    resolveThird({
      ...current,
      revision: 5,
      title: thirdReq.title,
      document: thirdReq.document,
      updated_at: '2026-11-22T07:02:00Z',
    })
    await Promise.all([p3, conflictedHandoff])

    expect(mocks.save).toHaveBeenCalledTimes(4)
    expect(mocks.save.mock.calls[3]?.[1]).toMatchObject({ revision: 5, title: '最终交接标题' })
    expect(editor.conflict.value).toBe(true)
    expect(editor.saveErrorKind.value).toBe('conflict')
    expect(editor.title.value).toBe('最终交接标题')
    expect(mocks.push).toHaveBeenCalledTimes(1) // did NOT navigate on 409
  })

  it('U5 P1 regression: stale late PUT response or error from a previous board cannot affect a newly opened board', async () => {
    const editor = init()
    const board1 = { ...board(uuid(1)), title: '旧画布', revision: 2, updated_at: '2026-11-22T05:00:00Z' }
    const board2 = { ...board(uuid(2)), title: '新画布', revision: 7, updated_at: '2026-11-22T08:00:00Z' }
    mocks.get.mockImplementation(async (id: string) => (id === board1.id ? board1 : board2))

    await editor.open(board1.id)

    let resolveStale!: (reply: CreativeCanvas) => void
    mocks.save.mockImplementationOnce(() => new Promise<CreativeCanvas>(resolve => {
      resolveStale = resolve
    }))

    editor.rename('旧画布未完成修改')
    const staleSave = editor.save()
    await vi.waitFor(() => expect(mocks.save).toHaveBeenCalledTimes(1))

    // Switch to board 2 before board 1 PUT resolves
    await editor.open(board2.id)
    expect(editor.board.value?.id).toBe(board2.id)
    expect(editor.title.value).toBe('新画布')

    // Stale PUT for board 1 resolves late (ignoring AbortSignal)
    resolveStale({
      ...board1,
      revision: 3,
      title: '旧画布未完成修改',
      updated_at: '2026-11-22T09:59:00Z',
    })
    expect(await staleSave).toBe(false)

    // Board 2 remains completely unaffected
    expect(editor.board.value?.id).toBe(board2.id)
    expect(editor.board.value?.revision).toBe(7)
    expect(editor.title.value).toBe('新画布')
    expect(editor.lastSavedAt.value).toBe('2026-11-22T08:00:00Z')
    expect(editor.hasSavedInSession.value).toBe(false)
    expect(editor.conflict.value).toBe(false)
    expect(editor.dirty.value).toBe(false)
    expect(editor.saving.value).toBe(false)

    // Board 2 can still save cleanly without being blocked by stale PUT
    mocks.save.mockImplementationOnce(async (_id: string, req: { revision: number; title: string; document: CanvasDocument }) => ({
      ...board2,
      revision: req.revision + 1,
      title: req.title,
      document: req.document,
      updated_at: '2026-11-22T08:05:00Z',
    }))
    editor.rename('新画布更新')
    expect(await editor.save()).toBe(true)
    expect(mocks.save).toHaveBeenCalledTimes(2)
    expect(mocks.save.mock.calls[1]?.[0]).toBe(board2.id)
    expect(mocks.save.mock.calls[1]?.[1]).toMatchObject({ revision: 7, title: '新画布更新' })
    expect(editor.board.value?.revision).toBe(8)
  })

  it('P3: createCanvasImageThumbCache uses /thumbnail for index 0 and authorized image(index) for index > 0 without cross-using index 0', async () => {
    const createObjSpy = vi.fn(() => 'blob:canvas-idx')
    const revokeObjSpy = vi.fn()
    Object.defineProperties(URL, {
      createObjectURL: { configurable: true, value: createObjSpy },
      revokeObjectURL: { configurable: true, value: revokeObjSpy },
    })
    mocks.thumbnail.mockResolvedValueOnce(new Blob(['thumb0'], { type: 'image/webp' }))
    mocks.image.mockResolvedValueOnce(new Blob(['orig1'], { type: 'image/png' }))

    const cache = createCanvasImageThumbCache(9)
    await cache.ensure({ route: 0, batchId: 'b1', itemId: 'i1', index: 0 }, true)
    expect(mocks.thumbnail).toHaveBeenCalledTimes(1)
    expect(mocks.image).not.toHaveBeenCalled()
    expect(cache.get({ route: 0, batchId: 'b1', itemId: 'i1', index: 0 }).status).toBe('ready')

    await cache.ensure({ route: 0, batchId: 'b1', itemId: 'i1', index: 1 }, true)
    expect(mocks.thumbnail).toHaveBeenCalledTimes(1) // NEVER called for index 1
    expect(mocks.image).toHaveBeenCalledTimes(1)
    expect(mocks.image.mock.calls[0]?.[1]).toBe(1)
    expect(cache.get({ route: 0, batchId: 'b1', itemId: 'i1', index: 1 }).status).toBe('ready')

    cache.dispose()
    expect(revokeObjSpy).toHaveBeenCalledTimes(2)
  })

  it('U5 P1 regression: A -> B -> A lost reply (connectionreset) keeps saveUnconfirmed, retry uses original revision and 409 preserves local A without overwriting server B', async () => {
    vi.useFakeTimers()
    const editor = init()
    const current = board()
    await editor.open(current.id)
    const initialTitle = editor.title.value

    let rejectFirst!: (err: unknown) => void
    let firstSent!: { revision: number; title: string; document: CanvasDocument }
    mocks.save.mockImplementationOnce((_id: string, req: { revision: number; title: string; document: CanvasDocument }) => {
      firstSent = JSON.parse(JSON.stringify(req))
      return new Promise<CreativeCanvas>((_resolve, reject) => {
        rejectFirst = reject
      })
    })

    // 1. A -> B sends PUT #1 (rev 2); server commits B/v3, but browser hasn't received reply yet
    editor.rename('服务端已写入的中间标题 B')
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_DEBOUNCE_MS + 20)
    expect(mocks.save).toHaveBeenCalledTimes(1)
    expect(firstSent).toMatchObject({ revision: 2, title: '服务端已写入的中间标题 B' })

    // 2. User reverts B -> A before reply drops; dirty against stale baseline A is false
    editor.rename(initialTitle)
    expect(editor.dirty.value).toBe(false)

    // 3. Reply drops at transport layer (connectionreset)
    rejectFirst({ status: 0, code: 'ERR_NETWORK', message: 'Network Error' })
    await vi.waitFor(() => expect(editor.saving.value).toBe(false))

    expect(editor.title.value).toBe(initialTitle)
    expect(editor.dirty.value).toBe(false)
    expect(editor.saveUnconfirmed.value).toBe(true)
    expect(editor.saveErrorKind.value).toBe('offline')
    expect(editor.conflict.value).toBe(false)
    // Prompt/title must never be persisted to browser storage
    const readStorageValues = (storage: Storage) =>
      Array.from({ length: storage.length }, (_, idx) => storage.getItem(storage.key(idx) ?? '') ?? '')
    expect(readStorageValues(localStorage).some(v => v.includes('服务端已写入'))).toBe(false)
    expect(readStorageValues(sessionStorage).some(v => v.includes('服务端已写入'))).toBe(false)

    // 4. Online event must NOT bleach state back to saved; it keeps saveUnconfirmed and schedules verification
    window.dispatchEvent(new Event('online'))
    expect(editor.saveUnconfirmed.value).toBe(true)
    expect(editor.saveErrorKind.value).toBeNull()

    // 5. Explicit Retry sends PUT #2 with ORIGINAL revision 2 (never silently bumps revision);
    // server already has B/v3 so PUT #2 gets 409, and read-only GET returns B/v3 (!== local A).
    const serverBoardB: CreativeCanvas = {
      ...current,
      revision: 3,
      title: '服务端已写入的中间标题 B',
      document: firstSent.document,
      updated_at: '2026-11-22T07:10:00Z',
    }
    mocks.save.mockRejectedValueOnce({ status: 409, message: 'Conflict' })
    mocks.get.mockResolvedValueOnce(serverBoardB)

    const retryOk = await editor.save()
    expect(retryOk).toBe(false)
    expect(mocks.save).toHaveBeenCalledTimes(2)
    expect(mocks.save.mock.calls[1]?.[1]).toMatchObject({ revision: 2, title: initialTitle })
    expect(mocks.get).toHaveBeenCalledTimes(2) // initial open + read-only reconciliation check
    expect(editor.conflict.value).toBe(true)
    expect(editor.saveErrorKind.value).toBe('conflict')
    expect(editor.saveUnconfirmed.value).toBe(false)
    expect(editor.title.value).toBe(initialTitle) // local A retained, never silently discarded
    expect(editor.board.value?.revision).toBe(2) // server B/v3 NOT overwritten with a 3rd PUT

    // 6. Handoff to billing/Video Studio is blocked; no further PUT spam over 20s
    await editor.toStudio(draftId)
    expect(mocks.push).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(20_000)
    expect(mocks.save).toHaveBeenCalledTimes(2)
  })

  it('U5 P1 regression: handoff cannot bypass unconfirmed write after A -> B -> A timeout, and online/retry recovery reconciles cleanly without looping', async () => {
    vi.useFakeTimers()
    const editor = init()
    const current = board()
    await editor.open(current.id)
    const initialTitle = editor.title.value

    // Part 1: Timeout on PUT(B) after revert B -> A; immediate toStudio MUST verify via save() and abort on 409
    let rejectTimeout!: (err: unknown) => void
    mocks.save.mockImplementationOnce(() => new Promise<CreativeCanvas>((_r, reject) => {
      rejectTimeout = reject
    }))
    editor.rename('超时中间标题 B')
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_DEBOUNCE_MS + 20)
    editor.rename(initialTitle)
    rejectTimeout({ status: 0, code: 'ECONNABORTED', message: 'timeout of 30000ms exceeded' })
    await vi.waitFor(() => expect(editor.saving.value).toBe(false))

    expect(editor.dirty.value).toBe(false)
    expect(editor.saveUnconfirmed.value).toBe(true)

    mocks.save.mockRejectedValueOnce({ status: 409, message: 'Conflict' })
    mocks.get.mockResolvedValueOnce({ ...current, revision: 3, title: '超时中间标题 B' })
    await editor.toStudio(draftId)
    expect(mocks.save).toHaveBeenCalledTimes(2)
    expect(mocks.save.mock.calls[1]?.[1]).toMatchObject({ revision: 2, title: initialTitle })
    expect(editor.conflict.value).toBe(true)
    expect(mocks.push).not.toHaveBeenCalled()

    // Part 2: Reload clean board -> A -> B -> A where PUT(B) never reached server (still A/v2);
    // online event reconciles with PUT(rev 2, A) -> 200 (A/v3), then truly clean board never loops
    mocks.get.mockResolvedValueOnce(current)
    await editor.open(current.id)
    expect(editor.conflict.value).toBe(false)
    expect(editor.saveUnconfirmed.value).toBe(false)

    mocks.save.mockRejectedValueOnce({ status: 0, code: 'ERR_NETWORK', message: 'Network Error' })
    editor.rename('未送达服务器的 B')
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_DEBOUNCE_MS + 20)
    expect(editor.saveUnconfirmed.value).toBe(true)
    expect(editor.saveErrorKind.value).toBe('offline')

    // Simulate reverting while offline -> must keep saveUnconfirmed and NOT bleach to saved
    Object.defineProperty(navigator, 'onLine', { configurable: true, value: false })
    try {
      editor.rename(initialTitle)
      expect(editor.dirty.value).toBe(false)
      expect(editor.saveUnconfirmed.value).toBe(true)
      expect(editor.saveErrorKind.value).toBe('offline')
      await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_MAX_WAIT_MS)
      expect(mocks.save).toHaveBeenCalledTimes(3) // no spam while offline
    } finally {
      Object.defineProperty(navigator, 'onLine', { configurable: true, value: true })
    }

    mocks.save.mockImplementationOnce(async (_id: string, req: { revision: number; title: string; document: CanvasDocument }) => ({
      ...current,
      revision: req.revision + 1,
      title: req.title,
      document: req.document,
      updated_at: '2026-11-22T07:20:00Z',
    }))
    window.dispatchEvent(new Event('online'))
    expect(editor.saveUnconfirmed.value).toBe(true) // still unconfirmed until server replies
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_DEBOUNCE_MS + 50)

    expect(mocks.save).toHaveBeenCalledTimes(4)
    expect(mocks.save.mock.calls[3]?.[1]).toMatchObject({ revision: 2, title: initialTitle })
    expect(editor.saveUnconfirmed.value).toBe(false)
    expect(editor.dirty.value).toBe(false)
    expect(editor.board.value?.revision).toBe(3)
    expect(editor.lastSavedAt.value).toBe('2026-11-22T07:20:00Z')

    // Truly clean board sends 0 further PUTs across timers, online events, and manual save()
    window.dispatchEvent(new Event('online'))
    expect(await editor.save()).toBe(true)
    await vi.advanceTimersByTimeAsync(20_000)
    expect(mocks.save).toHaveBeenCalledTimes(4)
  })

  it('U5 P1 regression: unconfirmed retry confirms saved only when server state matches current content, and never overwrites another session on 409', async () => {
    const editor = init()
    const current = board()
    await editor.open(current.id)

    // Case 1: Server committed B/v3, browser got 504; user stays on B and clicks Retry -> 409 + read-only GET matches B/v3
    mocks.save.mockRejectedValueOnce({ status: 504, message: 'Gateway Timeout' })
    editor.rename('服务端已落盘的 B')
    expect(await editor.save()).toBe(false)
    expect(editor.saveUnconfirmed.value).toBe(true)

    const committedB: CreativeCanvas = {
      ...current,
      revision: 3,
      title: '服务端已落盘的 B',
      document: editor.doc.value,
      updated_at: '2026-11-22T07:30:00Z',
    }
    mocks.save.mockRejectedValueOnce({ status: 409, message: 'Conflict' })
    mocks.get.mockResolvedValueOnce(committedB)

    expect(await editor.save()).toBe(true)
    expect(mocks.save).toHaveBeenCalledTimes(2) // NO 3rd PUT
    expect(editor.conflict.value).toBe(false)
    expect(editor.saveUnconfirmed.value).toBe(false)
    expect(editor.dirty.value).toBe(false)
    expect(editor.board.value?.revision).toBe(3)
    expect(editor.lastSavedAt.value).toBe('2026-11-22T07:30:00Z')

    // Case 2: Server has another session's C/v4 after 504 -> 409 + read-only GET sees C/v4 (!== local D) -> conflict, NEVER overwrites C/v4
    mocks.save.mockRejectedValueOnce({ status: 504, message: 'Gateway Timeout' })
    editor.rename('本地改动 D')
    expect(await editor.save()).toBe(false)
    expect(editor.saveUnconfirmed.value).toBe(true)

    mocks.save.mockRejectedValueOnce({ status: 409, message: 'Conflict' })
    mocks.get.mockResolvedValueOnce({
      ...committedB,
      revision: 4,
      title: '另一会话的新标题 C',
      updated_at: '2026-11-22T07:31:00Z',
    })

    expect(await editor.save()).toBe(false)
    expect(mocks.save).toHaveBeenCalledTimes(4) // NEVER sends rev 4 PUT
    expect(mocks.save.mock.calls[3]?.[1]).toMatchObject({ revision: 3, title: '本地改动 D' })
    expect(editor.conflict.value).toBe(true)
    expect(editor.saveErrorKind.value).toBe('conflict')
    expect(editor.saveUnconfirmed.value).toBe(false)
    expect(editor.title.value).toBe('本地改动 D')
  })

  it('U5 P1 regression: definite 400 rejection clears cleanly on revert, whereas ambiguous 500 stays unconfirmed on revert', async () => {
    vi.useFakeTimers()
    const editor = init()
    const current = board()
    await editor.open(current.id)
    const initialTitle = editor.title.value

    // Definite 400 -> server definitely rejected without writing
    mocks.save.mockRejectedValueOnce({ status: 400, message: 'Bad Request' })
    editor.rename('非法草稿')
    expect(await editor.save()).toBe(false)
    expect(editor.saveErrorKind.value).toBe('error')
    expect(editor.saveUnconfirmed.value).toBe(false)

    editor.rename(initialTitle)
    await Promise.resolve()
    expect(editor.dirty.value).toBe(false)
    expect(editor.saveErrorKind.value).toBeNull()
    expect(editor.saveUnconfirmed.value).toBe(false)
    await vi.advanceTimersByTimeAsync(20_000)
    expect(mocks.save).toHaveBeenCalledTimes(1)

    // Ambiguous 500 -> server may have committed; reverting B -> A keeps saveUnconfirmed and verifies with rev 2
    mocks.save.mockRejectedValueOnce({ status: 500, message: 'Internal Server Error' })
    editor.rename('服务端 500 未知结果')
    expect(await editor.save()).toBe(false)
    expect(editor.saveErrorKind.value).toBe('error')
    expect(editor.saveUnconfirmed.value).toBe(true)

    mocks.save.mockRejectedValueOnce({ status: 409, message: 'Conflict' })
    mocks.get.mockResolvedValueOnce({ ...current, revision: 3, title: '服务端 500 未知结果' })
    editor.rename(initialTitle)
    expect(editor.saveUnconfirmed.value).toBe(true) // NOT bleached to saved
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_DEBOUNCE_MS + 50)
    expect(mocks.save).toHaveBeenCalledTimes(3)
    expect(mocks.save.mock.calls[2]?.[1]).toMatchObject({ revision: 2, title: initialTitle })
    expect(editor.conflict.value).toBe(true)
  })

  it('does not trigger background debounced autosave on viewport-only pan/zoom, while still persisting viewport on explicit save, hidden flush, handoff, and content edits', async () => {
    vi.useFakeTimers()
    const boardOne = board(uuid(1))
    const boardTwo = { ...board(uuid(2)), title: '另一块画布' }
    mocks.list.mockResolvedValueOnce([
      { id: boardTwo.id, title: boardTwo.title, revision: boardTwo.revision, updated_at: boardTwo.updated_at },
      { id: boardOne.id, title: boardOne.title, revision: boardOne.revision, updated_at: boardOne.updated_at },
    ])
    const editor = init()
    await editor.open(boardOne.id)
    expect(editor.boards.value.map(item => item.id)).toEqual([boardTwo.id, boardOne.id])

    // 1. Repeated viewport panning/zooming over 30s must NOT trigger background autosave PUTs
    for (let step = 1; step <= 6; step++) {
      editor.changeViewport({ x: 80 + step * 35, y: 20 + step * 15, zoom: 0.8 + step * 0.05 })
      await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_DEBOUNCE_MS + 500)
    }
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_MAX_WAIT_MS * 2)
    expect(editor.dirty.value).toBe(true)
    expect(editor.contentDirty.value).toBe(false)
    expect(mocks.save).not.toHaveBeenCalled()

    // 2. Explicit save() persists the updated viewport and preserves existing boards list order in place
    mocks.save.mockImplementationOnce(async (_id: string, req: { title: string; document: CanvasDocument }) => ({
      ...boardOne,
      revision: 3,
      title: req.title,
      document: req.document,
      updated_at: '2026-11-23T04:20:00Z',
    }))
    expect(await editor.save()).toBe(true)
    expect(mocks.save).toHaveBeenCalledTimes(1)
    expect(mocks.save.mock.calls[0]?.[1].document.viewport).toEqual({ x: 290, y: 110, zoom: 1.1 })
    expect(editor.dirty.value).toBe(false)
    expect(editor.boards.value.map(item => item.id)).toEqual([boardTwo.id, boardOne.id])
    expect(editor.boards.value[1]?.revision).toBe(3)

    // 3. Real content edit still autosaves after normal debounce and piggybacks any concurrent viewport pan
    editor.changeViewport({ x: 310, y: 140, zoom: 1.15 })
    editor.changeNode(draftId, { prompt: '真实镜头内容编辑' })
    expect(editor.contentDirty.value).toBe(true)
    mocks.save.mockImplementationOnce(async (_id: string, req: { title: string; document: CanvasDocument }) => ({
      ...boardOne,
      revision: 4,
      title: req.title,
      document: req.document,
      updated_at: '2026-11-23T04:21:00Z',
    }))
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_DEBOUNCE_MS + 50)
    expect(mocks.save).toHaveBeenCalledTimes(2)
    expect(mocks.save.mock.calls[1]?.[1].document.viewport).toEqual({ x: 310, y: 140, zoom: 1.15 })
    expect(mocks.save.mock.calls[1]?.[1].document.nodes.find((n: { id: string }) => n.id === draftId)?.prompt).toBe('真实镜头内容编辑')

    // 4. Subsequent viewport-only pan stays quiet until tab hides (visibilitychange flushes once)
    editor.changeViewport({ x: 10, y: 12, zoom: 0.75 })
    await vi.advanceTimersByTimeAsync(CANVAS_AUTOSAVE_MAX_WAIT_MS * 2)
    expect(mocks.save).toHaveBeenCalledTimes(2)

    mocks.save.mockImplementationOnce(async (_id: string, req: { title: string; document: CanvasDocument }) => ({
      ...boardOne,
      revision: 5,
      title: req.title,
      document: req.document,
      updated_at: '2026-11-23T04:22:00Z',
    }))
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
    try {
      document.dispatchEvent(new Event('visibilitychange'))
      await vi.waitFor(() => expect(mocks.save).toHaveBeenCalledTimes(3))
    } finally {
      delete (document as { visibilityState?: DocumentVisibilityState }).visibilityState
    }
    expect(mocks.save.mock.calls[2]?.[1].document.viewport).toEqual({ x: 10, y: 12, zoom: 0.75 })
    expect(editor.dirty.value).toBe(false)
  })
})
