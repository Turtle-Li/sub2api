import { onScopeDispose, ref } from 'vue'
import channelsAPI from '@/api/admin/channels'
import { extractApiErrorMessage } from '@/utils/apiError'
import { normalizePricingCurrency, perTokenToMTok, type PricingFormEntry } from './types'

const priceFields = [
  'input_price', 'output_price', 'cache_write_price', 'cache_write_1h_price',
  'cache_read_price', 'image_input_price', 'image_output_price',
] as const

export function useModelDefaultPricing(
  entry: () => PricingFormEntry,
  update: (entry: PricingFormEntry) => void,
) {
  const state = ref('')
  let revision = 0
  onScopeDispose(() => { revision++ })

  async function fillDefaultPrices(models = entry().models) {
    const model = models[0]
    if (models.length !== 1 || !model || entry().billing_mode !== 'token') return
    const request = ++revision
    const currency = normalizePricingCurrency(entry().currency)
    const prices = JSON.stringify(priceFields.map(field => entry()[field]))
    state.value = 'admin.channels.form.defaultPricingLoading'
    try {
      const result = await channelsAPI.getModelDefaultPricing(model, currency)
      if (request !== revision) return
      if (entry().models.length !== 1 || entry().models[0] !== model ||
          entry().billing_mode !== 'token' || normalizePricingCurrency(entry().currency) !== currency ||
          JSON.stringify(priceFields.map(field => entry()[field])) !== prices) {
        state.value = ''
        return
      }
      if (!result.found) {
        state.value = 'admin.channels.form.defaultPricingMissing'
        return
      }
      if ((result.currency ?? 'USD') !== currency) {
        state.value = 'admin.channels.form.defaultPricingError'
        return
      }
      const next = { ...entry(), models }
      for (const field of priceFields) {
        next[field] = entry()[field] ?? perTokenToMTok(result[field] ?? null)
      }
      next.reasoning_effort_multipliers = entry().reasoning_effort_multipliers ?? result.reasoning_effort_multipliers ?? null
      update(next)
      state.value = 'admin.channels.form.defaultPricingFilled'
    } catch (error) {
      if (request === revision) {
        state.value = 'admin.channels.form.defaultPricingError'
        failure.value = extractApiErrorMessage(error, '')
      }
    }
  }

  const failure = ref('')

  function onModelsUpdate(models: string[]) {
    const previous = entry()
    revision++
    state.value = ''
    failure.value = ''
    update({ ...previous, models })
    const hasPrice = priceFields.some(field => previous[field] != null) || previous.per_request_price != null
    if (previous.models.length === 0 && models.length === 1 && !hasPrice) {
      void fillDefaultPrices(models)
    }
  }

  return { state, failure, fillDefaultPrices, onModelsUpdate }
}
