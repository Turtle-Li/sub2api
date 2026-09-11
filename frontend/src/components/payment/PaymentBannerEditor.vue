<template>
  <section class="rounded-lg border border-primary-100 bg-primary-50/40 p-4 dark:border-primary-900/40 dark:bg-primary-950/20">
    <div class="flex items-start justify-between gap-4">
      <div>
        <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.settings.payment.bannerTitle') }}</h3>
        <p class="mt-1 text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t('admin.settings.payment.bannerHint') }}</p>
      </div>
      <div class="flex shrink-0 items-center gap-2">
        <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.payment.bannerEnabled') }}</span>
        <Toggle :model-value="value.enabled" @update:model-value="update({ enabled: $event })" />
      </div>
    </div>

    <div v-if="value.enabled" class="mt-4 space-y-3">
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div>
          <label class="input-label">{{ t('admin.settings.payment.bannerTitleField') }} <span class="text-red-500">*</span></label>
          <input :value="value.title" type="text" maxlength="120" class="input" :placeholder="t('admin.settings.payment.bannerTitlePlaceholder')" @input="update({ title: inputValue($event) })" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.settings.payment.bannerButtonText') }}</label>
          <input :value="value.button_text" type="text" maxlength="40" class="input" :placeholder="t('admin.settings.payment.bannerButtonPlaceholder')" @input="update({ button_text: inputValue($event) })" />
        </div>
      </div>

      <div>
        <label class="input-label">{{ t('admin.settings.payment.bannerDescription') }}</label>
        <textarea :value="value.description" rows="2" maxlength="240" class="input" :placeholder="t('admin.settings.payment.bannerDescriptionPlaceholder')" @input="update({ description: inputValue($event) })"></textarea>
      </div>

      <div>
        <label class="input-label">{{ t('admin.settings.payment.bannerLink') }} <span class="text-red-500">*</span></label>
        <input :value="value.link_url" type="text" class="input" :placeholder="t('admin.settings.payment.bannerLinkPlaceholder')" @input="update({ link_url: inputValue($event) })" />
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.payment.bannerLinkHint') }}</p>
      </div>

      <div>
        <label class="input-label">{{ t('admin.settings.payment.bannerImage') }}</label>
        <ImageUpload
          :model-value="value.image_url || ''"
          :upload-label="t('admin.settings.site.uploadImage')"
          :remove-label="t('admin.settings.site.remove')"
          :hint="t('admin.settings.payment.bannerImagePlaceholder')"
          :max-size="300 * 1024"
          @update:model-value="updateImage"
        />
        <p v-if="imageError" role="alert" class="mt-1 text-xs text-red-500">{{ t('admin.settings.payment.bannerImageFormatError') }}</p>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.payment.bannerImageHint') }}</p>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ImageUpload from '@/components/common/ImageUpload.vue'
import Toggle from '@/components/common/Toggle.vue'
import type { PaymentBanner } from '@/types/payment'

const props = defineProps<{
  modelValue: PaymentBanner
}>()

const emit = defineEmits<{
  'update:modelValue': [value: PaymentBanner]
}>()

const { t } = useI18n()

const value = computed<PaymentBanner>(() => ({
  ...props.modelValue,
  enabled: props.modelValue.enabled ?? false,
  title: props.modelValue.title ?? '',
  description: props.modelValue.description ?? '',
  image_url: props.modelValue.image_url ?? '',
  link_url: props.modelValue.link_url ?? '',
  button_text: props.modelValue.button_text ?? '',
}))

const imageError = ref(false)

function updateImage(imageURL: string) {
  const mediaType = imageURL.match(/^data:([^;,]+)/i)?.[1]?.toLowerCase()
  imageError.value = Boolean(mediaType && (!mediaType.startsWith('image/') || mediaType.includes('svg') || mediaType.endsWith('+xml')))
  if (!imageError.value) update({ image_url: imageURL })
}

function update(patch: Partial<PaymentBanner>) {
  emit('update:modelValue', { ...value.value, ...patch })
}

function inputValue(event: Event): string {
  return (event.target as HTMLInputElement | HTMLTextAreaElement).value
}
</script>
