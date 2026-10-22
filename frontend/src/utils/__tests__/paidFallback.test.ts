import { describe, expect, it } from 'vitest'
import { isPaidFallback } from '../paidFallback'

describe('paid fallback quarantine', () => {
  it.each([null, undefined, {}, { paid_fallback: false }])('leaves missing and false markers ordinary', extra => {
    expect(isPaidFallback(extra)).toBe(false)
  })
  it.each([true, null, 'false', 0, [], {}])('quarantines every other present value', value => {
    expect(isPaidFallback({ paid_fallback: value })).toBe(true)
  })
})
