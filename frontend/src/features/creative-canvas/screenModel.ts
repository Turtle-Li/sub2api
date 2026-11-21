import type { Edge, Node } from '@xyflow/vue'
import type { VideoJob } from '@/features/video-studio/api'
import { branchLetter, findDerivedDraftNodes, findSourceImageNode } from './document'
import type { CompareBranchEntry } from './BranchCompareModal.vue'
import type {
  CanvasAttempt,
  CanvasDocument,
  CanvasNodeDoc,
  CanvasTake,
  CanvasViewport,
  CreativeCanvas,
} from './types'

export interface FlowNodeData extends Record<string, unknown> {
  readonly node: CanvasNodeDoc
}

export function buildTakesForDraft(
  draft: CanvasNodeDoc,
  attempts: readonly CanvasAttempt[],
): CanvasTake[] {
  const mine = attempts
    .filter(item => item.node_id === draft.id)
    .slice()
    .sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at))

  if (!mine.length) return []

  const pinnedExists = Boolean(
    draft.pinned_id && mine.some(item => item.request_id === draft.pinned_id),
  )
  const activeRequestId = pinnedExists
    ? draft.pinned_id!
    : mine[mine.length - 1].request_id

  return mine.map((item, index) => ({
    requestId: item.request_id,
    createdAt: item.created_at,
    unavailable: Boolean(item.unavailable),
    job: item.job,
    isPinned: item.request_id === draft.pinned_id,
    isActive: item.request_id === activeRequestId,
    takeNumber: index + 1,
  }))
}

export function activeTakeFromList(takes: readonly CanvasTake[]): CanvasTake | null {
  if (!takes.length) return null
  return takes.find(item => item.isActive) ?? takes[takes.length - 1] ?? null
}

export function buildBranchLabelMap(doc: CanvasDocument): Map<string, string> {
  const labels = new Map<string, string>()
  for (const node of doc.nodes) {
    if (node.kind !== 'image') continue
    const derived = findDerivedDraftNodes(doc, node.id)
    derived.forEach((draft, idx) => {
      labels.set(draft.id, `分支 ${branchLetter(idx)}`)
    })
  }
  return labels
}

export function buildBranchIndexMap(doc: CanvasDocument): Map<string, number> {
  const indices = new Map<string, number>()
  for (const node of doc.nodes) {
    if (node.kind !== 'image') continue
    const derived = findDerivedDraftNodes(doc, node.id)
    derived.forEach((draft, idx) => {
      indices.set(draft.id, idx)
    })
  }
  return indices
}

export function buildCompareEntries(
  doc: CanvasDocument,
  imageNodeId: string,
  attempts: readonly CanvasAttempt[],
): CompareBranchEntry[] {
  const drafts = findDerivedDraftNodes(doc, imageNodeId)
  return drafts.map(node => {
    const takes = buildTakesForDraft(node, attempts)
    return {
      node,
      takes,
      activeTake: activeTakeFromList(takes),
    }
  })
}

export function toFlowNodes(
  doc: CanvasDocument,
  selectedId: string | null,
): Node<FlowNodeData>[] {
  return doc.nodes.map(node => ({
    id: node.id,
    type: node.kind,
    position: { x: node.x, y: node.y },
    selected: node.id === selectedId,
    data: { node },
  }))
}

export function reconcileFlowNodes(
  current: readonly Node<FlowNodeData>[],
  doc: CanvasDocument,
  selectedId: string | null,
): Node<FlowNodeData>[] {
  const existingById = new Map<string, Node<FlowNodeData>>()
  for (const item of current) {
    existingById.set(item.id, item)
  }
  return doc.nodes.map(docNode => {
    const prev = existingById.get(docNode.id)
    const selected = docNode.id === selectedId
    if (!prev) {
      return {
        id: docNode.id,
        type: docNode.kind,
        position: { x: docNode.x, y: docNode.y },
        selected,
        data: { node: docNode },
      }
    }
    const keepPosition =
      Boolean(prev.dragging) ||
      (prev.position.x === docNode.x && prev.position.y === docNode.y)
    return {
      ...prev,
      type: docNode.kind,
      position: keepPosition ? prev.position : { x: docNode.x, y: docNode.y },
      selected,
      data: { node: docNode },
    }
  })
}

