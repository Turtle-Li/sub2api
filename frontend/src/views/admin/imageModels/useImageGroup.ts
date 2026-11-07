import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import * as groupsAPI from '@/api/admin/groups'
import { getImageModels, type ImageModelCatalogue } from '@/api/admin/imageModels'
import type { AdminGroup } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'
import { createImageDraft, imageDraftPatch, type ImageGroupDraft } from './imageGroupDraft'

export function useImageGroup({ autoSelect = true }: { autoSelect?: boolean } = {}) {
  const groups = ref<AdminGroup[]>([])
  const group = ref<AdminGroup | null>(null)
  const draft = ref<ImageGroupDraft | null>(null)
  const catalogue = ref<ImageModelCatalogue | null>(null)
  const selectedId = ref<number | null>(null)
  const loading = ref(false)
  const saving = ref(false)
  const error = ref('')
  const saved = ref(false)
  let version = 0
  let catalogueVersion = 0
  const dirty = computed(() => group.value !== null && draft.value !== null &&
    Object.keys(imageDraftPatch(group.value, draft.value)).length > 0)
  const imageGroups = computed(() => groups.value.filter(item => item.platform === 'openai' || item.platform === 'gemini'))

  async function select(id: number) {
    const current = ++version
    const currentCatalogue = ++catalogueVersion
    selectedId.value = id
    loading.value = true
    error.value = ''
    saved.value = false
    group.value = null
    draft.value = null
    catalogue.value = null
    try {
      const [detail, models] = await Promise.all([groupsAPI.getById(id), getImageModels(id)])
      if (current !== version) return
      group.value = detail
      draft.value = createImageDraft(detail)
      if (currentCatalogue === catalogueVersion) catalogue.value = models
    } catch (cause) {
      if (current === version) error.value = extractApiErrorMessage(cause)
    } finally {
      if (current === version) loading.value = false
    }
  }

  async function loadGroups() {
    loading.value = true
    error.value = ''
    try {
      groups.value = await groupsAPI.getAllIncludingInactive()
      const first = imageGroups.value[0]
      if (autoSelect && first) return await select(first.id)
      loading.value = false
    } catch (cause) {
      error.value = extractApiErrorMessage(cause)
      loading.value = false
    }
  }

  async function refreshCatalogue() {
    const id = selectedId.value
    const current = version
    if (id === null) return
    const currentCatalogue = ++catalogueVersion
    if (catalogue.value) catalogue.value = { ...catalogue.value, models: [], unavailable_reason: 'checking' }
    try {
      const models = await getImageModels(id)
      if (current === version && currentCatalogue === catalogueVersion) catalogue.value = models
    } catch (cause) {
      if (current === version && currentCatalogue === catalogueVersion) {
        error.value = extractApiErrorMessage(cause)
        if (catalogue.value) catalogue.value = { ...catalogue.value, models: [], unavailable_reason: 'unknown' }
      }
    }
  }

  async function save() {
    if (group.value === null || draft.value === null || saving.value || !dirty.value) return
    const id = group.value.id
    const current = version
    const patch = imageDraftPatch(group.value, draft.value)
    saving.value = true
    saved.value = false
    error.value = ''
    try {
      const updated = await groupsAPI.update(id, patch)
      if (current !== version) return
      group.value = updated
      draft.value = createImageDraft(updated)
      groups.value = groups.value.map(item => item.id === id ? updated : item)
      saved.value = true
      await refreshCatalogue()
    } catch (cause) {
      if (current === version) error.value = extractApiErrorMessage(cause)
    } finally {
      saving.value = false
    }
  }

  function beforeUnload(event: BeforeUnloadEvent) {
    if (dirty.value || saving.value) { event.preventDefault(); event.returnValue = '' }
  }
  onMounted(() => { void loadGroups(); window.addEventListener('beforeunload', beforeUnload) })
  onBeforeUnmount(() => { version++; catalogueVersion++; window.removeEventListener('beforeunload', beforeUnload) })
  return { groups, imageGroups, group, draft, catalogue, selectedId, loading, saving, error, saved, dirty,
    select, save, loadGroups, refreshCatalogue }
}
