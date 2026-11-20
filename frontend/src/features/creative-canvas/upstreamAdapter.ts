/**
 * Thin adapter over vendored BeatDesign upstream helpers.
 * Reuses BeatDesign's resolveCanvasPlacement, resolveNonOverlappingPlacement,
 * buildGenerationTakes, and resolvePinnedGenerationOutputId without reimplementing them.
 */
import {
  resolveCanvasPlacement,
  resolveNonOverlappingPlacement,
  type CanvasOccupiedFrame,
} from './upstream/upload-layout'
import {
  buildGenerationTakes,
  resolvePinnedGenerationOutputId,
} from './upstream/generation-history'
import type { CanvasCardStatus, CanvasOutputCard } from './upstream/canvas-types'
import { DEFAULT_NODE_HEIGHT, DEFAULT_NODE_WIDTH } from './document'
import type {
  CanvasAttempt,
  CanvasNodeDoc,
  CanvasNodeKind,
  CanvasTake,
} from './types'

export interface PlacementOptions {
  readonly existingNodes: readonly CanvasNodeDoc[]
  readonly measuredSizes?: ReadonlyMap<string, { readonly width: number; readonly height: number }>
  readonly anchorNode?: CanvasNodeDoc | null
  readonly explicitDropPosition?: { readonly x: number; readonly y: number } | null
  readonly kind: CanvasNodeKind
}

function frameForNode(
  node: CanvasNodeDoc,
  measuredSizes?: ReadonlyMap<string, { readonly width: number; readonly height: number }>,
): CanvasOccupiedFrame {
  const measured = measuredSizes?.get(node.id)
  return {
    x: node.x,
    y: node.y,
    w: measured && measured.width > 0 ? measured.width : DEFAULT_NODE_WIDTH,
    h: measured && measured.height > 0 ? measured.height : DEFAULT_NODE_HEIGHT[node.kind],
  }
}

export function placeNewCanvasNode(options: PlacementOptions): { x: number; y: number } {
  const w = DEFAULT_NODE_WIDTH
  const h = DEFAULT_NODE_HEIGHT[options.kind]
  const occupied: CanvasOccupiedFrame[] = options.existingNodes.map(node =>
    frameForNode(node, options.measuredSizes),
  )

  if (options.explicitDropPosition != null) {
    const placed = resolveNonOverlappingPlacement({
      x: Math.round(options.explicitDropPosition.x),
      y: Math.round(options.explicitDropPosition.y),
      w,
      h,
      occupied,
    })
    return { x: Math.round(placed.x), y: Math.round(placed.y) }
  }

  const sourceFrames = options.anchorNode
    ? [frameForNode(options.anchorNode, options.measuredSizes)]
    : []

  const placed = resolveCanvasPlacement({
    sourceFrames,
    occupied,
    size: { w, h },
    origin: { x: 80, y: 80 },
  })

  return {
    x: Math.round(placed.x),
    y: Math.round(placed.y),
  }
}

function mapAttemptStatus(attempt: CanvasAttempt): CanvasCardStatus {
  if (attempt.unavailable) return 'failed'
  const status = attempt.job?.status
  if (!status) return 'pending'
  if (status === 'completed') return 'succeeded'
  if (status === 'running') return 'processing'
  if (status === 'failed' || status === 'expired') return 'failed'
  return 'pending'
}

/**
 * Resolves generation takes and pinned take strictly for a single video-draft node.
 * Attempts belonging to other branches are always excluded.
 * Never attaches arbitrary URLs: url is always null, resultText carries only the
 * authorized internal job ID for completed usable takes.
 */
export function resolveDraftTakes(
  draftNode: Pick<CanvasNodeDoc, 'id' | 'pinned_id'>,
  allAttempts: readonly CanvasAttempt[],
): { takes: CanvasTake[]; activeTake: CanvasTake | null; pinnedRequestId: string | null } {
  const scopedAttempts = allAttempts.filter(attempt => attempt.node_id === draftNode.id)
  if (!scopedAttempts.length) {
    return { takes: [], activeTake: null, pinnedRequestId: null }
  }

  const attemptById = new Map<string, CanvasAttempt>()
  const outputs: CanvasOutputCard[] = []

  for (const attempt of scopedAttempts) {
    attemptById.set(attempt.request_id, attempt)
    const status = mapAttemptStatus(attempt)
    const usableCompleted = status === 'succeeded' && !attempt.unavailable && Boolean(attempt.job?.id)
    outputs.push({
      kind: 'output',
      id: attempt.request_id,
      sourceConfigCardId: attempt.node_id,
      type: 'video',
      url: null,
      status,
      resultText: usableCompleted ? attempt.job!.id : null,
      generationSnapshot: { capturedAt: attempt.created_at || '' },
    })
  }

  const pinnedOutputId = draftNode.pinned_id ?? null
  const upstreamTakes = buildGenerationTakes({ outputs, pinnedOutputId })
  const resolvedPinnedId = resolvePinnedGenerationOutputId({ outputs, pinnedOutputId })

  const includedIds = new Set<string>()
  const takes: CanvasTake[] = upstreamTakes.map(take => {
    includedIds.add(take.id)
    const raw = attemptById.get(take.id)
    return {
      requestId: take.id,
      createdAt: raw?.created_at ?? '',
      unavailable: Boolean(raw?.unavailable),
      job: raw?.job,
      isPinned: take.isPinned,
      isActive: take.isPinned,
      takeNumber: take.takeNumber,
    }
  })

  // Also surface failed/expired/unavailable attempts chronologically after active takes
  // so users never see an indefinitely pending state when a job failed or lost authorization.
  const failedOrUnavailable = scopedAttempts
    .filter(attempt => !includedIds.has(attempt.request_id))
    .sort(
      (a, b) =>
        (a.created_at || '').localeCompare(b.created_at || '') ||
        a.request_id.localeCompare(b.request_id),
    )

  for (const raw of failedOrUnavailable) {
    takes.push({
      requestId: raw.request_id,
      createdAt: raw.created_at || '',
      unavailable: Boolean(raw.unavailable),
      job: raw.job,
      isPinned: false,
      isActive: false,
      takeNumber: takes.length + 1,
    })
  }

  let activeTake = takes.find(take => take.isPinned) ?? null
  if (!activeTake && takes.length > 0) {
    // Prefer newest non-failed, otherwise newest failed/unavailable attempt so honest status is shown.
    activeTake = takes[takes.length - 1]
  }

  return {
    takes: takes.map(take => ({
      ...take,
      isActive: activeTake?.requestId === take.requestId,
    })),
    activeTake,
    pinnedRequestId: resolvedPinnedId,
  }
}
