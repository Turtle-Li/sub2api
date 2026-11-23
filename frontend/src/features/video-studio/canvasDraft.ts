import { getStoredUserID } from '@/api/tokenRefresh'
import {
  creativeCanvasApi,
  getApiErrorMessage,
  getApiErrorStatus,
  isCanvasConflictError,
} from '@/features/creative-canvas/api'
import {
  findSourceImageNode,
  isCanvasUuid,
  resolveDraftLineage,
  sanitizeImageLocator,
} from '@/features/creative-canvas/document'
import {
  clearCanvasDraftHandoff,
  readStagedCanvasDraftHandoff,
  readStoredCanvasBindingMeta,
  stageCanvasDraftHandoff,
} from '@/features/creative-canvas/handoff'
import type { CanvasAttempt, CanvasDraftContinuationMode } from '@/features/creative-canvas/types'
import { resolveDraftTakes } from '@/features/creative-canvas/upstreamAdapter'
import { useAuthStore } from '@/stores/auth'
import { clearImage, prepareImage, prepareVideoTailFrame, type VideoSource } from './source'

export interface CanvasRouteRef {
  readonly present: boolean
  readonly valid: boolean
  readonly canvasId: string
  readonly nodeId: string
}

export interface ReadyCanvasDraft {
  readonly status: 'ready'
  readonly canvasId: string
  readonly nodeId: string
  readonly revision: number
  readonly canvasTitle: string
  readonly draftTitle: string
  readonly prompt: string
  readonly hasSourceImage: boolean
  readonly hasTailFrameSource: boolean
  readonly continuationMode: CanvasDraftContinuationMode
  readonly sourceImageTitle: string
  readonly sourceSummary: string
  readonly unavailableAttempt: boolean
  readonly attempts: readonly CanvasAttempt[]
}

export type CanvasDraftState =
  | { readonly status: 'none' }
  | { readonly status: 'verifying'; readonly canvasId: string; readonly nodeId: string }
  | ReadyCanvasDraft
  | {
      readonly status: 'invalid' | 'source-unavailable' | 'conflict' | 'error'
      readonly canvasId: string
      readonly nodeId: string
      readonly message: string
      readonly retryable: boolean
    }

const CANVAS_QUERY_KEYS = ['canvas', 'canvasId', 'board'] as const
const NODE_QUERY_KEYS = ['node', 'nodeId', 'draft'] as const

function firstQueryString(value: unknown): string {
  if (typeof value === 'string') return value.trim()
  if (Array.isArray(value)) {
    const first = value.find((item): item is string => typeof item === 'string')
    return first ? first.trim() : ''
  }
  return ''
}

function hasAnyKey(query: Record<string, unknown>, keys: readonly string[]): boolean {
  return keys.some(key => Object.prototype.hasOwnProperty.call(query, key) && query[key] !== undefined)
}

/**
 * Parses canvas draft route query keys (`canvas`/`node`, plus compatible aliases
 * `canvasId`/`nodeId`/`board`/`draft`). Deliberately ignores any prompt, locator,
 * coordinate, title, or media parameters in the URL.
 */
export function parseCanvasDraftQuery(query: Record<string, unknown> | null | undefined): CanvasRouteRef {
  if (!query || typeof query !== 'object') {
    return { present: false, valid: false, canvasId: '', nodeId: '' }
  }
  const present = hasAnyKey(query, CANVAS_QUERY_KEYS) || hasAnyKey(query, NODE_QUERY_KEYS)
  if (!present) {
    return { present: false, valid: false, canvasId: '', nodeId: '' }
  }

  let canvasId = ''
  for (const key of CANVAS_QUERY_KEYS) {
    const candidate = firstQueryString(query[key])
    if (candidate) {
      canvasId = candidate
      break
    }
  }

  let nodeId = ''
  for (const key of NODE_QUERY_KEYS) {
    const candidate = firstQueryString(query[key])
    if (candidate) {
      nodeId = candidate
      break
    }
  }

  return {
    present: true,
    valid: isCanvasUuid(canvasId) && isCanvasUuid(nodeId),
    canvasId,
    nodeId,
  }
}

export function stripCanvasDraftQuery(query: Record<string, unknown> | null | undefined): Record<string, unknown> {
  if (!query || typeof query !== 'object') return {}
  const next: Record<string, unknown> = {}
  const blocked = new Set<string>([...CANVAS_QUERY_KEYS, ...NODE_QUERY_KEYS])
  for (const [key, value] of Object.entries(query)) {
    if (!blocked.has(key)) next[key] = value
  }
  return next
}

