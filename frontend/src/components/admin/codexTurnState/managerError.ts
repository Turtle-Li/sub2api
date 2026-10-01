type Translate = (key: string) => string

// 管理器经后端代理后错误正文被统一替换，只保留 400/403/404/409/429 状态码；按状态码给出可读提示。
const STATUS_KEYS: Record<number, string> = {
  0: 'admin.codexTurnState.errors.unavailable',
  400: 'admin.codexTurnState.errors.rejected',
  403: 'admin.codexTurnState.errors.forbidden',
  404: 'admin.codexTurnState.errors.notFound',
  409: 'admin.codexTurnState.errors.conflict',
  429: 'admin.codexTurnState.errors.tooMany',
  502: 'admin.codexTurnState.errors.unavailable',
  503: 'admin.codexTurnState.errors.unavailable',
}

export function describeManagerError(err: unknown, t: Translate, fallbackKey: string): string {
  const status = (err as { status?: unknown } | null)?.status
  const key = typeof status === 'number' ? STATUS_KEYS[status] : undefined
  return t(key ?? fallbackKey)
}
