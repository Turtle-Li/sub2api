import type { ComputedRef, Ref } from 'vue'
import type { Router } from 'vue-router'
import { getApiErrorMessage } from './api'
import { isCanvasUuid, resolveDraftLineage, sanitizeImageLocator, type DraftLineageInfo } from './document'
import { stageCanvasDraftHandoff } from './handoff'
import type { CanvasDocument, CanvasNodeDoc, CreativeCanvas } from './types'

interface CanvasHandoffDeps {
  readonly board: Ref<CreativeCanvas | null>
  readonly doc: Ref<CanvasDocument>
  readonly dirty: ComputedRef<boolean>
  readonly saving?: Ref<boolean>
  readonly saveUnconfirmed?: Ref<boolean>
  readonly conflict: Ref<boolean>
  readonly error: Ref<string>
  readonly currentUserId: () => number | null
  readonly save: () => Promise<boolean>
  readonly router: Router
}

export function useCanvasHandoff(deps: CanvasHandoffDeps) {
  function validateDraft(
    currentBoard: CreativeCanvas,
    currentDoc: CanvasDocument,
    draftId: string,
    requireServerInputs: boolean,
  ): {
    readonly draft: CanvasNodeDoc
    readonly lineage: DraftLineageInfo
    readonly expectsSourceImage: boolean
    readonly expectsTailFrame: boolean
  } | null {
    const draft = currentDoc.nodes.find(n => n.id === draftId && n.kind === 'video-draft')
    if (!draft) {
      deps.error.value = '找不到对应的视频草稿分支。'
      return null
    }
    const lineage = resolveDraftLineage(currentDoc, draftId)
    if (lineage.mode === 'direct-image' && lineage.parentNode) {
      if (!sanitizeImageLocator(lineage.parentNode.image)) {
        deps.error.value = '关联的原图定位无效，无法进入视频工作台。'
        return null
      }
      if (currentBoard.assets[lineage.parentNode.id]?.available !== true) {
        deps.error.value = '关联的原图资源当前不可用或无权访问，无法进入视频工作台。'
        return null
      }
      return { draft, lineage, expectsSourceImage: true, expectsTailFrame: false }
    }

    if (lineage.continuation) {
      if (requireServerInputs) {
        const resolved = currentBoard.inputs?.[draftId]
        if (!resolved) {
          deps.error.value = '服务端未返回该续镜分支的来源核验信息，无法进入视频工作台。'
          return null
        }
        if (resolved.available !== true) {
          deps.error.value = resolved.video_id
            ? '该续镜固定的上游视频作品已过期、撤权或不可用，无法进入视频工作台。'
            : '该续镜沿用的来源原图已过期、撤权或不可用，无法进入视频工作台。'
          return null
        }
        return {
          draft,
          lineage,
          expectsSourceImage: Boolean(resolved.image),
          expectsTailFrame: Boolean(resolved.video_id),
        }
      }

      if (lineage.image) {
        if (!sanitizeImageLocator(lineage.image)) {
          deps.error.value = '沿用的原图定位无效，无法进入视频工作台。'
          return null
        }
        if (lineage.originNode && currentBoard.assets[lineage.originNode.id]?.available !== true) {
          deps.error.value = '沿用的原图资源当前不可用或无权访问，无法进入视频工作台。'
          return null
        }
        return { draft, lineage, expectsSourceImage: true, expectsTailFrame: false }
      }
      if (lineage.videoId) {
        if (!isCanvasUuid(lineage.videoId)) {
          deps.error.value = '续镜关联的视频标识无效，无法进入视频工作台。'
          return null
        }
        if (lineage.originNode?.kind === 'video') {
          const asset = currentBoard.assets[lineage.originNode.id]
          if (asset?.available !== true || asset.job?.status !== 'completed') {
            deps.error.value = '关联的导入视频不可用或尚未完成，无法提取尾帧续镜。'
            return null
          }
        } else {
          const attempt = currentBoard.attempts.find(item => item.request_id === lineage.videoId)
          if (!attempt || attempt.unavailable || attempt.job?.status !== 'completed') {
            deps.error.value = '固定的成片版本已失效或尚未完成，无法提取尾帧续镜。'
            return null
          }
        }
        return { draft, lineage, expectsSourceImage: false, expectsTailFrame: true }
      }
      return { draft, lineage, expectsSourceImage: false, expectsTailFrame: false }
    }

    return { draft, lineage, expectsSourceImage: false, expectsTailFrame: false }
  }

  async function toStudio(draftId: string): Promise<void> {
    const userId = deps.currentUserId()
    if (userId === null) {
      deps.error.value = '请先登录创作画布。'
      return
    }
    if (!deps.board.value) {
      deps.error.value = '请先打开一个画布。'
      return
    }
    if (deps.conflict.value) {
      deps.error.value = '画布存在版本冲突，请先解决冲突再进入视频工作台。'
      return
    }
    const hasPendingSync = () =>
      Boolean(deps.dirty.value || deps.saving?.value || deps.saveUnconfirmed?.value)
    if (!validateDraft(deps.board.value, deps.doc.value, draftId, !hasPendingSync())) return

    while (hasPendingSync()) {
      const ok = await deps.save()
      if (!ok) return
      if (deps.conflict.value) return
    }

    if (deps.currentUserId() !== userId) {
      deps.error.value = '登录账号已变更，无法交接画布草稿。'
      return
    }
    const savedBoard = deps.board.value
    if (!savedBoard || deps.conflict.value) return
    if (deps.dirty.value || deps.saveUnconfirmed?.value) {
      deps.error.value = deps.saveUnconfirmed?.value
        ? '画布保存结果尚未确认，请先重试核验后再进入视频工作台。'
        : '保存期间画布有新修改，请再次保存后再进入视频工作台。'
      return
    }
    const verified = validateDraft(savedBoard, deps.doc.value, draftId, true)
    if (!verified) return

    try {
      stageCanvasDraftHandoff(userId, {
        canvasId: savedBoard.id,
        nodeId: verified.draft.id,
        revision: savedBoard.revision,
        canvasTitle: savedBoard.title,
        draftTitle: verified.draft.title || '',
        prompt: verified.draft.prompt || '',
        expectsSourceImage: verified.expectsSourceImage,
        expectsTailFrame: verified.expectsTailFrame,
        continuationMode: verified.lineage.mode,
      })
      await deps.router.push({
        path: '/video-studio',
        query: { canvas: savedBoard.id, node: verified.draft.id },
      })
    } catch (err) {
      deps.error.value = getApiErrorMessage(err, '进入视频工作台失败，请稍后重试。')
    }
  }

  return { toStudio }
}
