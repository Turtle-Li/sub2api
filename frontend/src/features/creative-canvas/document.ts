import { isSafeStudioOriginal } from '@/features/image-studio/workspaceAPI'
import type {
  CanvasAttempt,
  CanvasDocument,
  CanvasDraftContinuationMode,
  CanvasEdgeDoc,
  CanvasImageLocator,
  CanvasNodeDoc,
  CanvasNodeKind,
  CanvasViewport,
} from './types'

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const ZERO_UUID = '00000000-0000-0000-0000-000000000000'
const VALID_KINDS = new Set<CanvasNodeKind>(['image', 'video', 'video-draft', 'note'])

export const MAX_BOARD_TITLE_LENGTH = 100
export const MAX_NODE_TITLE_LENGTH = 120
export const MAX_PROMPT_LENGTH = 4000
export const MAX_CANVAS_NODES = 160
export const MAX_CANVAS_EDGES = 320

export const DEFAULT_VIEWPORT: CanvasViewport = Object.freeze({ x: 0, y: 0, zoom: 1 })
export const DEFAULT_NODE_WIDTH = 296
export const DEFAULT_NODE_HEIGHT: Readonly<Record<CanvasNodeKind, number>> = Object.freeze({
  image: 344,
  video: 364,
  'video-draft': 412,
  note: 232,
})

export function isCanvasUuid(value: unknown): value is string {
  return typeof value === 'string' && UUID_RE.test(value) && value !== ZERO_UUID
}

