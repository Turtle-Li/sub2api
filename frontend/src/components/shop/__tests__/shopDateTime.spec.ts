import { describe, expect, it } from 'vitest'
import { shopBeijingDateTime } from '../shopDateTime'

describe('shopBeijingDateTime', () => {
  it('renders Beijing time regardless of the machine time zone', () => {
    // 2026-11-28T10:00:00Z is 18:00 in Beijing, still the same day.
    expect(shopBeijingDateTime('2026-11-28T10:00:00Z')).toBe('2026-11-28 18:00:00')
  })

  it('crosses the date boundary into the next Beijing day', () => {
    // 2026-11-28T17:30:00Z is 01:30 on 2026-11-29 in Beijing.
    expect(shopBeijingDateTime('2026-11-28T17:30:00Z')).toBe('2026-11-29 01:30:00')
  })

  it('accepts epoch milliseconds and Date input', () => {
    const ms = Date.parse('2026-11-28T10:00:00Z')
    expect(shopBeijingDateTime(ms)).toBe('2026-11-28 18:00:00')
    expect(shopBeijingDateTime(new Date(ms))).toBe('2026-11-28 18:00:00')
  })

  it('returns an empty string for missing or unparseable values', () => {
    expect(shopBeijingDateTime(null)).toBe('')
    expect(shopBeijingDateTime(undefined)).toBe('')
    expect(shopBeijingDateTime('')).toBe('')
    expect(shopBeijingDateTime('not-a-date')).toBe('')
  })
})
