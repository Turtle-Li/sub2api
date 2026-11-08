import type { BatchImageModel } from '@/api/batchImage'
import type { StudioKey } from './api'

// Display metadata only. Fresh key admission and submission pricing stay server-owned.
const prefix = 'image-studio:directory:v1:'
const maxAge = 10 * 60 * 1000
const maxBytes = 128 * 1024
const maxCatalogues = 32
const oversized = (raw: string) => raw.length > maxBytes || new TextEncoder().encode(raw).byteLength > maxBytes
const record = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value)
const strings = (value: unknown): value is string[] => Array.isArray(value) && value.length <= 64 && value.every(item => typeof item === 'string' && item.length <= 256)

function validModel(value: unknown): value is BatchImageModel {
  if (!record(value) || typeof value.id !== 'string' || !value.id || value.id.length > 256 || typeof value.provider !== 'string' || !value.provider || value.provider.length > 64 || typeof value.object !== 'string') return false
  const caps = value.capabilities
  if (caps !== undefined && (!record(caps) || !strings(caps.image_sizes) || !strings(caps.aspect_ratios) || !strings(caps.response_mime_types) || typeof caps.default_image_size !== 'string' || typeof caps.default_aspect_ratio !== 'string' || typeof caps.max_reference_images !== 'number' || !Number.isFinite(caps.max_reference_images))) return false
  return value.prices === undefined || (record(value.prices) && Object.values(value.prices).every(price => record(price) && typeof price.min === 'number' && typeof price.max === 'number' && Number.isFinite(price.min) && Number.isFinite(price.max) && price.min >= 0 && price.max >= price.min))
}
function validKeys(value: unknown): value is StudioKey[] {
  return Array.isArray(value) && value.length <= 256 && value.every(key => record(key) && Number.isSafeInteger(key.id) && Number(key.id) > 0 && (key.group_id == null || Number.isSafeInteger(key.group_id) && Number(key.group_id) > 0) && ['name', 'group_name', 'status'].every(field => typeof key[field] === 'string') && typeof key.expired === 'boolean' && typeof key.quota_exhausted === 'boolean' && (key.can_generate === undefined || typeof key.can_generate === 'boolean') && (key.has_history === undefined || typeof key.has_history === 'boolean'))
}
function storageKey(user: number | undefined, part: string): string | undefined {
  return user !== undefined && Number.isSafeInteger(user) && user > 0 ? `${prefix}${user}:${part}` : undefined
}
function read<T>(user: number | undefined, part: string, valid: (value: unknown) => value is T): T | undefined {
  const key = storageKey(user, part)
  if (!key) return undefined
  try {
    const raw = sessionStorage.getItem(key)
    if (!raw || oversized(raw)) return undefined
    const entry: unknown = JSON.parse(raw)
    if (!record(entry) || typeof entry.at !== 'number' || entry.at > Date.now() || Date.now() - entry.at >= maxAge || !valid(entry.value)) return undefined
    return entry.value
  } catch { return undefined }
}
function catalogueKeys(user: number | undefined): string[] {
  const scope = storageKey(user, 'model:')
  return scope ? Array.from({ length: sessionStorage.length }, (_, index) => sessionStorage.key(index)).filter((item): item is string => Boolean(item?.startsWith(scope))) : []
}
function storedAt(key: string): number {
  try {
    const entry: unknown = JSON.parse(sessionStorage.getItem(key) || '{}')
    return record(entry) && typeof entry.at === 'number' && Number.isFinite(entry.at) ? entry.at : 0
  } catch { return 0 }
}
function write(user: number | undefined, part: string, value: unknown) {
  const key = storageKey(user, part)
  if (!key) return
  try {
    const raw = JSON.stringify({ at: Date.now(), value })
    if (oversized(raw)) { sessionStorage.removeItem(key); return }
    sessionStorage.setItem(key, raw)
    const entries = catalogueKeys(user)
    entries.sort((a, b) => storedAt(a) - storedAt(b))
    for (const previous of entries.slice(0, Math.max(0, entries.length - maxCatalogues))) sessionStorage.removeItem(previous)
  } catch { /* Storage may be blocked or full; network discovery remains available. */ }
}
export const cachedStudioKeys = (user: number): StudioKey[] | undefined => read(user, 'keys', validKeys)
export function cacheStudioKeys(user: number, keys: readonly StudioKey[]) {
  const previous = new Map(cachedStudioKeys(user)?.map(key => [key.id, key]))
  const current = new Map(keys.map(key => [key.id, key]))
  try {
    for (const name of catalogueKeys(user)) {
      const id = Number(name.slice(name.lastIndexOf(':') + 1))
      const before = previous.get(id)
      const next = current.get(id)
      if (!before || !next || before.group_id !== next.group_id || next.can_generate === false) sessionStorage.removeItem(name)
    }
  } catch { /* Optional cache; fresh permissions still govern every request. */ }
  write(user, 'keys', keys.map(({ id, name, group_id, group_name, status, expired, quota_exhausted, can_generate, has_history }) => ({ id, name, group_id, group_name, status, expired, quota_exhausted, can_generate, has_history })))
}
export const cachedStudioModels = (user: number | undefined, key: number): BatchImageModel[] | undefined => read(user, `model:${key}`, (value): value is BatchImageModel[] => Array.isArray(value) && value.length <= 128 && value.every(validModel))
export function cacheStudioModels(user: number | undefined, key: number, models: BatchImageModel[]) {
  write(user, `model:${key}`, models.map(({ id, object, provider, capabilities, prices }) => ({ id, object, provider, capabilities, prices })))
}
export function forgetStudioModels(user: number | undefined, key: number) {
  const name = storageKey(user, `model:${key}`)
  try { if (name) sessionStorage.removeItem(name) } catch { /* Optional cache. */ }
}
