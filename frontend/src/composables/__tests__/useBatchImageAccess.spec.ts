import { describe, expect, it } from 'vitest'
import { keyAllowsBatchImage } from '@/composables/useBatchImageAccess'

function key(platform: string, overrides: Record<string, unknown> = {}) {
  return {
    status: 'active',
    group: {
      platform,
      allow_batch_image_generation: true,
    },
    ...overrides,
  } as any
}

describe('keyAllowsBatchImage', () => {
  it('allows enabled Gemini and OpenAI groups', () => {
    expect(keyAllowsBatchImage(key('gemini'))).toBe(true)
    expect(keyAllowsBatchImage(key('openai'))).toBe(true)
  })

  it('keeps inactive, disabled, and unrelated groups hidden', () => {
    expect(keyAllowsBatchImage(key('openai', { status: 'disabled' }))).toBe(false)
    expect(keyAllowsBatchImage(key('openai', { group: { platform: 'openai', allow_batch_image_generation: false } }))).toBe(false)
    expect(keyAllowsBatchImage(key('grok'))).toBe(false)
  })
})
