/**
 * 网关实际暴露的协议面与端点。
 *
 * 来源是 backend/internal/server/routes/gateway.go 的路由注册，不是
 * 市场文案。改后端路由时请同步这里，否则文档会撒谎。
 *
 * 端点只给 method + path：对开发者来说这已经自解释，逐条写说明反而
 * 会让表变稀。分组标题在 i18n 里（site.docs.protocols.groups.*）。
 */

export interface ProtocolSurface {
  /** 对应 site.platform.protocols.items.* */
  key: 'anthropic' | 'openai' | 'gemini' | 'codex' | 'antigravity'
  base: string
}

/** 四套协议面 + Antigravity 专用端点。 */
export const PROTOCOL_SURFACES: ProtocolSurface[] = [
  { key: 'anthropic', base: '/v1' },
  { key: 'openai', base: '/v1' },
  { key: 'gemini', base: '/v1beta' },
  { key: 'codex', base: '/backend-api/codex' },
  { key: 'antigravity', base: '/antigravity/v1' },
]

export interface Endpoint {
  method: 'GET' | 'POST'
  path: string
}

export interface EndpointGroup {
  /** 对应 site.docs.protocols.groups.* */
  key: string
  endpoints: Endpoint[]
}

export const ENDPOINT_GROUPS: EndpointGroup[] = [
  {
    key: 'anthropic',
    endpoints: [
      { method: 'POST', path: '/v1/messages' },
      { method: 'POST', path: '/v1/messages/count_tokens' },
    ],
  },
  {
    key: 'openai',
    endpoints: [
      { method: 'POST', path: '/v1/chat/completions' },
      { method: 'POST', path: '/v1/responses' },
      { method: 'POST', path: '/v1/embeddings' },
      { method: 'GET', path: '/v1/models' },
    ],
  },
  {
    key: 'gemini',
    endpoints: [
      { method: 'GET', path: '/v1beta/models' },
      { method: 'GET', path: '/v1beta/models/{model}' },
    ],
  },
  {
    key: 'images',
    endpoints: [
      { method: 'POST', path: '/v1/images/generations' },
      { method: 'POST', path: '/v1/images/edits' },
      { method: 'POST', path: '/v1/images/generations/async' },
      { method: 'GET', path: '/v1/images/tasks/{task_id}' },
      { method: 'POST', path: '/v1/images/batches' },
      { method: 'GET', path: '/v1/images/batches/{id}' },
      { method: 'GET', path: '/v1/images/batches/{id}/download' },
      { method: 'POST', path: '/v1/images/batches/{id}/cancel' },
    ],
  },
  {
    key: 'video',
    endpoints: [
      { method: 'POST', path: '/v1/videos/generations' },
      { method: 'POST', path: '/v1/videos/edits' },
      { method: 'POST', path: '/v1/videos/extensions' },
      { method: 'GET', path: '/v1/videos/{request_id}' },
      { method: 'GET', path: '/v1/videos/{request_id}/content' },
    ],
  },
  {
    key: 'voice',
    endpoints: [
      { method: 'POST', path: '/v1/tts' },
      { method: 'POST', path: '/v1/stt' },
      { method: 'POST', path: '/v1/custom-voices' },
      { method: 'GET', path: '/v1/custom-voices' },
    ],
  },
  {
    key: 'realtime',
    endpoints: [
      { method: 'GET', path: '/v1/realtime' },
      { method: 'POST', path: '/v1/web_search' },
      { method: 'POST', path: '/v1/x_search' },
    ],
  },
  {
    key: 'account',
    endpoints: [
      { method: 'GET', path: '/v1/usage' },
      { method: 'GET', path: '/v1/sub2api/billing' },
    ],
  },
]
