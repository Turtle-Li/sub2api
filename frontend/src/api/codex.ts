export interface CodexModelsManifestResult {
  content: string
  modelCount: number
}

export type CodexModelsRequestErrorKind = 'network' | 'http' | 'manifest'

export class CodexModelsRequestError extends Error {
  readonly kind: CodexModelsRequestErrorKind
  readonly status?: number

  constructor(kind: CodexModelsRequestErrorKind, message: string, status?: number) {
    super(message)
    this.name = 'CodexModelsRequestError'
    this.kind = kind
    this.status = status
  }
}

const DEFAULT_CODEX_CLIENT_VERSION = '0.147.0'

function normalizeCodexBaseUrl(baseUrl: string): string {
  const fallback = typeof window !== 'undefined' ? window.location.origin : ''
  const value = (baseUrl || fallback).trim().replace(/\/+$/, '')
  if (!value) return '/v1'
  return /\/v1$/i.test(value) ? value : `${value}/v1`
}

export function buildCodexModelsManifestUrl(
  baseUrl: string,
  clientVersion = DEFAULT_CODEX_CLIENT_VERSION
): string {
  const url = normalizeCodexBaseUrl(baseUrl)
  const params = new URLSearchParams({ client_version: clientVersion })
  return `${url}/models?${params.toString()}`
}

function isCodexModelsManifest(value: unknown): value is { models: unknown[] } {
  return typeof value === 'object' && value !== null && Array.isArray((value as { models?: unknown }).models)
}

async function readHTTPErrorMessage(response: Response): Promise<string> {
  try {
    const payload: unknown = await response.json()
    if (typeof payload !== 'object' || payload === null) return ''
    const record = payload as Record<string, unknown>
    const nestedError = record.error
    if (typeof nestedError === 'object' && nestedError !== null) {
      const message = (nestedError as Record<string, unknown>).message
      if (typeof message === 'string') return message.trim().slice(0, 240)
    }
    if (typeof record.message === 'string') return record.message.trim().slice(0, 240)
  } catch {
    // The API may return an empty body or an edge-generated non-JSON response.
  }
  return ''
}

export async function fetchCodexModelsManifest(
  baseUrl: string,
  apiKey: string,
  signal?: AbortSignal
): Promise<CodexModelsManifestResult> {
  let response: Response
  try {
    response = await fetch(buildCodexModelsManifestUrl(baseUrl), {
      method: 'GET',
      headers: {
        Accept: 'application/json',
        Authorization: `Bearer ${apiKey}`
      },
      cache: 'no-store',
      signal
    })
  } catch (error) {
    if (error && typeof error === 'object' && 'name' in error && (error as { name?: unknown }).name === 'AbortError') {
      throw error
    }
    throw new CodexModelsRequestError(
      'network',
      'The browser could not reach the Codex models endpoint; this commonly indicates a CORS or network configuration error.'
    )
  }

  if (!response.ok) {
    const detail = await readHTTPErrorMessage(response)
    const suffix = detail ? `: ${detail}` : ''
    throw new CodexModelsRequestError(
      'http',
      `Codex models request failed with status ${response.status}${suffix}`,
      response.status
    )
  }

  const payload: unknown = await response.json()
  if (!isCodexModelsManifest(payload)) {
    throw new CodexModelsRequestError('manifest', 'Codex models response is not a valid manifest')
  }

  return {
    content: JSON.stringify(payload, null, 2),
    modelCount: payload.models.length
  }
}
