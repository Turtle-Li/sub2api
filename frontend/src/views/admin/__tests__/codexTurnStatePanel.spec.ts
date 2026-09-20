import { afterEach, describe, expect, it, vi } from 'vitest'
import panelHTML from '@/assets/codex-turn-state-panel.html?raw'

const removers: Array<() => void> = []
afterEach(() => {
  removers.splice(0).forEach(remove => remove())
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  document.body.replaceChildren()
})

function startPanel(readOnly = false) {
  document.documentElement.innerHTML = panelHTML.replace(/<script>[\s\S]*?<\/script>/g, '')
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
  for (const target of [window, document]) {
    const add = target.addEventListener.bind(target)
    vi.spyOn(target, 'addEventListener').mockImplementation((type, handler, options) => {
      add(type, handler, options)
      removers.push(() => target.removeEventListener(type, handler, options))
    })
  }
  let failState = false
  let enabled = true
  const state = { read_only: readOnly, generated_at: Date.now() / 1000,
    accounts: [{ id: 9, name: '测试账号', models: [{ model: 'gpt-6-astra', valid: true, expired: false,
      state_len: 292, target_len: 292, remaining_minutes: 40, next_probe_seconds: 1200 }] }],
    degraded_enabled: true, degraded: [{ account_id: 10, account_name: '其他账号', sent_model: 'gpt-6-astra', count: 1 }],
    jobs: [], static_proxy_count: 10, dynamic_provider_count: 1 }
  const requests: Array<{method: string; path: string; body?: {enabled?: boolean}}> = []
  vi.spyOn(window.parent, 'postMessage').mockImplementation(message => {
    if (message.type !== 'ctsm-request') return
    requests.push(message)
    const path: string = message.path
    if (path.endsWith('/enabled')) enabled = message.body.enabled
    const data = path === 'api/state' ? state : path.startsWith('api/stats') ? {
      totals: { attempts: 12, persisted: 2 }, by_source: {
        static: {attempts: 2, http_200: 2, target_hits: 2, persisted: 2, errors: 0},
        dynamic: {attempts: 10, http_200: 9, target_hits: 0, persisted: 0, errors: 1},
      },
    } : path === 'api/proxy-sources' ? { sources: [
      {id: 'base-static', name: 'static', type: 'static', origin: 'base', read_only: true, enabled: true, endpoint_count: 10, status: 'loaded'},
      {id: '0123456789abcdef0123456789abcdef', name: 'new-pool', type: 'rotating', origin: 'managed', enabled, endpoint_count: 1, status: enabled ? 'loaded' : 'disabled'},
    ] } : {}
    queueMicrotask(() => window.dispatchEvent(new MessageEvent('message', {
      source: window, data: {type: 'ctsm-result', id: message.id,
        ok: !(failState && path === 'api/state'), error: 'Unavailable', data},
    })))
  })
  const script = panelHTML.match(/<script>([\s\S]*?)<\/script>/)?.[1]
  expect(script).toBeTruthy()
  new Function(script!)()
  return { requests, fail: () => { failState = true } }
}
const node = (id: string) => document.getElementById(id)!

describe('actual embedded Codex panel source inventory', () => {
  it('distinguishes zero hits from no samples and sends pause/resume through the bridge', async () => {
    const {requests} = startPanel()
    await vi.waitFor(() => expect(node('sourceSummary').textContent).toContain('10 次尝试 / 0 命中'))
    expect(node('sources').textContent).toContain('尚无尝试，不能判断命中能力')
    expect(node('sources').textContent).toContain('请求成功 9 次，但未获得目标状态')
    const toggle = () => node('proxySources').querySelector('button')!
    toggle().click()
    await vi.waitFor(() => expect(toggle().textContent).toBe('启用'))
    expect(requests.at(-2)).toMatchObject({method:'POST',path:'api/proxy-sources/0123456789abcdef0123456789abcdef/enabled',body:{enabled:false}})
    toggle().click()
    await vi.waitFor(() => expect(toggle().textContent).toBe('暂停'))
  })

  it('keeps inventory visible in readonly mode without any source or degraded-account mutation controls', async () => {
    startPanel(true)
    await vi.waitFor(() => expect(node('proxySources').textContent).toContain('现有配置'))
    expect(node('proxyImportSection').hidden).toBe(false)
    expect(node('proxyImportForm').hidden).toBe(true)
    expect(node('proxySources').querySelectorAll('button')).toHaveLength(0)
    expect(node('degraded').querySelectorAll('[data-add]')).toHaveLength(0)
  })

  it('clears stale healthy status and management controls after a failed refresh', async () => {
    const {fail} = startPanel()
    await vi.waitFor(() => expect(node('renewalSummary').textContent).toContain('1 个当前有效'))
    fail()
    node('refresh').click()
    await vi.waitFor(() => expect(node('models').textContent).toContain('当前状态未知'))
    expect(node('renewalSummary').textContent).not.toContain('当前有效')
    expect(node('managementSection').hidden).toBe(true)
    expect(node('proxySources').querySelectorAll('button')).toHaveLength(0)
  })
})
