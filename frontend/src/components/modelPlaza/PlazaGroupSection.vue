<template>
  <section
    class="overflow-hidden rounded-2xl border bg-white shadow-card dark:bg-dark-800/50"
    :class="[platformBorderStrongClass(group.platform)]"
  >
    <!-- 分组头部:名称/平台/倍率徽章/专属/订阅徽章 + 描述 -->
    <header class="border-b border-gray-100 px-5 py-4 dark:border-dark-700/60">
      <div class="flex flex-wrap items-center gap-2">
        <GroupBadge
          :name="group.name"
          :platform="isMuse ? undefined : group.platform as GroupPlatform"
          :subscription-type="(group.subscription_type || 'standard') as SubscriptionType"
          :rate-multiplier="group.rate_multiplier"
          :user-rate-multiplier="group.user_rate_multiplier ?? null"
          :peak-rate-enabled="group.peak_rate_enabled"
          :peak-start="group.peak_start"
          :peak-end="group.peak_end"
          :peak-rate-multiplier="group.peak_rate_multiplier"
          always-show-rate
        />
        <span
          v-if="group.is_exclusive"
          class="inline-flex items-center gap-1 rounded-md bg-purple-50 px-2 py-0.5 text-xs font-medium text-purple-600 dark:bg-purple-900/20 dark:text-purple-400"
        >
          <Icon name="shield" size="xs" class="h-3 w-3" />
          {{ t('modelPlaza.badges.exclusive') }}
        </span>
        <span
          v-if="group.subscription_type === 'subscription'"
          class="inline-flex items-center rounded-md bg-violet-50 px-2 py-0.5 text-xs font-medium text-violet-600 dark:bg-violet-900/20 dark:text-violet-400"
        >
          {{ t('modelPlaza.badges.subscription') }}
        </span>
      </div>
      <p v-if="group.description" class="mt-2 text-sm text-gray-500 dark:text-dark-400">
        {{ group.description }}
      </p>
      <p
        v-if="peakNote"
        class="mt-1.5 inline-flex items-center gap-1 text-xs text-amber-600 dark:text-amber-400"
      >
        <Icon name="clock" size="xs" class="h-3 w-3" />
        {{ peakNote }}
      </p>
      <p
        v-if="longContextNote"
        class="mt-1.5 flex items-center gap-1 text-xs text-gray-500 dark:text-dark-400"
      >
        <Icon name="infoCircle" size="xs" class="h-3 w-3" />
        {{ longContextNote }}
      </p>
    </header>


    <div v-if="priceSections.length">
      <div
        v-for="section in priceSections"
        :key="section.kind"
        class="border-t border-gray-100 first:border-t-0 dark:border-dark-700/60"
      >
        <div v-if="section.title" class="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-5 py-3">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ section.title }}</h3>
          <p v-if="section.unit === 'image'" class="text-xs text-gray-600 dark:text-dark-300">
            {{ t(group.image_rate_independent ? 'modelPlaza.detail.fixedImageRate' : 'modelPlaza.detail.groupImageRate') }}
          </p>
          <p v-else-if="section.unit === 'second'" class="text-xs text-gray-600 dark:text-dark-300">
            {{ t('modelPlaza.detail.videoDurationRate') }}
          </p>
        </div>
        <PlazaModelPricingTable
          :models="section.models"
          :unit="section.unit"
          :platform="group.platform"
          :rate-multiplier="group.rate_multiplier"
          :user-rate-multiplier="group.user_rate_multiplier ?? null"
          :image-rate-independent="group.image_rate_independent"
          :image-rate-multiplier="group.image_rate_multiplier"
          :video-rate-independent="group.video_rate_independent"
          :video-rate-multiplier="group.video_rate_multiplier"
          :peak-window="peakWindow"
          :peak-rate-multiplier="group.peak_rate_multiplier"
          :display-currency="group.display_currency"
          :display-usd-rate="group.display_usd_rate"
        />
      </div>
    </div>
    <p v-else class="px-5 py-4 text-center text-sm text-gray-400 dark:text-dark-500">
      {{ t('modelPlaza.detail.noModels') }}
    </p>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import { BILLING_MODE_IMAGE, BILLING_MODE_PER_REQUEST } from '@/constants/channel'
