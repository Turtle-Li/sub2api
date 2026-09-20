import { afterEach, describe, expect, it, vi } from 'vitest'
import panelHTML from '@/assets/codex-turn-state-panel.html?raw'

const removers: Array<() => void> = []
afterEach(() => {
  window.dispatchEvent(new Event('pagehide'))
  vi.useRealTimers()
  removers.splice(0).forEach(remove => remove())
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  document.body.replaceChildren()
})

function startPanel(readOnly = false) {
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
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
  const state = { settings:{refresh_advance_minutes:15}, renewal_timing:{samples:3,average_seconds:18.5,last_seconds:12}, read_only: readOnly, generated_at: Date.now() / 1000,
    accounts: [{ id: 9, name: '测试账号', models: [{ model: 'gpt-6-astra', valid: true, expired: false,
      state_len: 292, target_len: 292, remaining_minutes: 40, next_probe_seconds: 1200 }] }],
    degraded_enabled: true, degraded: [{ account_id: 10, account_name: '其他账号', sent_model: 'gpt-6-astra', count: 1 }],
    jobs: [], static_proxy_count: 10, dynamic_provider_count: 1 }
  const requests: Array<{method: string; path: string; body?: {enabled?: boolean;refresh_advance_minutes?:number}}> = []
  vi.spyOn(window.parent, 'postMessage').mockImplementation(message => {
    if (message.type !== 'ctsm-request') return
    requests.push(message)
    const path: string = message.path
    if (path.endsWith('/enabled')) enabled = message.body.enabled
    if (path === 'api/settings' && message.method === 'POST') state.settings.refresh_advance_minutes = message.body.refresh_advance_minutes
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
        ok: !(failState && path === 'api/state'), error: 'Unavailable', data: JSON.parse(JSON.stringify(data))},
    })))
  })
  const script = panelHTML.match(/<script>([\s\S]*?)<\/script>/)?.[1]
  expect(script).toBeTruthy()
  new Function(script!)()
  return { state, requests, fail: () => { failState = true } }
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
  it('counts down and refreshes changed data without replacing rows or wiping unsaved settings', async () => {
    vi.useFakeTimers({toFake:['setInterval','clearInterval','Date','performance']})
    const {state, requests} = startPanel()
    await vi.waitFor(() => expect(node('models').textContent).toContain('40 分'))
    const row=node('models').firstElementChild
    const input=node('advanceMinutes') as HTMLInputElement
    input.value='5';input.dispatchEvent(new Event('input'));input.focus()
    await vi.advanceTimersByTimeAsync(2000)
    expect(node('models').textContent).toContain('39 分 59 秒')
    expect(node('models').firstElementChild).toBe(row)
    state.accounts[0].models[0].state_len=312
    state.generated_at += 10
    await vi.advanceTimersByTimeAsync(8000)
    await vi.waitFor(() => expect(node('models').textContent).toContain('312'))
    expect(requests.filter(r=>r.path==='api/state')).toHaveLength(2)
    expect(node('models').firstElementChild).toBe(row)
    expect(input.value).toBe('5')
    expect(document.activeElement).toBe(input)
    expect(node('timingSummary').textContent).toContain('18.5 秒')
    node('saveAdvance').click()
    await vi.waitFor(() => expect(requests.some(r=>r.path==='api/settings' && r.body?.refresh_advance_minutes===5)).toBe(true))
    await vi.waitFor(() => expect(node('advanceStatus').textContent).toContain('当前提前 5'))
  })

  it('retains a decreasing countdown for a repeated stale snapshot', async () => {
    vi.useFakeTimers({toFake:['setInterval','clearInterval','Date','performance']})
    startPanel()
    await vi.waitFor(() => expect(node('models').textContent).toContain('40 分'))
    await vi.advanceTimersByTimeAsync(12000)
    expect(node('models').textContent).not.toContain('40 分 0 秒')
    expect(node('models').textContent).toMatch(/39 分 (4[7-9]|50) 秒/)
  })

  it('shows an account read error without failing the entire panel', async () => {
    const {state}=startPanel()
    await vi.waitFor(() => expect(node('models').textContent).toContain('测试账号'))
    Object.assign(state.accounts[0],{error:'账号状态不可用'})
    state.generated_at += 1
    node('refresh').click()
    await vi.waitFor(() => expect(node('models').textContent).toContain('账号状态不可用'))
    expect(node('meta').textContent).not.toContain('加载失败')
  })

})
