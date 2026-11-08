import { describe, expect, it } from 'vitest'
import { resolveShopGuideRedirect } from '../shopGuideRedirect'

describe('retired shop guide URLs', () => {
  it.each([
    [{ order: '42' }, { path: '/shop/orders/42', hash: '#usage-guide' }],
    [{ product: '2' }, { path: '/shop/products/2' }],
    [{ order: '42', product: '2' }, { path: '/shop/orders/42', hash: '#usage-guide' }],
    [{ order: '//example.invalid' }, { path: '/shop' }],
    [{ order: ['42'] }, { path: '/shop' }],
    [{ order: '-42' }, { path: '/shop' }],
    [{ order: '0' }, { path: '/shop' }],
    [{ order: '9007199254740992' }, { path: '/shop' }],
    [{}, { path: '/shop' }],
  ])('redirects only to a bounded existing page: %j', (query, target) => {
    expect(resolveShopGuideRedirect(query)).toEqual(target)
  })
})
