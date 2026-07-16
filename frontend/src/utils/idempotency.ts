/** Create an ASCII idempotency key accepted by the backend write coordinator. */
export function createIdempotencyKey(prefix: string): string {
  const requestId =
    globalThis.crypto?.randomUUID?.() ??
    `${Date.now()}-${Math.random().toString(36).slice(2)}`
  return `${prefix}-${requestId}`
}
