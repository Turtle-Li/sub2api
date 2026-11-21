import { shallowRef } from 'vue'

interface PosterEntry {
  readonly id: string
  readonly url: string
  readonly expiresAt?: number
  users: number
  revoked: boolean
  timer?: ReturnType<typeof setTimeout>
}

export function createVideoPosterCache() {
  const version = shallowRef(0)
  const current = new Map<string, PosterEntry>()
  const entries = new Set<PosterEntry>()

  function revoke(entry: PosterEntry) {
    if (!entry.revoked || entry.users || !entry.url) return
    URL.revokeObjectURL(entry.url)
    entries.delete(entry)
  }
  function detach(entry: PosterEntry) {
    if (current.get(entry.id) === entry) current.delete(entry.id)
    entry.revoked = true
    clearTimeout(entry.timer)
    version.value += 1
    revoke(entry)
  }
  function invalidate(id: string) {
    const entry = current.get(id)
    if (entry) detach(entry)
  }
  function url(id: string) {
    version.value
    const entry = current.get(id)
    if (!entry) return ''
    if (entry.expiresAt !== undefined && entry.expiresAt <= Date.now()) {
      detach(entry)
      return ''
    }
    return entry.url
  }
  function retain(id: string) {
    const entry = current.get(id)
    if (!entry || (entry.expiresAt !== undefined && entry.expiresAt <= Date.now())) {
      if (entry) detach(entry)
      return () => {}
    }
    entry.users += 1
    let retained = true
    return () => {
      if (!retained) return
      retained = false
      entry.users -= 1
      revoke(entry)
    }
  }
  function put(id: string, objectUrl: string, expiresAtISO?: string) {
    const expiresAt = expiresAtISO === undefined ? undefined : Date.parse(expiresAtISO)
    if (expiresAt !== undefined && (Number.isNaN(expiresAt) || expiresAt <= Date.now())) {
      invalidate(id)
      URL.revokeObjectURL(objectUrl)
      return false
    }
    const previous = current.get(id)
    const entry: PosterEntry = { id, url: objectUrl, expiresAt, users: 0, revoked: false }
    current.set(id, entry)
    entries.add(entry)
    if (previous) detach(previous)
    if (expiresAt !== undefined) entry.timer = setTimeout(() => detach(entry), expiresAt - Date.now())
    version.value += 1
    return true
  }
  function dispose() {
    current.clear()
    for (const entry of entries) {
      entry.revoked = true
      clearTimeout(entry.timer)
    }
    version.value += 1
    for (const entry of [...entries]) revoke(entry)
  }

  return { url, retain, put, invalidate, dispose }
}

export type VideoPosterCache = ReturnType<typeof createVideoPosterCache>