export function toFlowEdges(
  doc: CanvasDocument,
  selectedId: string | null,
  branchLabels: ReadonlyMap<string, string>,
  branchIndices?: ReadonlyMap<string, number>,
): Edge[] {
  const indices = branchIndices ?? buildBranchIndexMap(doc)
  const nodeKindById = new Map(doc.nodes.map(n => [n.id, n.kind] as const))
  return doc.edges.map(edge => {
    const active = selectedId === edge.source || selectedId === edge.target
    const label = branchLabels.get(edge.target)
    const isBranchEdge =
      nodeKindById.get(edge.source) === 'image' &&
      nodeKindById.get(edge.target) === 'video-draft'
    const branchIdx = indices.get(edge.target)
    const classes = ['cc-edge', isBranchEdge ? 'cc-edge-branch' : 'cc-edge-note']
    if (isBranchEdge && branchIdx !== undefined) {
      classes.push(`cc-branch-${branchIdx % 4}`)
    }
    if (active) classes.push('is-active')
    return {
      id: `${edge.source}->${edge.target}`,
      type: 'default',
      source: edge.source,
      target: edge.target,
      label: label || undefined,
      labelShowBg: Boolean(label),
      labelBgPadding: [8, 4] as [number, number],
      labelBgBorderRadius: 999,
      class: classes.join(' '),
      selectable: false,
    }
  })
}

export function reconcileFlowEdges(
  current: readonly Edge[],
  doc: CanvasDocument,
  selectedId: string | null,
  branchLabels: ReadonlyMap<string, string>,
  branchIndices?: ReadonlyMap<string, number>,
): Edge[] {
  const existingById = new Map<string, Edge>()
  for (const item of current) {
    existingById.set(item.id, item)
  }
  return toFlowEdges(doc, selectedId, branchLabels, branchIndices).map(next => {
    const prev = existingById.get(next.id)
    return prev ? { ...prev, ...next } : next
  })
}

export function collectBoardVideoJobs(board: CreativeCanvas | null): VideoJob[] {
  if (!board) return []
  const byId = new Map<string, VideoJob>()
  for (const status of Object.values(board.assets)) {
    if (status?.available !== false && status?.job) {
      byId.set(status.job.id, status.job)
    }
  }
  for (const attempt of board.attempts) {
    if (!attempt.unavailable && attempt.job) {
      byId.set(attempt.job.id, attempt.job)
    }
  }
  return Array.from(byId.values())
}

export function isViewportEqual(a: CanvasViewport, b: CanvasViewport): boolean {
  return (
    Math.abs(a.x - b.x) < 0.5 &&
    Math.abs(a.y - b.y) < 0.5 &&
    Math.abs(a.zoom - b.zoom) < 0.002
  )
}

export function resolveCompareImageId(
  doc: CanvasDocument,
  selectedNodeId: string | null,
): string | null {
  if (selectedNodeId) {
    const selected = doc.nodes.find(n => n.id === selectedNodeId)
    if (selected?.kind === 'image') return selected.id
    if (selected?.kind === 'video-draft') {
      const src = findSourceImageNode(doc, selected.id)
      if (src) return src.id
    }
  }
  const firstImage = doc.nodes.find(n => n.kind === 'image')
  return firstImage?.id ?? null
}

const boardTimeFormat = new Intl.DateTimeFormat('zh-CN', {
  month: 'short',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
})

export function formatBoardTime(iso: string): string {
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? iso : boardTimeFormat.format(date)
}
