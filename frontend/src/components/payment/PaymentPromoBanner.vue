<template>
  <section v-if="visible" class="mb-6 overflow-hidden rounded-2xl border border-primary-200/70 bg-gradient-to-br from-primary-50 via-white to-sky-50 shadow-sm dark:border-primary-900/50 dark:from-primary-950/50 dark:via-dark-900 dark:to-sky-950/30">
    <a
      v-if="safeLink"
      :href="safeLink"
      :target="isExternalLink ? '_blank' : undefined"
      :rel="isExternalLink ? 'noopener noreferrer' : undefined"
      class="block transition-colors hover:bg-white/50 dark:hover:bg-white/[0.03]"
    >
      <BannerContent />
    </a>
    <div v-else>
      <BannerContent />
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, h } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { sanitizeUrl } from '@/utils/url'
import type { PaymentBanner } from '@/types/payment'

const props = defineProps<{
  banner?: PaymentBanner | null
}>()

const { t } = useI18n()

const safeLink = computed(() => sanitizeUrl(props.banner?.link_url || '', { allowRelative: true }))
const safeImage = computed(() => sanitizeBannerImageUrl(props.banner?.image_url || ''))
// A promotion is a click-through surface. If a malformed/legacy payload loses
// its destination, fail closed instead of showing an apparently actionable
// banner with no action.
const visible = computed(() => Boolean(props.banner?.enabled && props.banner.title?.trim() && safeLink.value))
const isExternalLink = computed(() => /^https?:\/\//i.test(safeLink.value))

function sanitizeBannerImageUrl(value: string): string {
  const trimmed = value.trim()
  if (!trimmed) return ''
  if (/^data:/i.test(trimmed)) {
    return isSafeRasterDataImageUrl(trimmed) ? trimmed : ''
  }
  return sanitizeUrl(trimmed, { allowRelative: true })
}

function isSafeRasterDataImageUrl(value: string): boolean {
  const match = value.match(/^data:([^,]+),([A-Za-z0-9+/]+={0,2})$/i)
  if (!match) return false
  const header = match[1]
  const parts = header.split(';')
  if (parts.length < 2 || parts[parts.length - 1]?.trim().toLowerCase() !== 'base64') return false
  const mediaType = parts[0].trim().toLowerCase()
  return mediaType.startsWith('image/') && !mediaType.includes('svg') && !mediaType.endsWith('+xml')
}

// Keeping the visual treatment in one component prevents the admin content
// fields from becoming an accidental theme API.
const BannerContent = () => h('div', { class: 'flex flex-col gap-4 px-5 py-4 sm:flex-row sm:items-center sm:px-6 sm:py-5' }, [
  safeImage.value
    ? h('img', {
        src: safeImage.value,
        alt: '',
        class: 'h-20 w-full shrink-0 rounded-xl object-cover sm:h-20 sm:w-32',
      })
    : h('div', { class: 'flex h-12 w-12 shrink-0 items-center justify-center rounded-xl bg-primary-600 text-white shadow-sm dark:bg-primary-500' }, [
        h(Icon, { name: 'sparkles', size: 'md', strokeWidth: 2 }),
      ]),
  h('div', { class: 'min-w-0 flex-1' }, [
    h('p', { class: 'text-base font-semibold text-gray-900 dark:text-white' }, props.banner?.title),
    props.banner?.description
      ? h('p', { class: 'mt-1 text-sm leading-relaxed text-gray-600 dark:text-dark-300' }, props.banner.description)
      : null,
  ]),
  safeLink.value
    ? h('span', { class: 'inline-flex shrink-0 items-center gap-1.5 rounded-lg bg-primary-600 px-3.5 py-2 text-sm font-medium text-white shadow-sm dark:bg-primary-500' }, [
        props.banner?.button_text?.trim() || t('payment.viewOffer'),
        h(Icon, { name: 'arrowRight', size: 'sm', strokeWidth: 2 }),
      ])
    : null,
])
</script>
