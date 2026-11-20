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
  sanitizeDocument,
  sanitizeImageLocator,
  sanitizeViewport,
} from './document'
import type { CanvasDocument, CanvasNodeDoc, CanvasViewport, CreativeCanvas } from './types'
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
  function newDraft(imageId: string, at?: { x: number; y: number }): string | null {
    if (!board.value) return null
    const current = doc.value
    if (current.nodes.length >= MAX_CANVAS_NODES || current.edges.length >= MAX_CANVAS_EDGES) {
      return null
    }
    const imageNode = current.nodes.find(n => n.id === imageId && n.kind === 'image')
    if (!imageNode) return null
    if (!sanitizeImageLocator(imageNode.image)) return null
    if (board.value.assets[imageNode.id]?.available !== true) return null

    const pos = placeNewCanvasNode({
      existingNodes: current.nodes,
      anchorNode: imageNode,
      explicitDropPosition: at ?? null,
      kind: 'video-draft',
    })
    const count = findDerivedDraftNodes(current, imageId).length
    const id = createCanvasUuid()
    const draftNode: CanvasNodeDoc = {
      id,
      kind: 'video-draft',
      x: pos.x,
      y: pos.y,
      title: `视频分支 ${branchLetter(count)}`,
    }
    doc.value = sanitizeDocument({
      viewport: current.viewport,
      nodes: [...current.nodes, draftNode],
      edges: [...current.edges, { source: imageId, target: id }],
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
    if (!isValidCanvasConnection(current, sourceId, targetId)) return
    doc.value = sanitizeDocument({
      viewport: current.viewport,
      nodes: current.nodes,
      edges: [...current.edges, { source: sourceId, target: targetId }],
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

  return { newDraft, addNote, connect, removeNode, changeNode, moveNodes, changeViewport, pin }
}
