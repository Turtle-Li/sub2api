/**
 * Admin Cline Pass upstream pinning API endpoints.
 *
 * Cline Pass 订阅模型在 Cline 网关之后分两条路由管道（直连 OpenRouter / 规划器 Vercel AI
 * Gateway），钉住写法不同。这些接口枚举、校验并钉住每条模型背后的上游渠道，
 * 配置与探测快照保存在账号 Extra 中（cline_upstream_pins / cline_upstream_meta）。
 */

import { apiClient } from '../client'

/** 钉住模式：off 不钉住（仍可排除/排序）、strict 严格钉单个渠道、preferred 优先+回退。 */
export type ClinePinMode = 'off' | 'strict' | 'preferred'

/** 渠道排序偏好。 */
export type ClineUpstreamSort = '' | 'cost' | 'ttft' | 'tps'

/** 路由管道：direct 走 OpenRouter（顶层 provider.*）、planner 走 Vercel AI Gateway。 */
export type ClinePipeline = '' | 'direct' | 'planner'

/** 单渠道实测状态。 */
export type ClineUpstreamStatusKind = 'ok' | 'limited' | 'bad' | 'auth' | 'unknown' | 'not-adopted'

/** 钉住是否被网关采纳（200 不代表生效，只有响应回读的实际渠道能证明）。 */
export type ClinePinAdherence = 'adopted' | 'fallback' | 'not-adopted' | 'violated' | 'unresolved'

/** 单个模型的钉住配置。 */
export interface ClineUpstreamPin {
  mode: ClinePinMode
  /** 按优先顺序勾选的渠道（preferred 模式即网关侧回退顺序）。 */
  upstreams: string[]
  /** 永不使用的渠道；已知渠道清单存在时换算成 only 白名单。 */
  exclude: string[]
  sort: ClineUpstreamSort
}

/** 单渠道实测结果。 */
export interface ClineUpstreamStatus {
  status: ClineUpstreamStatusKind
  ms: number
  note: string
  checked_at: number
}

/** 单个模型的探测快照。 */
export interface ClineUpstreamMeta {
  pipeline: ClinePipeline
  pinnable: boolean
  upstreams: string[]
  statuses: Record<string, ClineUpstreamStatus> | null
  last_provider: string
  canonical_slug: string
  last_error: string
  /** 渠道清单采集说明：成功为空，失败时是网关返回的原始错误片段。 */
  harvest_note: string
  /** 上游响应顶层声明的模型。 */
  response_model: string
  /** 网关路由元数据原文，用于解释为什么只有这些渠道。 */
  routing_raw: string
  probed_at: number
  validated_at: number
}

/** 单模型状态（配置 + 快照 + 展开后的候选顺序）。 */
export interface ClineUpstreamModelState {
  model: string
  /** subscription 订阅清单 / catalog 账号目录 / configured 仅本地已配置。 */
  source: string
  pin: ClineUpstreamPin
  meta: ClineUpstreamMeta
  candidates: string[]
  configured: boolean
}

/** 账号级钉住状态。 */
export interface ClineUpstreamState {
  account_id: number
  account_name: string
  platform: string
  /** 是否可做上游探测（官方 api.cline.bot 主机 + API Key 账号）。 */
  supported: boolean
  base_url: string
  models: ClineUpstreamModelState[]
  fetched_at: number
  models_error?: string
}

/** 单模型探测结果。 */
export interface ClineUpstreamProbeResult {
  model: string
  ok: boolean
  error?: string
  pipeline: ClinePipeline
  pinnable: boolean
  actual_provider: string
  canonical_slug: string
  fallbacks: string[] | null
  plan: string
  ms: number
  upstreams: string[] | null
  harvest_note?: string
}

/** 单模型全渠道校验结果。 */
export interface ClineUpstreamValidateResult {
  model: string
  pipeline: ClinePipeline
  upstreams: string[] | null
  statuses: Record<string, ClineUpstreamStatus> | null
  summary: Record<string, number> | null
}

/** 单次钉住验证结果。 */
export interface ClineUpstreamTestResult {
  model: string
  ok: boolean
  /** 本次请求是否真的按钉住执行。 */
  adherence: ClinePinAdherence
  error?: string
  ms: number
  pipeline: ClinePipeline
  actual_provider: string
  actual_provider_name: string
  canonical_slug: string
  content: string
  targets: string[] | null
  exclude: string[] | null
  candidates: string[] | null
}

const BASE = '/admin/accounts'

/** 读取账号的钉住配置、探测快照与可钉模型清单。 */
export async function getClineUpstreams(id: number): Promise<ClineUpstreamState> {
  const { data } = await apiClient.get<ClineUpstreamState>(`${BASE}/${id}/cline-upstreams`)
  return data
}

/** 全量覆盖账号的每模型钉住配置。 */
export async function updateClineUpstreams(
  id: number,
  pins: Record<string, ClineUpstreamPin>
): Promise<ClineUpstreamState> {
  const { data } = await apiClient.put<ClineUpstreamState>(`${BASE}/${id}/cline-upstreams`, { pins })
  return data
}

/** 探测模型（缺省为全部订阅模型），刷新管道类型与渠道清单。 */
export async function probeClineUpstreams(
  id: number,
  models?: string[]
): Promise<{ account_id: number; results: ClineUpstreamProbeResult[] }> {
  const { data } = await apiClient.post<{ account_id: number; results: ClineUpstreamProbeResult[] }>(
    `${BASE}/${id}/cline-upstreams/probe`,
    models && models.length ? { models } : {}
  )
  return data
}

/** 把模型的每个渠道各钉一次，返回可用性地图。 */
export async function validateClineUpstreams(
  id: number,
  model: string
): Promise<ClineUpstreamValidateResult> {
  const { data } = await apiClient.post<ClineUpstreamValidateResult>(
    `${BASE}/${id}/cline-upstreams/validate`,
    { model }
  )
  return data
}

/** 用临时钉住配置发一条真实请求，验证网关是否采纳（不落库）。 */
export async function testClineUpstreams(
  id: number,
  payload: { model: string } & Partial<ClineUpstreamPin>
): Promise<ClineUpstreamTestResult> {
  const { data } = await apiClient.post<ClineUpstreamTestResult>(
    `${BASE}/${id}/cline-upstreams/test`,
    payload
  )
  return data
}

export default {
  getClineUpstreams,
  updateClineUpstreams,
  probeClineUpstreams,
  validateClineUpstreams,
  testClineUpstreams
}
