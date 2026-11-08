import type { ShopProduct, ShopSKU } from '@/types/shop'

export function skuLabel(sku: ShopSKU): string {
  return Object.values(sku.attributes).join(' / ') || '默认规格'
}

export function skuAttributeKey(attributes: Record<string, string>): string {
  return JSON.stringify(Object.entries(attributes).sort(([a], [b]) => a.localeCompare(b)))
}

export function newShopSKU(product: Pick<ShopProduct, 'price' | 'duration_value' | 'duration_unit'>, attributes: Record<string, string> = {}): ShopSKU {
  return { id: `sku_${crypto.randomUUID()}`, attributes, price: product.price, duration_value: product.duration_value, duration_unit: product.duration_unit, concurrency: 0, enabled: true }
}

export function editableShopSKUs(product: ShopProduct): ShopSKU[] {
  if (product.skus?.length) return product.skus.map(sku => ({ ...sku, attributes: { ...sku.attributes } }))
  if (product.concurrency_options?.length) {
    return product.concurrency_options.flatMap(option => {
      const periods = [{ days: 1, price: option.price }, ...(option.weekly_price === undefined ? [] : [{ days: 7, price: option.weekly_price }])]
      return periods.map(period => ({ ...newShopSKU(product, { 有效期: period.days === 1 ? '日卡' : '周卡', 并发: `${option.concurrency} 路` }), price: period.price, duration_value: period.days, duration_unit: 'day' as const, concurrency: option.concurrency }))
    })
  }
  return [newShopSKU(product)]
}

export function shopPriceRange(product: ShopProduct): { price: number; multiple: boolean } {
  const skus = product.skus?.filter(sku => sku.enabled) ?? []
  return { price: skus.length ? Math.min(...skus.map(sku => sku.price)) : product.price, multiple: skus.length > 1 || !!product.concurrency_options?.length }
}
