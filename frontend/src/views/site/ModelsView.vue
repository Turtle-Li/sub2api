<template>
  <SiteLayout>
    <section class="mx-auto max-w-[1180px] px-5 pb-10 pt-20 sm:px-8 sm:pt-24">
      <h1 class="display-xl max-w-[14ch]">{{ t('site.models.title') }}</h1>
      <p class="lede mt-7 max-w-[50ch]">{{ t('site.models.lede') }}</p>
      <p v-if="isLive" class="meta meta-cn mt-8">{{ t('site.models.live') }}</p>
    </section>

    <!-- plaza 不可用时的降级说明必须显眼，不能让人以为这就是全量目录 -->
    <section v-if="!loading && !isLive" class="hairline-t">
      <div class="mx-auto max-w-[1180px] px-5 py-6 sm:px-8">
        <p class="meta-strong">{{ t('site.models.fallbackTitle') }}</p>
        <p class="note is-warn mt-3 max-w-[62ch]">{{ t('site.models.fallbackBody') }}</p>
      </div>
    </section>

    <section class="hairline-t">
      <div class="mx-auto max-w-[1180px] px-5 sm:px-8">
        <div class="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-2 py-5">
          <h2 class="meta-strong">{{ t('site.models.tableLabel') }}</h2>
          <p v-if="!loading" class="meta">
            {{ t('site.models.count', { shown: rows.length, total: allRows.length }) }}
          </p>
        </div>

        <!-- 分组切换：加载完成前不渲染，否则会先闪一遍降级列表的分组 -->
        <div
          v-if="!loading && groupNames.length > 1"
          class="hairline-t flex flex-wrap items-center gap-x-5 gap-y-2 py-3.5"
        >
          <span class="meta shrink-0">{{ t('site.models.groupLabel') }}</span>
          <button
            type="button"
            class="filterlink"
            :class="{ 'is-on': activeGroup === ALL }"
            :aria-pressed="activeGroup === ALL"
            @click="activeGroup = ALL"
          >
            {{ t('site.models.allGroups') }}
          </button>
          <button
            v-for="name in groupNames"
            :key="name"
            type="button"
            class="filterlink"
            :class="{ 'is-on': activeGroup === name }"
            :aria-pressed="activeGroup === name"
            @click="activeGroup = name"
          >
            {{ name }}
          </button>
        </div>

        <div class="modelrow drow drow-head meta hairline-t py-2.5" aria-hidden="true">
          <span>{{ t('site.models.cols.model') }}</span>
          <span>{{ t('site.models.cols.group') }}</span>
          <span class="justify-self-end">{{ t('site.models.cols.multiplier') }}</span>
          <span class="justify-self-end">{{ t('site.models.cols.input') }}</span>
          <span class="justify-self-end">{{ t('site.models.cols.output') }}</span>
        </div>

        <p v-if="loading" class="meta hairline-t py-10">{{ t('site.models.loading') }}</p>
        <p v-else-if="!rows.length" class="meta hairline-t py-10">{{ t('site.models.empty') }}</p>

        <div v-else class="hairline-t">
          <div v-for="row in rows" :key="row.key" class="modelrow drow hairline-b py-3.5">
            <span class="dcell-id">{{ row.name }}</span>
            <span class="dcell-text">
              {{ row.group }}
              <span v-if="row.exclusive" class="meta ml-2">{{ t('site.models.exclusive') }}</span>
            </span>
            <span class="dcell-num justify-self-end">
              <template v-if="row.multiplier !== null">
                {{ row.multiplier }}{{ t('site.models.unit.multiplier') }}
              </template>
              <span v-else class="dcell-meta">—</span>
            </span>
            <span class="dcell-num justify-self-end">
              <template v-if="row.input">{{ row.input }}</template>
              <span v-else class="dcell-meta">—</span>
            </span>
            <span class="dcell-num justify-self-end">
              <template v-if="row.output">{{ row.output }}</template>
              <span v-else class="dcell-meta">—</span>
            </span>
          </div>
        </div>

        <p class="meta meta-cn flex flex-wrap items-center justify-between gap-x-6 gap-y-2 py-4">
          <span>{{ isLive ? t('site.models.unit.perMillion') : t('site.models.pricingUnavailable') }}</span>
          <router-link v-if="showModelPlazaEntry" to="/model-plaza" class="textlink shrink-0">
            {{ t('site.models.viewPlaza') }} →
          </router-link>
        </p>
      </div>
    </section>

    <!-- 倍率说明：窄栏定义列表，与上面的宽表形成节奏对比 -->
    <section class="hairline-t" :style="{ background: 'var(--surface)' }">
      <div class="mx-auto max-w-[52rem] px-5 py-16 sm:px-8 sm:py-20">
        <p class="index">{{ t('site.models.multiplier.index') }}</p>
        <h2 class="display-md mt-4 max-w-[20ch]">{{ t('site.models.multiplier.title') }}</h2>
        <p class="lede-sm mt-5">{{ t('site.models.multiplier.lede') }}</p>

        <dl class="hairline-t mt-10">
          <div v-for="item in multiplierItems" :key="item.key" class="defrow hairline-b">
            <dt class="meta-strong">{{ item.term }}</dt>
            <dd class="body-sm">{{ item.desc }}</dd>
          </div>
        </dl>

        <p class="note mt-8">{{ t('site.models.multiplier.note') }}</p>
      </div>
    </section>
  </SiteLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import SiteLayout from '@/components/site/SiteLayout.vue'
