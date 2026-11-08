export function resolveShopGuideRedirect(query: Readonly<Record<string, unknown>>): { readonly path: string; readonly hash?: string } {
  for (const [key, resource] of [['order', 'orders'], ['product', 'products']] as const) {
    const value = query[key]
    if (typeof value !== 'string' || !/^[1-9]\d*$/.test(value) || !Number.isSafeInteger(Number(value))) continue
    return key === 'order' ? { path: `/shop/${resource}/${value}`, hash: '#usage-guide' } : { path: `/shop/${resource}/${value}` }
  }
  return { path: '/shop' }
}