export function createCanvasUuid(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

export function finiteNumber(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

export function clampZoom(zoom: unknown): number {
  const n = finiteNumber(zoom, 1)
  return Math.max(0.2, Math.min(2.2, Math.round(n * 1000) / 1000))
}

export function sanitizeViewport(raw: Partial<CanvasViewport> | null | undefined): CanvasViewport {
  return {
    x: Math.round(finiteNumber(raw?.x, 0) * 100) / 100,
    y: Math.round(finiteNumber(raw?.y, 0) * 100) / 100,
    zoom: clampZoom(raw?.zoom),
  }
}

export function sanitizeImageLocator(raw: unknown): CanvasImageLocator | undefined {
  if (!raw || typeof raw !== 'object') return undefined
  const candidate = raw as Partial<CanvasImageLocator>
  if (
    typeof candidate.route !== 'number' ||
    typeof candidate.batchId !== 'string' ||
    typeof candidate.itemId !== 'string' ||
    typeof candidate.index !== 'number'
  ) {
    return undefined
  }
  const clean: CanvasImageLocator = {
    route: candidate.route,
    batchId: candidate.batchId,
    itemId: candidate.itemId,
    index: candidate.index,
  }
  return isSafeStudioOriginal(clean) ? clean : undefined
}

function sanitizeOptionalText(value: unknown, maxLength: number): string | undefined {
  if (typeof value !== 'string') return undefined
  const trimmed = value.slice(0, maxLength)
  return trimmed.length > 0 ? trimmed : undefined
}

export function isMediaEdgeKinds(sourceKind: CanvasNodeKind, targetKind: CanvasNodeKind): boolean {
  return (
    targetKind === 'video-draft' &&
    (sourceKind === 'image' || sourceKind === 'video' || sourceKind === 'video-draft')
  )
}

export function isAllowedEdgeKinds(sourceKind: CanvasNodeKind, targetKind: CanvasNodeKind): boolean {
  if (isMediaEdgeKinds(sourceKind, targetKind)) return true
  if (sourceKind === 'note' && (targetKind === 'image' || targetKind === 'video-draft')) return true
  return false
}

/**
 * Strictly whitelists persisted canvas document fields.
 * Never persists Vue Flow internal fields, URLs, data:/blob: strings, expiresAt, or credentials.
 * Preserves legal 0 coordinates (x === 0, y === 0).
 */
export function sanitizeNode(raw: unknown): CanvasNodeDoc | null {
  if (!raw || typeof raw !== 'object') return null
  const input = raw as Partial<CanvasNodeDoc>
  if (!isCanvasUuid(input.id)) return null
  if (typeof input.kind !== 'string' || !VALID_KINDS.has(input.kind as CanvasNodeKind)) return null

  const kind = input.kind as CanvasNodeKind
  const node: {
    id: string
    kind: CanvasNodeKind
    x: number
    y: number
    title?: string
    prompt?: string
    image?: CanvasImageLocator
    video_id?: string
    pinned_id?: string
  } = {
    id: input.id,
    kind,
    x: Math.round(finiteNumber(input.x, 0) * 100) / 100,
    y: Math.round(finiteNumber(input.y, 0) * 100) / 100,
  }

  const title = sanitizeOptionalText(input.title, MAX_NODE_TITLE_LENGTH)
  if (title !== undefined) node.title = title

  const prompt = sanitizeOptionalText(input.prompt, MAX_PROMPT_LENGTH)
  if (prompt !== undefined) node.prompt = prompt

  if (kind === 'image') {
    const image = sanitizeImageLocator(input.image)
    if (image) node.image = image
  }

  if (kind === 'video' && isCanvasUuid(input.video_id)) {
    node.video_id = input.video_id
  }

  if (kind === 'video-draft' && isCanvasUuid(input.pinned_id)) {
    node.pinned_id = input.pinned_id
  }

  return node
}

export function wouldCreateCycle(
  edges: readonly CanvasEdgeDoc[],
  source: string,
  target: string,
): boolean {
  if (source === target) return true
  const adjacency = new Map<string, string[]>()
  for (const edge of edges) {
    const list = adjacency.get(edge.source)
    if (list) list.push(edge.target)
    else adjacency.set(edge.source, [edge.target])
  }
  const visited = new Set<string>()
  const stack = [target]
  while (stack.length) {
    const current = stack.pop()!
    if (current === source) return true
    if (visited.has(current)) continue
    visited.add(current)
    const next = adjacency.get(current)
    if (next) stack.push(...next)
  }
  return false
}

export function sanitizeDocument(raw: Partial<CanvasDocument> | null | undefined): CanvasDocument {
  const viewport = sanitizeViewport(raw?.viewport)
  const nodes: CanvasNodeDoc[] = []
  const nodeById = new Map<string, CanvasNodeDoc>()

  if (Array.isArray(raw?.nodes)) {
    for (const item of raw.nodes) {
      if (nodes.length >= MAX_CANVAS_NODES) break
      const clean = sanitizeNode(item)
      if (!clean || nodeById.has(clean.id)) continue
      nodeById.set(clean.id, clean)
      nodes.push(clean)
    }
  }

  const edges: CanvasEdgeDoc[] = []
  const edgeKeys = new Set<string>()
  const draftMediaParents = new Set<string>()
  if (Array.isArray(raw?.edges)) {
    for (const item of raw.edges) {
      if (edges.length >= MAX_CANVAS_EDGES) break
      if (!item || typeof item !== 'object') continue
      const candidate = item as Partial<CanvasEdgeDoc>
      const source = candidate.source
      const target = candidate.target
      if (!isCanvasUuid(source) || !isCanvasUuid(target)) continue
      if (source === target) continue
      const sourceNode = nodeById.get(source)
      const targetNode = nodeById.get(target)
      if (!sourceNode || !targetNode) continue
      if (!isAllowedEdgeKinds(sourceNode.kind, targetNode.kind)) continue
      const key = `${source}->${target}`
      if (edgeKeys.has(key)) continue
      const isMedia = isMediaEdgeKinds(sourceNode.kind, targetNode.kind)
      if (isMedia && draftMediaParents.has(target)) {
        continue
      }
      if (wouldCreateCycle(edges, source, target)) continue
      edgeKeys.add(key)
      if (isMedia) {
        draftMediaParents.add(target)
      }
      const takeId =
        sourceNode.kind === 'video-draft' && isCanvasUuid(candidate.take_id)
          ? candidate.take_id
          : undefined
      edges.push(takeId ? { source, target, take_id: takeId } : { source, target })
    }
  }

  return { viewport, nodes, edges }
}

export function emptyCanvasDocument(): CanvasDocument {
  return {
    viewport: { ...DEFAULT_VIEWPORT },
    nodes: [],
    edges: [],
  }
}

export function cloneCanvasDocument(doc: CanvasDocument): CanvasDocument {
  return sanitizeDocument(doc)
}

export function findParentMediaEdge(
  doc: CanvasDocument,
  draftNodeId: string,
): { readonly edge: CanvasEdgeDoc; readonly sourceNode: CanvasNodeDoc } | null {
  for (const edge of doc.edges) {
    if (edge.target !== draftNodeId) continue
    const sourceNode = doc.nodes.find(node => node.id === edge.source)
    if (sourceNode && isMediaEdgeKinds(sourceNode.kind, 'video-draft')) {
      return { edge, sourceNode }
    }
  }
  return null
}

export function findSourceImageNode(
  doc: CanvasDocument,
  draftNodeId: string,
): CanvasNodeDoc | null {
  const parent = findParentMediaEdge(doc, draftNodeId)
  return parent?.sourceNode.kind === 'image' ? parent.sourceNode : null
}

export function findDerivedDraftNodes(
  doc: CanvasDocument,
  sourceNodeId: string,
): CanvasNodeDoc[] {
  const sourceNode = doc.nodes.find(node => node.id === sourceNodeId)
  if (!sourceNode || sourceNode.kind === 'note') return []
  const targetIds = new Set(
    doc.edges.filter(edge => edge.source === sourceNodeId).map(edge => edge.target),
  )
  return doc.nodes.filter(node => node.kind === 'video-draft' && targetIds.has(node.id))
}

export function findUpstreamChain(doc: CanvasDocument, nodeId: string): CanvasNodeDoc[] {
  const chain: CanvasNodeDoc[] = []
  const visited = new Set<string>([nodeId])
  let cursor = nodeId
  while (true) {
    const parent = findParentMediaEdge(doc, cursor)
    if (!parent || visited.has(parent.sourceNode.id)) break
    visited.add(parent.sourceNode.id)
    chain.unshift(parent.sourceNode)
    cursor = parent.sourceNode.id
  }
  return chain
}

export interface DraftLineageInfo {
  readonly mode: CanvasDraftContinuationMode
  readonly continuation: boolean
  readonly parentNode: CanvasNodeDoc | null
  readonly parentEdge: CanvasEdgeDoc | null
  readonly originNode: CanvasNodeDoc | null
  readonly image?: CanvasImageLocator
  readonly videoId?: string
  readonly takeId?: string
}

export function resolveDraftLineage(
  doc: CanvasDocument,
  draftNodeId: string,
  visited: Set<string> = new Set(),
): DraftLineageInfo {
  if (visited.has(draftNodeId)) {
    return { mode: 'none', continuation: false, parentNode: null, parentEdge: null, originNode: null }
  }
  visited.add(draftNodeId)
  const parent = findParentMediaEdge(doc, draftNodeId)
  if (!parent) {
    return { mode: 'none', continuation: false, parentNode: null, parentEdge: null, originNode: null }
  }
  const { edge, sourceNode } = parent
  if (sourceNode.kind === 'image') {
    return {
      mode: 'direct-image',
      continuation: false,
      parentNode: sourceNode,
      parentEdge: edge,
      originNode: sourceNode,
      ...(sourceNode.image ? { image: sourceNode.image } : {}),
    }
  }
  if (sourceNode.kind === 'video') {
    return {
      mode: 'imported-video-tail',
      continuation: true,
      parentNode: sourceNode,
      parentEdge: edge,
      originNode: sourceNode,
      ...(sourceNode.video_id ? { videoId: sourceNode.video_id } : {}),
    }
  }
  if (edge.take_id) {
    return {
      mode: 'take-tail',
      continuation: true,
      parentNode: sourceNode,
      parentEdge: edge,
      originNode: sourceNode,
      videoId: edge.take_id,
      takeId: edge.take_id,
    }
  }
  const inherited = resolveDraftLineage(doc, sourceNode.id, visited)
  return {
    mode: 'inherit-input',
    continuation: true,
    parentNode: sourceNode,
    parentEdge: edge,
    originNode: inherited.originNode ?? sourceNode,
    ...(inherited.image ? { image: inherited.image } : {}),
    ...(inherited.videoId ? { videoId: inherited.videoId } : {}),
    ...(inherited.takeId ? { takeId: inherited.takeId } : {}),
  }
}

export function isValidCanvasConnection(
  doc: CanvasDocument,
  sourceId: string,
  targetId: string,
  attempts?: readonly Pick<CanvasAttempt, 'node_id'>[],
): boolean {
  if (!isCanvasUuid(sourceId) || !isCanvasUuid(targetId) || sourceId === targetId) return false
  if (doc.edges.length >= MAX_CANVAS_EDGES) return false
  const source = doc.nodes.find(n => n.id === sourceId)
  const target = doc.nodes.find(n => n.id === targetId)
  if (!source || !target) return false
  if (!isAllowedEdgeKinds(source.kind, target.kind)) return false
  if (doc.edges.some(edge => edge.source === sourceId && edge.target === targetId)) return false
  // A video-draft can have at most one incoming media edge, and bound drafts cannot swap sources.
  if (isMediaEdgeKinds(source.kind, target.kind)) {
    if (findParentMediaEdge(doc, targetId) !== null) return false
    if (attempts?.some(attempt => attempt.node_id === targetId)) return false
  }
  return !wouldCreateCycle(doc.edges, sourceId, targetId)
}

export function branchLetter(index: number): string {
  const safe = Math.max(0, Math.floor(index))
  if (safe < 26) return String.fromCharCode(65 + safe)
  return `#${safe + 1}`
}
