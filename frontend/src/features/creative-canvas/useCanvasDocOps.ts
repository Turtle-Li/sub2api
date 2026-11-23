import type { Ref } from 'vue'
import {
  branchLetter,
  createCanvasUuid,
  findDerivedDraftNodes,
  finiteNumber,
  isCanvasUuid,
  isValidCanvasConnection,
  MAX_CANVAS_EDGES,
  MAX_CANVAS_NODES,
  MAX_NODE_TITLE_LENGTH,
  MAX_PROMPT_LENGTH,
  resolveDraftLineage,
  sanitizeDocument,
  sanitizeImageLocator,
  sanitizeViewport,
} from './document'
import type { CanvasDocument, CanvasEdgeDoc, CanvasNodeDoc, CanvasViewport, CreativeCanvas } from './types'
import { placeNewCanvasNode, resolveDraftTakes } from './upstreamAdapter'

export function isCanvasDirty(
  board: CreativeCanvas | null,
  doc: CanvasDocument,
  title: string,
): boolean {
  if (!board) return false
  if (title !== board.title) return true
  return JSON.stringify(sanitizeDocument(doc)) !== JSON.stringify(board.document)
}

export function useCanvasDocOps(
  board: Ref<CreativeCanvas | null>,
  doc: Ref<CanvasDocument>,
  commit: () => void,
) {
  function canSourceNodeBranch(currentBoard: CreativeCanvas, currentDoc: CanvasDocument, sourceNode: CanvasNodeDoc): {
    readonly ok: boolean
    readonly takeId?: string
    readonly takeNumber?: number
  } {
    if (sourceNode.kind === 'image') {
      if (!sanitizeImageLocator(sourceNode.image)) return { ok: false }
      if (currentBoard.assets[sourceNode.id]?.available !== true) return { ok: false }
      return { ok: true }
    }
    if (sourceNode.kind === 'video') {
      if (!isCanvasUuid(sourceNode.video_id)) return { ok: false }
      const asset = currentBoard.assets[sourceNode.id]
      if (asset?.available !== true || asset.job?.status !== 'completed') return { ok: false }
      return { ok: true }
    }
    if (sourceNode.kind === 'video-draft') {
      const { activeTake } = resolveDraftTakes(sourceNode, currentBoard.attempts)
      if (
        activeTake &&
        !activeTake.unavailable &&
        activeTake.job?.status === 'completed' &&
        isCanvasUuid(activeTake.requestId)
      ) {
        return { ok: true, takeId: activeTake.requestId, takeNumber: activeTake.takeNumber }
      }
      // Unrendered draft: inherits sourceNode's input, unless that upstream input is known unavailable.
      const lineage = resolveDraftLineage(currentDoc, sourceNode.id)
      if (lineage.mode === 'direct-image' && lineage.parentNode) {
        if (currentBoard.assets[lineage.parentNode.id]?.available !== true) return { ok: false }
      } else if (lineage.continuation) {
        if (currentBoard.inputs && currentBoard.inputs[sourceNode.id]?.available === false) {
          return { ok: false }
        }
      }
      return { ok: true }
    }
    return { ok: false }
  }

  function newDraft(sourceNodeId: string, at?: { x: number; y: number }): string | null {
    if (!board.value) return null
    const current = doc.value
    if (current.nodes.length >= MAX_CANVAS_NODES || current.edges.length >= MAX_CANVAS_EDGES) {
      return null
    }
    const sourceNode = current.nodes.find(
      n => n.id === sourceNodeId && (n.kind === 'image' || n.kind === 'video' || n.kind === 'video-draft'),
    )
    if (!sourceNode) return null
    const check = canSourceNodeBranch(board.value, current, sourceNode)
    if (!check.ok) return null

    const pos = placeNewCanvasNode({
      existingNodes: current.nodes,
      anchorNode: sourceNode,
      explicitDropPosition: at ?? null,
      kind: 'video-draft',
    })
    const count = findDerivedDraftNodes(current, sourceNodeId).length
    const letter = branchLetter(count)
    const title =
      sourceNode.kind === 'image'
        ? `视频分支 ${letter}`
        : sourceNode.kind === 'video'
          ? `续镜 ${letter} · 成片尾帧`
          : check.takeId
            ? `续镜 ${letter} · v${check.takeNumber ?? 1} 尾帧`
            : `续镜 ${letter} · 沿用素材`

    const id = createCanvasUuid()
    const draftNode: CanvasNodeDoc = {
      id,
      kind: 'video-draft',
      x: pos.x,
      y: pos.y,
      title,
    }
    const edge: CanvasEdgeDoc = check.takeId
      ? { source: sourceNodeId, target: id, take_id: check.takeId }
      : { source: sourceNodeId, target: id }

    doc.value = sanitizeDocument({
      viewport: current.viewport,
      nodes: [...current.nodes, draftNode],
      edges: [...current.edges, edge],
    })
    commit()
    return id
  }

  function addRootDraft(at?: { x: number; y: number }): string | null {
    if (!board.value) return null
    const current = doc.value
    if (current.nodes.length >= MAX_CANVAS_NODES) return null
    const pos = placeNewCanvasNode({
      existingNodes: current.nodes,
      explicitDropPosition: at ?? null,
      kind: 'video-draft',
    })
    const id = createCanvasUuid()
    const draftNode: CanvasNodeDoc = {
      id,
      kind: 'video-draft',
      x: pos.x,
      y: pos.y,
      title: '纯文字分镜草稿',
    }
    doc.value = sanitizeDocument({
      viewport: current.viewport,
      nodes: [...current.nodes, draftNode],
      edges: current.edges,
    })
    commit()
    return id
  }

  function addNote(at?: { x: number; y: number }): string | null {
    if (!board.value) return null
    const current = doc.value
    if (current.nodes.length >= MAX_CANVAS_NODES) return null
    const pos = placeNewCanvasNode({
      existingNodes: current.nodes,
      explicitDropPosition: at ?? null,
      kind: 'note',
    })
    const id = createCanvasUuid()
    const noteNode: CanvasNodeDoc = { id, kind: 'note', x: pos.x, y: pos.y, title: '便签' }
    doc.value = sanitizeDocument({
      viewport: current.viewport,
      nodes: [...current.nodes, noteNode],
      edges: current.edges,
    })
    commit()
    return id
  }

  function connect(sourceId: string, targetId: string): void {
    if (!board.value) return
    const current = doc.value
    if (!isValidCanvasConnection(current, sourceId, targetId, board.value.attempts)) return
    const sourceNode = current.nodes.find(n => n.id === sourceId)
    const targetNode = current.nodes.find(n => n.id === targetId)
    if (!sourceNode || !targetNode) return
    let edge: CanvasEdgeDoc = { source: sourceId, target: targetId }
    if (sourceNode.kind !== 'note') {
      const check = canSourceNodeBranch(board.value, current, sourceNode)
      if (!check.ok) return
      if (check.takeId) {
        edge = { source: sourceId, target: targetId, take_id: check.takeId }
      }
    }
    doc.value = sanitizeDocument({
      viewport: current.viewport,
      nodes: current.nodes,
      edges: [...current.edges, edge],
    })
    commit()
  }

  function removeNode(id: string): void {
    if (!board.value) return
    const current = doc.value
    if (!current.nodes.some(n => n.id === id)) return
    doc.value = sanitizeDocument({
      viewport: current.viewport,
      nodes: current.nodes.filter(n => n.id !== id),
      edges: current.edges.filter(e => e.source !== id && e.target !== id),
    })
    commit()
  }

  function changeNode(id: string, patch: { title?: string; prompt?: string }): void {
    if (!board.value) return
    const current = doc.value
    let changed = false
    const nodes = current.nodes.map(node => {
      if (node.id !== id) return node
      const nextTitle = patch.title !== undefined ? patch.title.slice(0, MAX_NODE_TITLE_LENGTH) : node.title
      const nextPrompt = patch.prompt !== undefined ? patch.prompt.slice(0, MAX_PROMPT_LENGTH) : node.prompt
      if (nextTitle === node.title && nextPrompt === node.prompt) return node
      changed = true
      return { ...node, title: nextTitle, prompt: nextPrompt }
    })
    if (!changed) return
    doc.value = sanitizeDocument({ viewport: current.viewport, nodes, edges: current.edges })
  }

  function moveNodes(points: { id: string; x: number; y: number }[]): void {
    if (!board.value || !points.length) return
    const map = new Map(points.map(p => [p.id, p]))
    const current = doc.value
    let changed = false
    const nodes = current.nodes.map(node => {
      const pt = map.get(node.id)
      if (!pt) return node
      const x = Math.round(finiteNumber(pt.x, node.x) * 100) / 100
      const y = Math.round(finiteNumber(pt.y, node.y) * 100) / 100
      if (x === node.x && y === node.y) return node
      changed = true
      return { ...node, x, y }
    })
    if (!changed) return
    doc.value = sanitizeDocument({ viewport: current.viewport, nodes, edges: current.edges })
    commit()
  }

  function changeViewport(v: CanvasViewport): void {
    if (!board.value) return
    const next = sanitizeViewport(v)
    const cur = doc.value.viewport
    if (next.x === cur.x && next.y === cur.y && next.zoom === cur.zoom) return
    doc.value = { viewport: next, nodes: doc.value.nodes, edges: doc.value.edges }
  }

  function pin(draftId: string, requestId: string): void {
    if (!board.value || !isCanvasUuid(requestId)) return
    const current = doc.value
    const target = current.nodes.find(n => n.id === draftId && n.kind === 'video-draft')
    if (!target || target.pinned_id === requestId) return
    const { takes } = resolveDraftTakes(target, board.value.attempts)
    if (!takes.some(t => t.requestId === requestId && !t.unavailable)) return
    const nodes = current.nodes.map(n => (n.id === draftId ? { ...n, pinned_id: requestId } : n))
    doc.value = sanitizeDocument({ viewport: current.viewport, nodes, edges: current.edges })
    commit()
  }

  return {
    newDraft,
    branchFromNode: newDraft,
    addRootDraft,
    addNote,
    connect,
    removeNode,
    changeNode,
    moveNodes,
    changeViewport,
    pin,
  }
}
