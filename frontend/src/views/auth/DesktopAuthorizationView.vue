<template>
  <AuthLayout>
    <div class="space-y-6">
      <div class="text-center">
        <div class="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-2xl bg-primary-100 text-primary-600 dark:bg-primary-500/15 dark:text-primary-300">
          <Icon name="shield" size="lg" />
        </div>
        <h2 class="text-2xl font-bold text-gray-900 dark:text-white">授权 TT Switch</h2>
        <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">
          确认后，当前浏览器账号将仅登录这一次 TT Switch 请求。
        </p>
      </div>

      <div
        v-if="!validUserCode"
        class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-300"
        role="alert"
      >
        授权码无效或已丢失。请返回 TT Switch 重新开始。
      </div>

      <template v-else-if="approved">
        <div class="rounded-xl border border-emerald-200 bg-emerald-50 p-5 text-center dark:border-emerald-900/60 dark:bg-emerald-950/30">
          <Icon name="checkCircle" size="lg" class="mx-auto text-emerald-600 dark:text-emerald-300" />
          <p class="mt-3 font-medium text-emerald-800 dark:text-emerald-200">已授权 TT Switch</p>
          <p class="mt-1 text-sm text-emerald-700 dark:text-emerald-300">可以回到 TT Switch，它会自动完成登录。</p>
        </div>
      </template>

      <template v-else>
        <div class="rounded-xl border border-gray-200 bg-gray-50 p-4 text-center dark:border-dark-700 dark:bg-dark-800/60">
          <p class="text-xs font-medium uppercase tracking-[0.16em] text-gray-500 dark:text-dark-400">TT Switch 授权码</p>
          <p class="mt-2 font-mono text-2xl font-semibold tracking-[0.2em] text-gray-900 dark:text-white">{{ normalizedUserCode }}</p>
          <p class="mt-3 text-sm text-gray-600 dark:text-dark-300">请确认它与 TT Switch 显示的代码完全一致。</p>
        </div>

        <div class="rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-200">
          此操作会让当前账号登录本机 TT Switch。若不是你发起的请求，请不要继续。
        </div>

        <p v-if="error" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300" role="alert">
          {{ error }}
        </p>

        <button type="button" class="btn btn-primary w-full" :disabled="submitting" @click="approve">
          <svg v-if="submitting" class="-ml-1 mr-2 h-4 w-4 animate-spin" fill="none" viewBox="0 0 24 24" aria-hidden="true">
            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
            <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12H4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
          </svg>
          <Icon v-else name="shield" size="md" class="mr-2" />
          {{ submitting ? '正在授权…' : '确认授权' }}
        </button>
      </template>
    </div>
  </AuthLayout>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { AuthLayout } from '@/components/layout'
import Icon from '@/components/icons/Icon.vue'
import { approveDesktopAuthorization } from '@/api/auth'
import { extractApiErrorMessage } from '@/utils/apiError'

const route = useRoute()
const submitting = ref(false)
const approved = ref(false)
const error = ref('')

const normalizedUserCode = computed(() => {
  const value = typeof route.query.user_code === 'string' ? route.query.user_code : ''
  return value.trim().toUpperCase()
})
const validUserCode = computed(() => /^[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}$/.test(normalizedUserCode.value))

async function approve(): Promise<void> {
  if (!validUserCode.value || submitting.value) return
  submitting.value = true
  error.value = ''
  try {
    const result = await approveDesktopAuthorization(normalizedUserCode.value)
    if (result.status === 'approved') {
      approved.value = true
      return
    }
    error.value = '本次授权已过期，请返回 TT Switch 重新开始。'
  } catch (caught) {
    error.value = extractApiErrorMessage(caught, '授权未完成，请重试。')
  } finally {
    submitting.value = false
  }
}
</script>
