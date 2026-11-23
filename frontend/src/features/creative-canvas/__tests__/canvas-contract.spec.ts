import { describe, expect, it } from 'vitest'
import {
  findDerivedDraftNodes,
  findSourceImageNode,
  findUpstreamChain,
  isValidCanvasConnection,
  resolveDraftLineage,
  sanitizeDocument,
} from '../document'
import { buildBranchLabelMap, describeDraftProvenance, searchCanvasNodes } from '../screenModel'
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

  it('supports v2 3-layer lineage, strips illegal take_id on non-draft sources, and protects pinned take_id from parent switching', () => {
    const importedVideo: CanvasNodeDoc = { id: id(20), kind: 'video', x: 40, y: 360, title: '导入海浪成片', video_id: id(21) }
    const shotB: CanvasNodeDoc = { id: id(22), kind: 'video-draft', x: 500, y: 40, title: '第二镜 B', prompt: '海鸥掠过礁石', pinned_id: id(31) }
    const shotD: CanvasNodeDoc = { id: id(23), kind: 'video-draft', x: 920, y: 40, title: '第三镜 D', prompt: '镜头跟随海鸥升空' }
    const shotUnrendered: CanvasNodeDoc = { id: id(24), kind: 'video-draft', x: 500, y: 360, title: '未出片草稿', prompt: '暴雨前夕' }
    const shotInherit: CanvasNodeDoc = { id: id(25), kind: 'video-draft', x: 920, y: 360, title: '沿用素材草稿', prompt: '闪电划破乌云' }
    const shotFromVideo: CanvasNodeDoc = { id: id(26), kind: 'video-draft', x: 500, y: 680, title: '成片续镜', prompt: '浪花退去' }

    const rawDoc: CanvasDocument = {
      viewport: { x: 0, y: 0, zoom: 1 },
      nodes: [img, importedVideo, shotB, shotD, shotUnrendered, shotInherit, shotFromVideo],
      edges: [
        // take_id on image -> video-draft must be stripped
        { source: img.id, target: shotB.id, take_id: id(99) },
        // take_id on video-draft -> video-draft MUST be preserved (pinned to B1 = id(31))
        { source: shotB.id, target: shotD.id, take_id: id(31) },
        // unrendered video-draft -> video-draft omits take_id and inherits upstream image
        { source: img.id, target: shotUnrendered.id },
        { source: shotUnrendered.id, target: shotInherit.id },
        // take_id on video -> video-draft MUST be stripped by sanitizeDocument per Go v2 contract
        { source: importedVideo.id, target: shotFromVideo.id, take_id: id(21) },
      ],
    }

    const clean = sanitizeDocument(rawDoc)
    expect(clean.edges).toEqual([
      { source: img.id, target: shotB.id },
      { source: shotB.id, target: shotD.id, take_id: id(31) },
      { source: img.id, target: shotUnrendered.id },
      { source: shotUnrendered.id, target: shotInherit.id },
      { source: importedVideo.id, target: shotFromVideo.id },
    ])

    const attempts: CanvasAttempt[] = [
      receipt(31, shotB.id, 'completed'), // B1
      receipt(32, shotB.id, 'completed'), // B2
    ]

    // Even when parent shotB switches its active/pinned take to B2 (id(32)), shotD's lineage stays pinned to B1 (id(31))!
    const switchedDoc: CanvasDocument = {
      ...clean,
      nodes: clean.nodes.map(n => (n.id === shotB.id ? { ...n, pinned_id: id(32) } : n)),
    }
    const lineageD = resolveDraftLineage(switchedDoc, shotD.id)
    expect(lineageD).toMatchObject({
      mode: 'take-tail',
      continuation: true,
      videoId: id(31),
      takeId: id(31),
    })
    expect(describeDraftProvenance(switchedDoc, shotD.id, attempts).badge).toBe('用 v1 成片尾帧续镜')

    // Unrendered draft inheritance: even if shotUnrendered later gains a completed attempt, shotInherit has no take_id so it keeps inheriting img!
    const attemptsAfterUnrenderedCompletes = [...attempts, receipt(33, shotUnrendered.id, 'completed')]
    const lineageInherit = resolveDraftLineage(switchedDoc, shotInherit.id)
    expect(lineageInherit).toMatchObject({
      mode: 'inherit-input',
      continuation: true,
      image: img.image,
      originNode: img,
    })
    expect(describeDraftProvenance(switchedDoc, shotInherit.id, attemptsAfterUnrenderedCompletes).badge).toBe('沿用此镜素材')

    // Imported video continuation resolves via node.video_id without take_id
    expect(resolveDraftLineage(switchedDoc, shotFromVideo.id)).toMatchObject({
      mode: 'imported-video-tail',
      continuation: true,
      videoId: id(21),
    })

    // Upstream chain & search
    expect(findUpstreamChain(switchedDoc, shotD.id).map(n => n.id)).toEqual([img.id, shotB.id])
    const labels = buildBranchLabelMap(switchedDoc, attempts)
    expect(searchCanvasNodes(switchedDoc, '海鸥', labels).map(n => n.id)).toEqual([shotB.id, shotD.id])
    expect(searchCanvasNodes(switchedDoc, 'v1 尾帧', labels).map(n => n.id)).toEqual([shotD.id])
  })

  it('forbids cycles, note->video edges, and changing media source on drafts that already have bound attempts', () => {
    const videoNode: CanvasNodeDoc = { id: id(40), kind: 'video', x: 0, y: 0, video_id: id(41) }
    const noteNode: CanvasNodeDoc = { id: id(42), kind: 'note', x: 0, y: 120, prompt: '备注' }
    const d1: CanvasNodeDoc = { id: id(43), kind: 'video-draft', x: 300, y: 0, prompt: '镜头 1' }
    const d2: CanvasNodeDoc = { id: id(44), kind: 'video-draft', x: 600, y: 0, prompt: '镜头 2' }
    const doc: CanvasDocument = {
      viewport: { x: 0, y: 0, zoom: 1 },
      nodes: [img, videoNode, noteNode, d1, d2],
      edges: [{ source: d1.id, target: d2.id, take_id: id(50) }],
    }

    // Cycle d2 -> d1 rejected
    expect(isValidCanvasConnection(doc, d2.id, d1.id)).toBe(false)
    // note -> video rejected per Go backend contract
    expect(isValidCanvasConnection(doc, noteNode.id, videoNode.id)).toBe(false)
    // Unbound d1 can receive media parent img
    expect(isValidCanvasConnection(doc, img.id, d1.id, [])).toBe(true)
    // Once d1 has bound attempts, changing/adding its media parent is rejected upfront
    expect(isValidCanvasConnection(doc, img.id, d1.id, [receipt(50, d1.id)])).toBe(false)
    // Note edge to bound draft remains allowed
    expect(isValidCanvasConnection(doc, noteNode.id, d1.id, [receipt(50, d1.id)])).toBe(true)
  })
})
