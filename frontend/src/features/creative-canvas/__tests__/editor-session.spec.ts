import { effectScope, type EffectScope } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useCanvasEditor } from '../useCanvasEditor'
import type { CreativeCanvas, CanvasDocument } from '../types'

const mocks = vi.hoisted(() => ({ list: vi.fn(), get: vi.fn(), save: vi.fn(), push: vi.fn() }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: mocks.push }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAuthenticated: true, user: { id: 9 } }) }))
vi.mock('../api', async original => ({ ...await original<typeof import('../api')>(), creativeCanvasApi: mocks }))
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
afterEach(() => { scope.stop(); localStorage.clear(); sessionStorage.clear() })

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
})
