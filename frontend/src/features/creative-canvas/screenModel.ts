import type { Edge, Node } from '@xyflow/vue'
import type { VideoJob } from '@/features/video-studio/api'
import {
  branchLetter,
  findDerivedDraftNodes,
  findParentMediaEdge,
  isMediaEdgeKinds,
  resolveDraftLineage,
} from './document'
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

export function buildBranchLabelMap(
  doc: CanvasDocument,
  attempts: readonly CanvasAttempt[] = [],
): Map<string, string> {
  const labels = new Map<string, string>()
  for (const node of doc.nodes) {
    if (node.kind === 'note') continue
    const derived = findDerivedDraftNodes(doc, node.id)
    if (!derived.length) continue
    const parentTakes = node.kind === 'video-draft' ? buildTakesForDraft(node, attempts) : []
    derived.forEach((draft, idx) => {
      const letter = branchLetter(idx)
      if (node.kind === 'image') {
        labels.set(draft.id, `分支 ${letter}`)
        return
      }
      if (node.kind === 'video') {
        labels.set(draft.id, derived.length > 1 ? `成片尾帧 · ${letter}` : '成片尾帧续镜')
        return
      }
      const edge = doc.edges.find(item => item.source === node.id && item.target === draft.id)
      if (edge?.take_id) {
        const matched = parentTakes.find(take => take.requestId === edge.take_id)
        const vTag = matched ? `v${matched.takeNumber}` : '成片'
        labels.set(draft.id, derived.length > 1 ? `${vTag} 尾帧 · ${letter}` : `${vTag} 尾帧续镜`)
      } else {
        labels.set(draft.id, derived.length > 1 ? `沿用素材 · ${letter}` : '沿用此镜素材')
      }
    })
  }
  return labels
}

export function buildBranchIndexMap(doc: CanvasDocument): Map<string, number> {
  const indices = new Map<string, number>()
  for (const node of doc.nodes) {
    if (node.kind === 'note') continue
    const derived = findDerivedDraftNodes(doc, node.id)
    derived.forEach((draft, idx) => {
      indices.set(draft.id, idx)
    })
  }
  return indices
}

export function buildCompareEntries(
  doc: CanvasDocument,
  parentNodeId: string,
  attempts: readonly CanvasAttempt[],
): CompareBranchEntry[] {
  const drafts = findDerivedDraftNodes(doc, parentNodeId)
  return drafts.map(node => {
    const takes = buildTakesForDraft(node, attempts)
    return {
      node,
      takes,
      activeTake: activeTakeFromList(takes),
    }
  })
}

export function describeDraftProvenance(
  doc: CanvasDocument,
  draftId: string,
  attempts: readonly CanvasAttempt[],
): {
  readonly badge: string
  readonly detail: string
  readonly mode: ReturnType<typeof resolveDraftLineage>['mode']
} {
  const lineage = resolveDraftLineage(doc, draftId)
  if (lineage.mode === 'direct-image' && lineage.parentNode) {
    const idx = (lineage.parentNode.image?.index ?? 0) + 1
    return {
      mode: lineage.mode,
      badge: `原图第 ${idx} 张`,
      detail: `基于「${lineage.parentNode.title || '图片原图'}」（第 ${idx} 张），原图保持不动`,
    }
  }
  if (lineage.mode === 'take-tail' && lineage.parentNode) {
    const takes = buildTakesForDraft(lineage.parentNode, attempts)
    const matched = takes.find(item => item.requestId === lineage.takeId)
    const vTag = matched ? `v${matched.takeNumber}` : '已固定版本'
    return {
      mode: lineage.mode,
      badge: `用 ${vTag} 成片尾帧续镜`,
      detail: `已固定「${lineage.parentNode.title || '上一镜'}」的 ${vTag} 成片尾帧，父节点后续切换版本不会改变本镜输入`,
    }
  }
  if (lineage.mode === 'imported-video-tail' && lineage.parentNode) {
    return {
      mode: lineage.mode,
      badge: '用视频成片尾帧续镜',
      detail: `基于「${lineage.parentNode.title || '视频作品'}」尾帧续接下一镜，确认生成时本机提取 PNG`,
    }
  }
  if (lineage.mode === 'inherit-input' && lineage.parentNode) {
    const sub = lineage.image
      ? `（沿用「${lineage.originNode?.title || '上游原图'}」第 ${lineage.image.index + 1} 张）`
      : lineage.videoId
        ? `（沿用上游已固定成片尾帧）`
        : '（沿用纯文字构思）'
    return {
      mode: lineage.mode,
      badge: '沿用此镜素材',
      detail: `沿用「${lineage.parentNode.title || '上一镜'}」的输入素材${sub}，父节点后续出片不会改变本镜`,
    }
  }
  return {
    mode: 'none',
    badge: '纯文字草稿',
    detail: '无关联图片或视频输入，直接由文字描述生成镜头',
  }
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
  multiSelectedIds?: ReadonlySet<string>,
): Node<FlowNodeData>[] {
  const existingById = new Map<string, Node<FlowNodeData>>()
  for (const item of current) {
    existingById.set(item.id, item)
  }
  return doc.nodes.map(docNode => {
    const prev = existingById.get(docNode.id)
    const selected = multiSelectedIds?.size
      ? multiSelectedIds.has(docNode.id)
      : docNode.id === selectedId
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
    const srcKind = nodeKindById.get(edge.source)
    const dstKind = nodeKindById.get(edge.target)
    const isBranchEdge = Boolean(srcKind && dstKind && isMediaEdgeKinds(srcKind, dstKind))
    const isContinuationEdge = isBranchEdge && srcKind !== 'image'
    const branchIdx = indices.get(edge.target)
    const classes = ['cc-edge', isBranchEdge ? 'cc-edge-branch' : 'cc-edge-note']
    if (isContinuationEdge) {
      classes.push(edge.take_id ? 'cc-edge-tail' : 'cc-edge-inherit')
    }
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
    if (selected && selected.kind !== 'note' && findDerivedDraftNodes(doc, selected.id).length > 0) {
      return selected.id
    }
    if (selected?.kind === 'video-draft') {
      const parent = findParentMediaEdge(doc, selected.id)
      if (parent) return parent.sourceNode.id
    }
    if (selected?.kind === 'image') return selected.id
  }
  const parentWithChildren = doc.nodes.find(
    n => n.kind !== 'note' && findDerivedDraftNodes(doc, n.id).length > 0,
  )
  if (parentWithChildren) return parentWithChildren.id
  const firstImage = doc.nodes.find(n => n.kind === 'image')
  return firstImage?.id ?? null
}

const kindSearchNames: Readonly<Record<CanvasNodeDoc['kind'], string>> = {
  image: '图片原图 生图作品',
  video: '视频作品 导入视频',
  'video-draft': '视频草稿 分镜 续镜',
  note: '灵感便签 笔记',
}

export function searchCanvasNodes(
  doc: CanvasDocument,
  query: string,
  branchLabels?: ReadonlyMap<string, string>,
): CanvasNodeDoc[] {
  const q = query.trim().toLowerCase()
  if (!q) return [...doc.nodes]
  return doc.nodes.filter(node => {
    const title = (node.title || '').toLowerCase()
    const prompt = (node.prompt || '').toLowerCase()
    const branch = (branchLabels?.get(node.id) || '').toLowerCase()
    const kindName = kindSearchNames[node.kind].toLowerCase()
    return title.includes(q) || prompt.includes(q) || branch.includes(q) || kindName.includes(q)
  })
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
