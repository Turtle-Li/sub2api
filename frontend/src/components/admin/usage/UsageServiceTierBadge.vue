<template>
  <span
    v-if="isFast"
    data-testid="usage-fast-badge"
    class="inline-flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-primary-100 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
    :title="t('usage.fastBadgeTooltip')"
    role="img"
    :aria-label="t('usage.serviceTierPriority')"
  >
    <Icon name="bolt" size="xs" :stroke-width="2.2" aria-hidden="true" />
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { normalizeUsageServiceTier } from '@/utils/usageServiceTier'

const props = defineProps<{ readonly serviceTier?: string | null }>()
const { t } = useI18n()
const isFast = computed(() => normalizeUsageServiceTier(props.serviceTier) === 'priority')
</script>
