import { describe, expect, it } from 'vitest'
import { findDerivedDraftNodes, findSourceImageNode, isValidCanvasConnection, sanitizeDocument } from '../document'
import type { CanvasAttempt, CanvasNodeDoc, CanvasDocument } from '../types'
import { placeNewCanvasNode, resolveDraftTakes } from '../upstreamAdapter'
import type { VideoJob } from '@/features/video-studio/api'

const id = (n: number) => `10000000-0000-4000-8000-${String(n).padStart(12, '0')}`
const img: CanvasNodeDoc = { id: id(1), kind: 'image', x: 30, y: 50, image: { route: 0, batchId: 'user-batch', itemId: 'item', index: 1 } }
const draft: CanvasNodeDoc = { id: id(2), kind: 'video-draft', x: 500, y: 42, prompt: '缓慢推近' }
const other = { ...draft, id: id(3) }
const document: CanvasDocument = { nodes: [img, draft, other], edges: [{ source: img.id, target: draft.id }, { source: img.id, target: other.id }], viewport: { x: -22, y: 17, zoom: 0.82 } }
const receipt = (request: number, node: string, status: VideoJob['status'] = 'completed'): CanvasAttempt => {
  const created_at = `2026-07-01T10:00:${String(request).padStart(2, '0')}Z`
  return { node_id: node, created_at, request_id: id(request), job: { id: id(request), model: 'muse-video', status, seconds: 5, size: '16:9', created_at } }
}

describe('Creative Canvas studio adapter contracts', () => {
  it('retains the exact wallet route, image index, branches and viewport without persisting media or renderer state', () => {
    const dirty = { ...document, nodes: [{ ...img, selected: true, previewURL: 'blob:ephemeral', image: { ...img.image!, extra: 'never-persist' } }, draft, other] }
    expect(sanitizeDocument(dirty)).toStrictEqual(document)
    expect(findSourceImageNode(document, draft.id)?.image?.index).toBe(1)
    expect(findDerivedDraftNodes(document, img.id).map(x => x.id)).toEqual([draft.id, other.id])
  })

  it('retains one image parent per draft but multiple outgoing branches and a note edge are valid', () => {
    const img2: CanvasNodeDoc = { ...img, id: id(4) }
    const note: CanvasNodeDoc = { id: id(5), kind: 'note', x: 1, y: 9 }
    const doc = { ...document, nodes: [...document.nodes, img2, note] }
    expect(isValidCanvasConnection(doc, img2.id, draft.id)).toBe(false)
    expect(isValidCanvasConnection(doc, note.id, draft.id)).toBe(true)
    const noteEdge = { source: note.id, target: draft.id }
    expect(sanitizeDocument({ ...doc, edges: [...doc.edges, { source: img2.id, target: draft.id }, noteEdge, doc.edges[0]!] }).edges).toEqual([...doc.edges, noteEdge])
  })

  it('places siblings beside their measured source without moving the original or stacking on its sibling', () => {
    const sizes = new Map([[img.id, { width: 510, height: 290 }]])
    const options = { anchorNode: img, measuredSizes: sizes, kind: 'video-draft' as const }
    const a = placeNewCanvasNode({ ...options, existingNodes: [img] })
    const b = placeNewCanvasNode({ ...options, existingNodes: [img, { ...draft, ...a }] })
    expect(a.x).toBeGreaterThanOrEqual(img.x + 510)
    expect(b).not.toEqual(a)
    expect(img.x).toBe(30)
  })

  it('resolves unordered receipts only for their branch and preserves an older explicit choice', () => {
    const rows = [receipt(13, other.id), receipt(12, draft.id), receipt(11, draft.id)]
    expect(resolveDraftTakes(draft, rows).activeTake?.requestId).toBe(id(12))
    const r = resolveDraftTakes({ ...draft, pinned_id: id(11) }, rows)
    expect(r.takes.map(x => x.requestId)).toEqual([id(11), id(12)])
    expect(r.activeTake?.requestId).toBe(id(11))
    expect(resolveDraftTakes({ ...draft, pinned_id: id(13) }, rows).activeTake?.requestId).toBe(id(12))
  })

  it('never substitutes a failed or withdrawn take for an available completed generation', () => {
    const rows = [receipt(11, draft.id), receipt(12, draft.id, 'failed'), { node_id: draft.id, request_id: id(13), created_at: '2026-07-02T00:00:00Z', unavailable: true }]
    const r = resolveDraftTakes({ ...draft, pinned_id: id(13) }, rows)
    expect(r.activeTake?.requestId).toBe(id(11))
    expect(r.takes.find(x => x.requestId === id(13))).toMatchObject({ unavailable: true, isActive: false })
  })
})
