<template>
  <SiteLayout>
    <!-- 构图：窄栏单列。这页是读的，不是查的，所以行宽收到 46rem，
         和 /models 的宽表、/changelog 的时间轴明确区分。 -->
    <section class="mx-auto max-w-[46rem] px-5 pb-12 pt-20 sm:px-8 sm:pt-24">
      <h1 class="display-xl max-w-[14ch]">{{ t('site.why.title') }}</h1>
      <p class="lede mt-7">{{ t('site.why.lede') }}</p>
    </section>

    <section class="mx-auto max-w-[46rem] px-5 pb-16 sm:px-8">
      <ol class="hairline-t">
        <li v-for="(item, i) in items" :key="item.key" class="hairline-b py-8">
          <div class="flex items-baseline gap-4">
            <span class="meta shrink-0 tabular-nums">{{ String(i + 1).padStart(2, '0') }}</span>
            <h2 class="display-sm min-w-0">{{ item.term }}</h2>
          </div>
          <p class="body-sm ml-[2.75rem] mt-3">{{ item.desc }}</p>
          <p v-if="item.fact" class="note is-warn ml-[2.75rem] mt-4">
            <span class="placeholder">{{ item.fact }}</span>
          </p>
        </li>
      </ol>
    </section>

    <!-- 结尾不做 CTA 卡片，做一段落款式的联系信息 -->
    <section class="mx-auto max-w-[46rem] px-5 pb-24 sm:px-8">
      <h2 class="display-md">{{ t('site.why.contact.title') }}</h2>
      <p class="lede-sm mt-4">{{ t('site.why.contact.body') }}</p>
      <p class="note is-warn mt-5">
        <span class="placeholder">{{ t('site.why.contact.placeholder') }}</span>
      </p>
      <div class="mt-9 flex flex-wrap gap-x-8 gap-y-3">
        <router-link :to="primaryDestination" class="btn-outline">
          {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
        </router-link>
        <router-link to="/models" class="btn-outline">{{ t('site.nav.models') }}</router-link>
      </div>
    </section>
  </SiteLayout>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import SiteLayout from '@/components/site/SiteLayout.vue'
import { useSite } from '@/components/site/useSite'

const { t, isAuthenticated, primaryDestination } = useSite()

const ITEM_KEYS = ['pool', 'refund', 'invoice', 'support', 'billing', 'stability'] as const

const items = computed(() =>
  ITEM_KEYS.map((key) => ({
    key,
    term: t(`site.why.items.${key}.term`),
    desc: t(`site.why.items.${key}.desc`),
    // 空字符串表示这条不需要你补充事实，直接不渲染占位块
    fact: t(`site.why.items.${key}.fact`),
  })),
)
</script>
