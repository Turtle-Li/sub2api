// Host adapter: only BeatDesign history fields cross the canvas boundary; no provider or editor types.
export type CanvasCardMediaType = 'image' | 'video'
export type CanvasCardStatus = 'idle' | 'pending' | 'processing' | 'succeeded' | 'failed'
interface CardBase { id: string; type: CanvasCardMediaType; url: string | null; status: CanvasCardStatus; resultText?: string | null; pinnedOutputId?: string | null }
export interface CanvasOutputCard extends CardBase { kind: 'output'; sourceConfigCardId: string; generationSnapshot: { capturedAt: string } }
export type CanvasCard = CanvasOutputCard | (CardBase & { kind: 'generation' | 'asset' })
export const isCanvasOutputCard = (card: CanvasCard | null | undefined): card is CanvasOutputCard => card?.kind === 'output'