export function isSignedInOwner(userId: number): boolean {
  const auth = useAuthStore()
  return (
    auth.isAuthenticated &&
    auth.user?.id === userId &&
    getStoredUserID() === userId &&
    !!localStorage.getItem('auth_token')
  )
}

export interface VerifyCanvasDraftOptions {
  readonly userId: number
  readonly canvasId: string
  readonly nodeId: string
  readonly canvasEnabled: boolean
  readonly signal: AbortSignal
  readonly heldId?: string
}

export interface VerifyCanvasDraftResult {
  readonly state: CanvasDraftState
  readonly source: VideoSource | null
}

/**
 * Re-verifies a canvas-origin video draft against GET /creative-canvases/:id.
 * Checks signed-in owner, board revision, valid video-draft node, and source image availability.
 * Restores the authorized original image via prepareImage(userId, validatedLocator).
 * Always fails closed on slow/error/expired/conflict/unauthorized states.
 */
export async function verifyCanvasDraft(options: VerifyCanvasDraftOptions): Promise<VerifyCanvasDraftResult> {
  const { userId, canvasId, nodeId, canvasEnabled, signal, heldId } = options

  if (!canvasEnabled) {
    clearImage()
    return {
      state: {
        status: 'invalid',
        canvasId,
        nodeId,
        message: '创作画布功能当前未启用，无法验证或提交画布草稿。',
        retryable: false,
      },
      source: null,
    }
  }

  if (!isSignedInOwner(userId)) {
    clearCanvasDraftHandoff(userId)
    clearImage()
    return {
      state: {
        status: 'error',
        canvasId,
        nodeId,
        message: '登录账号已变更或未登录，无法验证画布草稿。',
        retryable: false,
      },
      source: null,
    }
  }

  if (!isCanvasUuid(canvasId) || !isCanvasUuid(nodeId)) {
    clearImage()
    return {
      state: {
        status: 'invalid',
        canvasId,
        nodeId,
        message: '画布草稿链接参数无效或不完整，未载入画布草稿。',
        retryable: false,
      },
      source: null,
    }
  }

  const staged = readStagedCanvasDraftHandoff(userId, canvasId, nodeId)
  const storedMeta = readStoredCanvasBindingMeta(userId, canvasId, nodeId)
  const expectedRevision = staged?.revision ?? storedMeta?.revision

  try {
    const canvas = await creativeCanvasApi.get(canvasId, signal)
    signal.throwIfAborted()

    if (!isSignedInOwner(userId)) {
      clearCanvasDraftHandoff(userId)
      clearImage()
      return {
        state: {
          status: 'error',
          canvasId,
          nodeId,
          message: '登录账号已变更，已中止画布草稿验证。',
          retryable: false,
        },
        source: null,
      }
    }

    if (canvas.id !== canvasId || !Number.isInteger(canvas.revision) || canvas.revision < 0) {
      clearImage()
      return {
        state: {
          status: 'invalid',
          canvasId,
          nodeId,
          message: '返回的画布数据或修订号无效，已禁止提交。',
          retryable: false,
        },
        source: null,
      }
    }

    if (expectedRevision !== undefined && canvas.revision !== expectedRevision) {
      clearImage()
      return {
        state: {
          status: 'conflict',
          canvasId,
          nodeId,
          message: `画布内容已更新（原修订号 v${expectedRevision}，当前 v${canvas.revision}）。为避免错绑，已停止当前草稿提交，请返回画布重新确认后再进入。`,
          retryable: false,
        },
        source: null,
      }
    }

    const draftNode = canvas.document.nodes.find(node => node.id === nodeId)
    if (!draftNode || draftNode.kind !== 'video-draft') {
      clearImage()
      return {
        state: {
          status: 'invalid',
          canvasId,
          nodeId,
          message: '画布中未找到对应的视频草稿节点，可能已被删除或类型已变更。',
          retryable: false,
        },
        source: null,
      }
    }

    const lineage = resolveDraftLineage(canvas.document, nodeId)
    const sourceNode = findSourceImageNode(canvas.document, nodeId)
    let restoredSource: VideoSource | null = null
    let hasSourceImage = false
    let hasTailFrameSource = false
    let sourceSummary = '纯文字分镜草稿'

    if (lineage.continuation) {
      const resolvedInput = canvas.inputs?.[nodeId]
      if (!resolvedInput) {
        clearImage()
        return {
          state: {
            status: 'source-unavailable',
            canvasId,
            nodeId,
            message: '服务端未返回该续镜节点的来源核验记录（inputs），已停止提交，请返回画布刷新后重试。',
            retryable: true,
          },
          source: null,
        }
      }
      if (resolvedInput.available !== true) {
        clearImage()
        return {
          state: {
            status: 'source-unavailable',
            canvasId,
            nodeId,
            message: resolvedInput.video_id
              ? '该续镜固定的上游视频成片已过期、不可用或原授权已失效，无法提取尾帧提交。'
              : '该续镜沿用的来源原图已过期、不可用或原授权已失效，无法以此草稿生成视频。',
            retryable: true,
          },
          source: null,
        }
      }

      if (resolvedInput.image) {
        const validatedLocator = sanitizeImageLocator(resolvedInput.image)
        if (!validatedLocator) {
          clearImage()
          return {
            state: {
              status: 'source-unavailable',
              canvasId,
              nodeId,
              message: '该续镜沿用的来源原图定位无效，已禁止提交。',
              retryable: false,
            },
            source: null,
          }
        }
        try {
          restoredSource = prepareImage(userId, validatedLocator)
          hasSourceImage = true
          sourceSummary = `沿用素材 · ${lineage.originNode?.title || `第 ${validatedLocator.index + 1} 张原图`}`
        } catch {
          clearImage()
          return {
            state: {
              status: 'source-unavailable',
              canvasId,
              nodeId,
              message: '无法恢复续镜沿用的私有原图授权，请返回画布检查状态。',
              retryable: true,
            },
            source: null,
          }
        }
      } else if (resolvedInput.video_id) {
        if (!isCanvasUuid(resolvedInput.video_id)) {
          clearImage()
          return {
            state: {
              status: 'source-unavailable',
              canvasId,
              nodeId,
              message: '该续镜固定的视频任务标识无效，已禁止提交。',
              retryable: false,
            },
            source: null,
          }
        }
        let takeSuffix = ''
        if (lineage.mode === 'take-tail' && lineage.parentNode) {
          const { takes } = resolveDraftTakes(lineage.parentNode, canvas.attempts)
          const matchedTake = takes.find(item => item.requestId === resolvedInput.video_id)
          takeSuffix = matchedTake ? `（v${matchedTake.takeNumber}）` : ''
        }
        const parentName = lineage.parentNode?.title || (lineage.mode === 'imported-video-tail' ? '视频作品' : '上一镜')
        const tailLabel =
          lineage.mode === 'imported-video-tail'
            ? `导入视频尾帧 · ${parentName}`
            : `成片尾帧续镜 · ${parentName}${takeSuffix}`
        try {
          restoredSource = prepareVideoTailFrame(userId, {
            videoId: resolvedInput.video_id,
            label: tailLabel,
          })
          hasTailFrameSource = true
          sourceSummary =
            lineage.mode === 'imported-video-tail'
              ? `用导入视频尾帧续镜 · ${parentName}`
              : `用这版成片尾帧续镜 · ${parentName}${takeSuffix}`
        } catch {
          clearImage()
          return {
            state: {
              status: 'source-unavailable',
              canvasId,
              nodeId,
              message: '无法建立续镜视频尾帧的本机提取授权，请返回画布确认后再试。',
              retryable: true,
            },
            source: null,
          }
        }
      } else if (staged?.expectsSourceImage || staged?.expectsTailFrame) {
        clearImage()
        return {
          state: {
            status: 'source-unavailable',
            canvasId,
            nodeId,
            message: '原续镜草稿预期的素材来源已变化，请返回画布重新确认。',
            retryable: false,
          },
          source: null,
        }
      } else {
        clearImage()
        sourceSummary = `沿用素材 · ${lineage.parentNode?.title || '纯文字上一镜'}`
      }
    } else if (sourceNode) {
      const assetStatus = canvas.assets[sourceNode.id]
      if (!assetStatus || assetStatus.available !== true) {
        clearImage()
        return {
          state: {
            status: 'source-unavailable',
            canvasId,
            nodeId,
            message: '该草稿关联的生图原图已过期、不可用或私有引用授权已失效，无法以此草稿生成视频。',
            retryable: true,
          },
          source: null,
        }
      }

      const validatedLocator = sanitizeImageLocator(sourceNode.image)
      if (!validatedLocator) {
        clearImage()
        return {
          state: {
            status: 'source-unavailable',
            canvasId,
            nodeId,
            message: '该草稿关联的来源原图定位无效，已禁止提交。',
            retryable: false,
          },
          source: null,
        }
      }

      try {
        restoredSource = prepareImage(userId, validatedLocator)
        hasSourceImage = true
        sourceSummary = `来源原图 · ${sourceNode.title || `第 ${validatedLocator.index + 1} 张`}`
      } catch {
        clearImage()
        return {
          state: {
            status: 'source-unavailable',
            canvasId,
            nodeId,
            message: '无法恢复该草稿的私有原图授权，请重新登录或返回画布检查原图状态。',
            retryable: true,
          },
          source: null,
        }
      }
    } else if (staged?.expectsSourceImage || staged?.expectsTailFrame) {
      clearImage()
      return {
        state: {
          status: 'source-unavailable',
          canvasId,
          nodeId,
          message: '原草稿关联的来源素材已被移除，请返回画布确认后再进入。',
          retryable: false,
        },
        source: null,
      }
    } else {
      clearImage()
    }

    const nodeAttempts = canvas.attempts.filter(attempt => attempt.node_id === nodeId)
    const matchedHeld = heldId ? nodeAttempts.find(attempt => attempt.request_id === heldId) : undefined
    const unavailableAttempt = Boolean(
      matchedHeld?.unavailable || (!heldId && nodeAttempts.some(attempt => attempt.unavailable)),
    )

    const prompt = draftNode.prompt || ''
    const draftTitle = draftNode.title || '视频草稿'

    try {
      stageCanvasDraftHandoff(userId, {
        canvasId: canvas.id,
        nodeId: draftNode.id,
        revision: canvas.revision,
        canvasTitle: canvas.title,
        draftTitle,
        prompt,
        expectsSourceImage: hasSourceImage,
        expectsTailFrame: hasTailFrameSource,
        continuationMode: lineage.mode,
        sourceSummary,
      })
    } catch {
      clearImage()
      return {
        state: {
          status: 'error',
          canvasId,
          nodeId,
          message: '登录账号已变更，无法保存画布草稿修订号。',
          retryable: false,
        },
        source: null,
      }
    }

    return {
      state: {
        status: 'ready',
        canvasId: canvas.id,
        nodeId: draftNode.id,
        revision: canvas.revision,
        canvasTitle: canvas.title,
        draftTitle,
        prompt,
        hasSourceImage,
        hasTailFrameSource,
        continuationMode: lineage.mode,
        sourceImageTitle: sourceNode?.title || '',
        sourceSummary,
        unavailableAttempt,
        attempts: nodeAttempts,
      },
      source: restoredSource,
    }
  } catch (error) {
    if (signal.aborted) throw error
    clearImage()
    const status = getApiErrorStatus(error)
    if (status === 401) {
      return {
        state: {
          status: 'error',
          canvasId,
          nodeId,
          message: '登录已失效，无法验证画布草稿。请重新登录后重试。',
          retryable: false,
        },
        source: null,
      }
    }
    if (status === 403) {
      return {
        state: {
          status: 'source-unavailable',
          canvasId,
          nodeId,
          message: '无权访问该画布或私有引用已被拒绝，已禁止提交。',
          retryable: false,
        },
        source: null,
      }
    }
    if (status === 404) {
      return {
        state: {
          status: 'invalid',
          canvasId,
          nodeId,
          message: '未找到对应的创作画布，可能已被删除或链接无效。',
          retryable: false,
        },
        source: null,
      }
    }
    if (isCanvasConflictError(error)) {
      return {
        state: {
          status: 'conflict',
          canvasId,
          nodeId,
          message: '画布修订号冲突，请返回画布确认最新版本。',
          retryable: false,
        },
        source: null,
      }
    }
    return {
      state: {
        status: 'error',
        canvasId,
        nodeId,
        message: getApiErrorMessage(error, '无法读取画布草稿，请稍后重试验证；未允许以空白草稿提交。'),
        retryable: true,
      },
      source: null,
    }
  }
}

export function formatCanvasBindError(error: unknown): { readonly conflict: boolean; readonly message: string } {
  const status = getApiErrorStatus(error)
  if (isCanvasConflictError(error)) {
    return {
      conflict: true,
      message: '画布已在其他标签页或窗口被修改（修订号冲突），本次未提交付费视频任务。请返回画布确认最新版本后再试。',
    }
  }
  if (status === 401) {
    return {
      conflict: false,
      message: '登录已失效，无法绑定画布草稿，本次未提交视频任务。请重新登录。',
    }
  }
  if (status === 403) {
    return {
      conflict: false,
      message: '无权绑定该画布草稿或私有引用被拒绝，本次未提交视频任务。',
    }
  }
  if (status === 404) {
    return {
      conflict: false,
      message: '目标画布或草稿节点已不存在，本次未提交视频任务。',
    }
  }
  return {
    conflict: false,
    message: getApiErrorMessage(
      error,
      '绑定画布草稿失败或状态未知，为避免错绑与重复计费，本次未提交视频任务，也不会自动重试。',
    ),
  }
}
