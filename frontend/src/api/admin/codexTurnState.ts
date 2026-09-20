import { apiClient } from '../client'

// Only the existing daemon API is bridged; scheduling stays in the legacy tool.
const allowed = /^api\/(?:state|degraded|stats|history|jobs(?:\/[A-Za-z0-9_-]+)?|probe|accounts(?:\/[1-9][0-9]*\/[A-Za-z0-9_.:@-]+)?|proxy-sources(?:\/[A-Za-z0-9_-]+(?:\/enabled)?)?)$/
export async function requestCodexPanel(method: string, path: string, body?: unknown): Promise<unknown> {
  if (!/^\/?api\//.test(path) || path.includes('..') || path.includes('\\')) throw new Error('Unsupported panel path')
  const parsed = new URL(path, 'https://panel.invalid/')
  const route = parsed.pathname.replace(/^\//, '')
  if (!allowed.test(route) || !['GET', 'POST', 'DELETE'].includes(method)) throw new Error('Unsupported panel request')
  const { data } = await apiClient.request({ method, url: `/admin/codex-turn-state/${route}${parsed.search}`, data: body, timeout: 20000 })
  return data
}
