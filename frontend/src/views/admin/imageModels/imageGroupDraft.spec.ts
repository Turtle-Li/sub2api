import { describe, expect, it } from 'vitest'
import type { AdminGroup } from '@/types'
import { createImageDraft, imageDraftPatch, imageRuleCovers, toggleExactImageRule } from './imageGroupDraft'

const group = {
  id: 7, name: 'Mixed group', platform: 'openai', rate_multiplier: 2,
  allow_image_generation: true, allow_batch_image_generation: true,
  image_rate_independent: false, image_rate_multiplier: 1,
  batch_image_discount_multiplier: 0.5, batch_image_hold_multiplier: 1,
  image_price_1k: null, image_price_2k: 0.2, image_price_4k: null,
  model_allowlist: { enabled: true, models: ['gpt-6*', 'custom/text', 'muse-image'] },
} as AdminGroup

describe('image group draft', () => {
  it('saves only changed image fields and does not send the account/group configuration', () => {
    const draft = createImageDraft(group)
    expect(imageDraftPatch(group, draft)).toEqual({})
    draft.image_price_1k = 0.1
    expect(imageDraftPatch(group, draft)).toEqual({ image_price_1k: 0.1 })
  })
  it('retains non-image rules and wildcards while adding/removing exact image rules', () => {
    const draft = createImageDraft(group)
    draft.rules = toggleExactImageRule(draft.rules, 'gpt-image-2')
    expect(imageDraftPatch(group, draft).model_allowlist?.models).toEqual(['gpt-6*', 'custom/text', 'muse-image', 'gpt-image-2'])
    draft.rules = toggleExactImageRule(draft.rules, 'MUSE-IMAGE')
    expect(imageDraftPatch(group, draft).model_allowlist?.models).toEqual(['gpt-6*', 'custom/text', 'gpt-image-2'])
  })
  it('preserves disabled stored rules and allows explicit default-price restoration', () => {
    const original = { ...group, model_allowlist: { enabled: false, models: ['custom/text'] } }
    const draft = createImageDraft(original)
    draft.image_price_2k = null
    expect(imageDraftPatch(original, draft)).toEqual({ image_price_2k: -1 })
    draft.allowlistEnabled = true
    expect(imageDraftPatch(original, draft).model_allowlist).toEqual({ enabled: true, models: ['custom/text'] })
  })
  it('omits unchanged null prices and preserves explicit zero prices', () => {
    const draft = createImageDraft(group)
    expect(imageDraftPatch(group, draft)).toEqual({})
    draft.image_price_1k = 0
    draft.image_price_2k = null
    expect(imageDraftPatch(group, draft)).toEqual({ image_price_1k: 0, image_price_2k: -1 })
  })
  it.each([
    ['*', 'muse-image', true], ['GPT-IMAGE-*', 'gpt-image-2', true],
    ['models/*', 'models/gemini-2.5-flash-image', true],
    ['gemini-*', 'models/gemini-2.5-flash-image', true],
    ['models/*', 'gemini-2.5-flash-image', false],
    ['gpt-image-2.5', 'gpt-image-2x5', false], ['muse-?', 'muse-a', false],
  ])('matches image rule %s against %s', (rule, model, expected) => {
    expect(imageRuleCovers(rule, model)).toBe(expected)
  })
})
