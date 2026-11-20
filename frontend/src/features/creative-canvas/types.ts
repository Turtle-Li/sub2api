import type { VideoJob } from '@/features/video-studio/api'
import type { StudioOriginal } from '@/features/image-studio/workspaceAPI'

export interface CanvasViewport {
  readonly x: number
  readonly y: number
  readonly zoom: number
}

export interface CanvasImageLocator {
  readonly route: number
  readonly batchId: string
  readonly itemId: string
  readonly index: number
}

export type CanvasNodeKind = 'image' | 'video' | 'video-draft' | 'note'

export interface CanvasNodeDoc {
  readonly id: string
  readonly kind: CanvasNodeKind
  readonly x: number
  readonly y: number
  readonly title?: string
  readonly prompt?: string
  readonly image?: CanvasImageLocator
  readonly video_id?: string
  readonly pinned_id?: string
}

export interface CanvasEdgeDoc {
  readonly source: string
  readonly target: string
}

export interface CanvasDocument {
  readonly viewport: CanvasViewport
  readonly nodes: readonly CanvasNodeDoc[]
  readonly edges: readonly CanvasEdgeDoc[]
}

export interface CanvasSummary {
  readonly id: string
  readonly title: string
  readonly revision: number
  readonly updated_at: string
}

export interface CanvasAssetStatus {
  readonly available: boolean
  readonly job?: VideoJob
}

export interface CanvasAttempt {
  readonly node_id: string
  readonly request_id: string
  readonly created_at: string
  readonly unavailable?: boolean
  readonly job?: VideoJob
}

export interface CreativeCanvas {
  readonly id: string
  readonly title: string
  readonly revision: number
  readonly updated_at: string
  readonly document: CanvasDocument
  readonly assets: Readonly<Record<string, CanvasAssetStatus>>
  readonly attempts: readonly CanvasAttempt[]
}

export interface CanvasTake {
  readonly requestId: string
  readonly createdAt: string
  readonly unavailable: boolean
  readonly job?: VideoJob
  readonly isPinned: boolean
  readonly isActive: boolean
  readonly takeNumber: number
}

export type AddToCanvasPayload =
  | {
      readonly kind: 'image'
      readonly original: StudioOriginal
      readonly title?: string
    }
  | {
      readonly kind: 'video'
      readonly videoId: string
      readonly title?: string
    }

export interface CanvasDraftHandoff {
  readonly userId: number
  readonly canvasId: string
  readonly nodeId: string
  readonly revision: number
  readonly canvasTitle: string
  readonly draftTitle: string
  readonly prompt: string
  readonly expectsSourceImage: boolean
}