import { useSite } from '@/components/site/useSite'
import { getModelPlaza, type ModelPlazaResponse } from '@/api/modelPlaza'

const { t, showModelPlazaEntry } = useSite()

const ALL = '__all__'
const activeGroup = ref<string>(ALL)

const data = ref<ModelPlazaResponse | null>(null)
const loading = ref(true)

/**
 * 广场关闭时后端返回 404，require_auth 开启时匿名也拿不到。
 * 两种情况都走同一条降级路径：显示代表性模型并明确标注，不白屏。
 */
const isLive = computed(() => !!data.value?.groups?.length)

/**
 * 目录是这页的加分项，不是前提。apiClient 默认超时 30 秒，后端一慢
 * 就会让公开页干等半分钟，所以这里单独卡一个短上限，超时直接降级。
 */
const PLAZA_TIMEOUT_MS = 6000

onMounted(async () => {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), PLAZA_TIMEOUT_MS)
  try {
    data.value = await getModelPlaza({ signal: controller.signal })
  } catch {
    data.value = null
  } finally {
    clearTimeout(timer)
    loading.value = false
  }
})

/** 代表性模型，仅在真实目录不可用时使用；不含价格。 */
const FALLBACK_MODELS = [
  { name: 'claude-sonnet-4-5', group: 'Anthropic' },
  { name: 'claude-opus-4-1', group: 'Anthropic' },
  { name: 'gpt-4o', group: 'OpenAI' },
  { name: 'gemini-2.5-pro', group: 'Google' },
  { name: 'deepseek-v3', group: 'DeepSeek' },
  { name: 'kimi-k2', group: 'Moonshot' },
  { name: 'glm-4.6', group: 'Zhipu' },
  { name: 'qwen3-max', group: 'Alibaba' },
]

/** 目录价是 USD/token，展示成每百万 tokens 才有可读性。 */
function perMillion(price: number | null | undefined): string | null {
  if (price === null || price === undefined) return null
  const value = price * 1_000_000
  if (!Number.isFinite(value) || value <= 0) return null
  const digits = value >= 100 ? 0 : value >= 1 ? 2 : 3
  return `$${value.toFixed(digits)}`
}

interface ModelRow {
  key: string
  name: string
  group: string
  exclusive: boolean
  multiplier: number | null
  input: string | null
  output: string | null
}

const allRows = computed<ModelRow[]>(() => {
  const groups = data.value?.groups
  if (!groups?.length) {
    return FALLBACK_MODELS.map((model) => ({
      key: `${model.group}/${model.name}`,
      name: model.name,
      group: model.group,
      exclusive: false,
      multiplier: null,
      input: null,
      output: null,
    }))
  }

  return groups.flatMap((group) =>
    (group.models ?? []).map((model) => ({
      key: `${group.id}/${model.name}`,
      name: model.name,
      group: group.name,
      exclusive: group.is_exclusive,
      // 生效倍率 = 专属倍率 ?? 分组倍率（与计费口径一致）
      multiplier: group.user_rate_multiplier ?? group.rate_multiplier ?? null,
      input: perMillion(model.pricing?.input_price),
      output: perMillion(model.pricing?.output_price),
    })),
  )
})

const groupNames = computed(() => [...new Set(allRows.value.map((row) => row.group))])

const rows = computed(() =>
  activeGroup.value === ALL
    ? allRows.value
    : allRows.value.filter((row) => row.group === activeGroup.value),
)

const multiplierItems = computed(() =>
  (['group', 'user', 'peak', 'longContext', 'image'] as const).map((key) => ({
    key,
    term: t(`site.models.multiplier.items.${key}.term`),
    desc: t(`site.models.multiplier.items.${key}.desc`),
  })),
)
</script>

<style scoped>
.modelrow {
  grid-template-columns:
    minmax(0, 2.2fr) minmax(0, 1.2fr)
    minmax(0, 0.6fr) minmax(0, 0.7fr) minmax(0, 0.7fr);
}

/* 窄屏折成两行：模型 ID 占一整行，分组与三个数字排在第二行 */
@media (max-width: 767px) {
  .modelrow {
    grid-template-columns: minmax(0, 1fr) auto auto auto;
    gap: 0.3125rem 0.75rem;
  }
  .modelrow > :nth-child(1) {
    grid-column: 1 / 5;
  }
  .modelrow > :nth-child(2) {
    grid-column: 1;
  }
}
</style>
