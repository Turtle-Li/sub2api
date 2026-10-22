export function isPaidFallback(extra: Record<string, unknown> | null | undefined): boolean {
  return !!extra && Object.prototype.hasOwnProperty.call(extra, 'paid_fallback') && extra.paid_fallback !== false
}
