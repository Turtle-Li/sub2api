export interface ShopAccountCredentials {
  account: string
  password: string
}

/** Recognize one account----password record without rewriting the delivery. */
export function parseShopAccountDelivery(content: string): ShopAccountCredentials | null {
  const line = content.replace(/(?:\r?\n)+$/, '')
  if (/[\r\n]/.test(line)) return null
  const separator = line.indexOf('----')
  if (separator < 0) return null
  const account = line.slice(0, separator)
  const password = line.slice(separator + 4)
  if (!account.trim() || !password.trim()) return null
  return { account, password }
}
