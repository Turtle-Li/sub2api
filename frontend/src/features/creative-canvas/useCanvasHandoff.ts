import type { ComputedRef, Ref } from 'vue'
import type { Router } from 'vue-router'
import { getApiErrorMessage } from './api'
import { findSourceImageNode, sanitizeImageLocator } from './document'
import { stageCanvasDraftHandoff } from './handoff'
import type { CanvasDocument, CanvasNodeDoc, CreativeCanvas } from './types'

interface CanvasHandoffDeps {
  readonly board: Ref<CreativeCanvas | null>
  readonly doc: Ref<CanvasDocument>
  readonly dirty: ComputedRef<boolean>
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
  ): { draft: CanvasNodeDoc; sourceImage: CanvasNodeDoc | null } | null {
    const draft = currentDoc.nodes.find(n => n.id === draftId && n.kind === 'video-draft')
    if (!draft) {
      deps.error.value = '找不到对应的视频草稿分支。'
      return null
    }
    const sourceImage = findSourceImageNode(currentDoc, draftId)
    if (sourceImage) {
      if (!sanitizeImageLocator(sourceImage.image)) {
        deps.error.value = '关联的原图定位无效，无法进入视频工作台。'
        return null
      }
      if (currentBoard.assets[sourceImage.id]?.available !== true) {
        deps.error.value = '关联的原图资源当前不可用或无权访问，无法进入视频工作台。'
        return null
      }
    }
    return { draft, sourceImage }
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
    if (!validateDraft(deps.board.value, deps.doc.value, draftId)) return

    if (deps.dirty.value) {
      const ok = await deps.save()
      if (!ok) return
    }

    if (deps.currentUserId() !== userId) {
      deps.error.value = '登录账号已变更，无法交接画布草稿。'
      return
    }
    const savedBoard = deps.board.value
    if (!savedBoard || deps.conflict.value) return
    if (deps.dirty.value) {
      deps.error.value = '保存期间画布有新修改，请再次保存后再进入视频工作台。'
      return
    }
    const verified = validateDraft(savedBoard, deps.doc.value, draftId)
    if (!verified) return

    try {
      stageCanvasDraftHandoff(userId, {
        canvasId: savedBoard.id,
        nodeId: verified.draft.id,
        revision: savedBoard.revision,
        canvasTitle: savedBoard.title,
        draftTitle: verified.draft.title || '',
        prompt: verified.draft.prompt || '',
        expectsSourceImage: Boolean(verified.sourceImage),
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
