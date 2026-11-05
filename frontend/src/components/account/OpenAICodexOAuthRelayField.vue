<template>
  <div class="border-t border-gray-200 pt-4 dark:border-dark-600">
    <div class="flex items-center justify-between gap-4">
      <div class="min-w-0">
        <label :for="toggleId" class="input-label mb-0">{{ t('admin.accounts.openai.codexOAuthRelay') }}</label>
        <p :id="descriptionId" class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.openai.codexOAuthRelayDesc') }}
        </p>
      </div>
      <Toggle
        :id="toggleId"
        data-testid="openai-codex-oauth-relay-toggle"
        :model-value="modelValue"
        :aria-label="t('admin.accounts.openai.codexOAuthRelay')"
        :aria-describedby="descriptionId"
        @update:model-value="emit('update:modelValue', $event)"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { useId } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'

defineProps<{ readonly modelValue: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
const { t } = useI18n()
const toggleId = useId()
const descriptionId = `${toggleId}-description`
</script>
