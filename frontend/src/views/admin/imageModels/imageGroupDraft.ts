import type { AdminGroup, UpdateGroupRequest } from '@/types'
import { buildModelAllowlistConfig, createModelAllowlistState } from '../groupModelAllowlist'

export const imageFields = [
  'allow_image_generation', 'allow_batch_image_generation', 'image_rate_independent',
  'image_rate_multiplier', 'batch_image_discount_multiplier', 'batch_image_hold_multiplier',
  'image_price_1k', 'image_price_2k', 'image_price_4k',
] as const

export type ImageGroupDraft = Pick<AdminGroup, typeof imageFields[number]> & {
  allowlistEnabled: boolean
  rules: string
}

export function createImageDraft(group: AdminGroup): ImageGroupDraft {
  const state = createModelAllowlistState(group.model_allowlist)
  return {
    allow_image_generation: group.allow_image_generation,
    allow_batch_image_generation: group.allow_batch_image_generation,
    image_rate_independent: group.image_rate_independent,
    image_rate_multiplier: group.image_rate_multiplier,
    batch_image_discount_multiplier: group.batch_image_discount_multiplier,
    batch_image_hold_multiplier: group.batch_image_hold_multiplier,
    image_price_1k: group.image_price_1k,
    image_price_2k: group.image_price_2k,
    image_price_4k: group.image_price_4k,
    allowlistEnabled: state.enabled,
    rules: state.savedModels.join('\n'),
  }
}

export function parseImageRules(rules: string): string[] {
  return createModelAllowlistState({ models: rules.split('\n') }).savedModels
}

export function imageDraftPatch(group: AdminGroup, draft: ImageGroupDraft): UpdateGroupRequest {
  // Keys and values originate from the typed form; unrelated group fields never enter this patch.
  // Existing group updates use -1 to clear a price; JSON null means leave unchanged.
  const patch: UpdateGroupRequest = Object.fromEntries(
    imageFields.filter(key => draft[key] !== group[key]).map(key => [key, draft[key] === null ? -1 : draft[key]]),
  )
  const config = buildModelAllowlistConfig(createModelAllowlistState({
    enabled: draft.allowlistEnabled, models: parseImageRules(draft.rules),
  }))
  const original = buildModelAllowlistConfig(createModelAllowlistState(group.model_allowlist))
  if (JSON.stringify(config) !== JSON.stringify(original)) patch.model_allowlist = config
  return patch
}

export function imageRuleCovers(rule: string, model: string): boolean {
  const pattern = rule.split('*').map(part => part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('.*')
  const matcher = new RegExp(`^${pattern}$`, 'i')
  return [model, model.replace(/^models\//, '')].some(candidate => matcher.test(candidate))
}

export function toggleExactImageRule(rules: string, model: string): string {
  const entries = parseImageRules(rules)
  const exact = entries.some(entry => entry.toLowerCase() === model.toLowerCase())
  return (exact ? entries.filter(entry => entry.toLowerCase() !== model.toLowerCase()) : [...entries, model]).join('\n')
}