import PlazaModelPricingTable from './PlazaModelPricingTable.vue'
import type { ModelPlazaGroup, PlazaCompactUnit, PlazaModel } from '@/api/modelPlaza'
import type { GroupPlatform, SubscriptionType } from '@/types'
import { platformBorderStrongClass } from '@/utils/platformColors'
import { hasPeakRate, formatPeakRateWindow, serverTimezoneLabel } from '@/utils/peak-rate'
import { useAppStore } from '@/stores/app'

const props = defineProps<{
  group: ModelPlazaGroup
}>()

const { t } = useI18n()
const appStore = useAppStore()
const isMuse = computed(() => props.group.catalog_provider === 'muse')

interface PriceSection {
  kind: string
  models: PlazaModel[]
  title: string
  unit?: PlazaCompactUnit
}

const priceSections = computed<PriceSection[]>(() => {
  if (isMuse.value) {
    const text = props.group.models.filter(model => !model.unit_pricing)
    const perRequest = (model: PlazaModel) => !model.pricing || model.pricing.billing_mode === BILLING_MODE_PER_REQUEST
    const textTitle = t('modelPlaza.detail.museTextPrices')
    const sections: PriceSection[] = [
      { kind: 'text', models: text.filter(model => !perRequest(model)), title: textTitle },
      { kind: 'request', models: text.filter(perRequest), title: textTitle, unit: 'request' },
      { kind: 'image', models: props.group.models.filter(model => model.unit_pricing?.unit === 'image'), title: t('modelPlaza.detail.museImagePrices'), unit: 'image' },
      { kind: 'video', models: props.group.models.filter(model => model.unit_pricing?.unit === 'second'), title: t('modelPlaza.detail.museVideoPrices'), unit: 'second' },
    ]
    return sections.filter(section => section.models.length > 0)
  }
  const other: PlazaModel[] = []
  const perImage: PlazaModel[] = []
  const otherImage: PlazaModel[] = []
  for (const model of props.group.models) {
    const isGPTImage = model.platform === 'openai' && (model.gpt_image ?? model.name.toLowerCase().startsWith('gpt-image-'))
    if (!isGPTImage) {
      other.push(model)
      continue
    }
    const mode = model.pricing?.billing_mode
    if (mode === BILLING_MODE_IMAGE || mode === BILLING_MODE_PER_REQUEST) {
      perImage.push(model)
    } else {
      otherImage.push(model)
    }
  }
  const sections: PriceSection[] = [
    { kind: 'text', models: other, title: perImage.length || otherImage.length ? t('modelPlaza.detail.gptTextModels') : '' },
    { kind: 'image', models: perImage, title: t('modelPlaza.detail.gptImagePrices'), unit: 'image' },
    { kind: 'otherImage', models: otherImage, title: t('modelPlaza.detail.gptImageOtherPricing') }
  ]
  return sections.filter(section => section.models.length > 0)
})

/** 高峰窗口描述(含倍率与服务器时区标注);分组未启用高峰为空串。 */
const peakWindow = computed(() => {
  if (!hasPeakRate(props.group)) return ''
  return formatPeakRateWindow(
    props.group,
    serverTimezoneLabel(appStore.cachedPublicSettings?.server_utc_offset)
  )
})

const peakNote = computed(() => {
  if (!peakWindow.value) return ''
  return t('modelPlaza.detail.peakNote', {
    window: peakWindow.value,
    multiplier: props.group.peak_rate_multiplier
  })
})

/**
 * 分组关闭了长上下文阶梯、但组内有模型官方带阶梯时提示:实付列只展示基础档,
 * 官方阶梯仅供参考。字段缺失(旧后端)不提示。
 */
const longContextNote = computed(() => {
  if (props.group.long_context_pricing_enabled !== false) return ''
  const hasOfficialLadder = props.group.models.some(
    (m) => (m.official_pricing?.intervals?.length ?? 0) > 1
  )
  return hasOfficialLadder ? t('modelPlaza.detail.longContextDisabledNote') : ''
})
</script>
