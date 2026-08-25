<template>
  <SiteLayout>
    <section class="mx-auto max-w-[1180px] px-5 pb-10 pt-20 sm:px-8 sm:pt-24">
      <h1 class="display-xl max-w-[14ch]">{{ t('site.docs.title') }}</h1>
      <p class="lede mt-6 max-w-[44ch]">{{ t('site.docs.lede') }}</p>
    </section>

    <!-- 构图：左侧固定目录 + 右侧正文。目录项是真实路由跳转，
         URL 会变、可前进后退、可直接分享。 -->
    <div class="hairline-t">
      <div class="mx-auto grid max-w-[1180px] gap-10 px-5 py-12 sm:px-8 lg:grid-cols-[minmax(0,13rem)_minmax(0,1fr)] lg:gap-16">
        <nav class="lg:sticky lg:top-[5.5rem] lg:self-start" :aria-label="t('site.docs.sectionsLabel')">
          <p class="meta mb-4">{{ t('site.docs.sectionsLabel') }}</p>
          <ul>
            <li v-for="section in SECTIONS" :key="section">
              <router-link
                :to="section === 'quickstart' ? '/docs' : `/docs/${section}`"
                class="sidelink"
                :class="{ 'is-current': section === current }"
              >
                {{ t(`site.docs.sections.${section}`) }}
              </router-link>
            </li>
          </ul>

          <template v-if="docUrl">
            <p class="meta mb-3 mt-10">{{ t('site.docs.externalDocs') }}</p>
            <a :href="docUrl" target="_blank" rel="noopener noreferrer" class="sidelink">
              {{ t('site.footer.links.externalDocs') }} ↗
            </a>
          </template>
        </nav>

        <article class="min-w-0">
          <!-- 快速开始 -->
          <template v-if="current === 'quickstart'">
            <h2 class="display-md">{{ t('site.docs.quickstart.title') }}</h2>
            <p class="lede-sm mt-4 max-w-[58ch]">{{ t('site.docs.quickstart.lede') }}</p>

            <ol class="hairline-t mt-10">
              <li v-for="(step, i) in quickstartSteps" :key="step.key" class="hairline-b py-6">
                <div class="flex items-baseline gap-4">
                  <span class="meta shrink-0 tabular-nums">{{ String(i + 1).padStart(2, '0') }}</span>
                  <div class="min-w-0">
                    <h3 class="display-sm">{{ step.title }}</h3>
                    <p class="body-sm mt-2 max-w-[58ch]">{{ step.body }}</p>
                  </div>
                </div>
              </li>
            </ol>

            <div class="mt-10">
              <div class="flex flex-wrap items-center justify-between gap-x-6 gap-y-3">
                <div class="flex min-w-0 items-baseline gap-3">
                  <span class="verb">POST</span>
                  <span class="truncate font-mono text-[13px]">/v1/messages</span>
                </div>
                <div class="flex flex-wrap items-center gap-x-5 gap-y-2">
                  <button
                    v-for="tab in CODE_TABS"
                    :key="tab"
                    type="button"
                    class="filterlink"
                    :class="{ 'is-on': activeTab === tab }"
                    :aria-pressed="activeTab === tab"
                    @click="activeTab = tab"
                  >
                    {{ TAB_LABELS[tab] }}
                  </button>
                  <button
                    type="button"
                    class="btn-ghost"
                    @click="copyToClipboard(snippet, t('site.common.copied'))"
                  >
                    <Icon :name="copied ? 'check' : 'copy'" size="sm" :stroke-width="2" />
                    <span>{{ copied ? t('site.common.copied') : t('site.common.copy') }}</span>
                  </button>
                </div>
              </div>
              <pre class="snippet hairline-t mt-5 pt-5">{{ snippet }}</pre>
            </div>

            <h3 class="display-sm mt-14">{{ t('site.docs.quickstart.verify.title') }}</h3>
            <p class="body-sm mt-3 max-w-[58ch]">{{ t('site.docs.quickstart.verify.body') }}</p>
            <p class="mt-4">
              <router-link to="/key-usage" class="textlink">
                {{ t('site.footer.links.keyUsage') }} →
              </router-link>
            </p>
          </template>

          <!-- 协议与端点 -->
          <template v-else-if="current === 'protocols'">
            <h2 class="display-md">{{ t('site.docs.protocols.title') }}</h2>
            <p class="lede-sm mt-4 max-w-[58ch]">{{ t('site.docs.protocols.lede') }}</p>

            <div v-for="group in ENDPOINT_GROUPS" :key="group.key" class="mt-12">
              <h3 class="meta-strong">{{ t(`site.docs.protocols.groups.${group.key}`) }}</h3>
              <div class="hairline-t mt-3">
                <div
                  v-for="endpoint in group.endpoints"
                  :key="endpoint.path + endpoint.method"
                  class="endpointrow drow hairline-b py-3"
                >
                  <span class="verb">{{ endpoint.method }}</span>
                  <span class="dcell-id">{{ endpoint.path }}</span>
                </div>
              </div>
            </div>

            <h3 class="display-sm mt-14">{{ t('site.docs.protocols.pickTitle') }}</h3>
            <p class="body-sm mt-3 max-w-[58ch]">{{ t('site.docs.protocols.pickBody') }}</p>
          </template>

          <!-- 客户端配置 -->
          <template v-else-if="current === 'clients'">
            <h2 class="display-md">{{ t('site.docs.clients.title') }}</h2>
            <p class="lede-sm mt-4 max-w-[58ch]">{{ t('site.docs.clients.lede') }}</p>

            <div class="hairline-t mt-10">
              <section v-for="client in clientItems" :key="client.key" class="hairline-b py-7">
                <h3 class="display-sm">{{ client.title }}</h3>
                <p class="body-sm mt-2 max-w-[58ch]">{{ client.body }}</p>
                <pre v-if="client.snippet" class="snippet mt-4">{{ client.snippet }}</pre>
              </section>
            </div>
          </template>

          <!-- 图像 · 视频 · 语音 -->
          <template v-else-if="current === 'media'">
            <h2 class="display-md">{{ t('site.docs.media.title') }}</h2>
            <p class="lede-sm mt-4 max-w-[58ch]">{{ t('site.docs.media.lede') }}</p>

            <h3 class="display-sm mt-10">{{ t('site.docs.media.asyncTitle') }}</h3>
            <p class="body-sm mt-3 max-w-[58ch]">{{ t('site.docs.media.asyncBody') }}</p>

            <div v-for="group in mediaGroups" :key="group.key" class="mt-12">
              <h3 class="meta-strong">{{ t(`site.docs.protocols.groups.${group.key}`) }}</h3>
              <div class="hairline-t mt-3">
                <div
                  v-for="endpoint in group.endpoints"
                  :key="endpoint.path + endpoint.method"
                  class="endpointrow drow hairline-b py-3"
                >
                  <span class="verb">{{ endpoint.method }}</span>
                  <span class="dcell-id">{{ endpoint.path }}</span>
                </div>
              </div>
            </div>
          </template>

          <!-- 错误与限流 -->
          <template v-else>
            <h2 class="display-md">{{ t('site.docs.errors.title') }}</h2>
            <p class="lede-sm mt-4 max-w-[58ch]">{{ t('site.docs.errors.lede') }}</p>

            <h3 class="display-sm mt-10">{{ t('site.docs.errors.limitsTitle') }}</h3>
            <dl class="hairline-t mt-4">
              <div v-for="limit in errorLimits" :key="limit.key" class="defrow hairline-b">
                <dt class="meta-strong">{{ limit.term }}</dt>
                <dd class="body-sm">{{ limit.desc }}</dd>
              </div>
            </dl>

            <h3 class="display-sm mt-12">{{ t('site.docs.errors.retryTitle') }}</h3>
            <p class="body-sm mt-3 max-w-[58ch]">{{ t('site.docs.errors.retryBody') }}</p>
          </template>
        </article>
      </div>
    </div>
  </SiteLayout>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import SiteLayout from '@/components/site/SiteLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { useSite } from '@/components/site/useSite'
import { useClipboard } from '@/composables/useClipboard'
import { ENDPOINT_GROUPS } from '@/data/siteEndpoints'

const route = useRoute()
const router = useRouter()
const { t, docUrl } = useSite()
const { copied, copyToClipboard } = useClipboard()

const SECTIONS = ['quickstart', 'protocols', 'clients', 'media', 'errors'] as const
type Section = (typeof SECTIONS)[number]

const current = computed<Section>(() => {
  const param = route.params.section
  const value = typeof param === 'string' ? param : ''
  return (SECTIONS as readonly string[]).includes(value) ? (value as Section) : 'quickstart'
})

// 未知的 section 直接回到 /docs，避免 URL 和内容对不上
watch(
  () => route.params.section,
  (param) => {
    const value = typeof param === 'string' ? param : ''
    if (value && !(SECTIONS as readonly string[]).includes(value)) {
      router.replace('/docs')
    }
  },
  { immediate: true },
)

// base URL 取当前站点，示例复制下来就能跑
const apiBase =
  typeof window !== 'undefined' ? window.location.origin : 'https://turtleroute.example.com'

const CODE_TABS = ['curl', 'python', 'node'] as const
type CodeTab = (typeof CODE_TABS)[number]
const TAB_LABELS: Record<CodeTab, string> = { curl: 'cURL', python: 'Python', node: 'Node.js' }
const activeTab = ref<CodeTab>('curl')

const snippet = computed(() => {
  if (activeTab.value === 'python') {
    return `import os
from anthropic import Anthropic

client = Anthropic(
    base_url="${apiBase}",
    api_key=os.environ["TURTLEROUTE_API_KEY"],
)
message = client.messages.create(
    model="claude-sonnet-4-5",
    max_tokens=1024,
    messages=[{"role": "user", "content": "ping"}],
)`
  }
  if (activeTab.value === 'node') {
    return `import Anthropic from '@anthropic-ai/sdk'

const client = new Anthropic({
  baseURL: '${apiBase}',
  apiKey: process.env.TURTLEROUTE_API_KEY,
})

const message = await client.messages.create({
  model: 'claude-sonnet-4-5',
  max_tokens: 1024,
  messages: [{ role: 'user', content: 'ping' }],
})`
  }
  return `curl ${apiBase}/v1/messages \\
  -H "Authorization: Bearer $TURTLEROUTE_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "claude-sonnet-4-5",
    "max_tokens": 1024,
    "messages": [{ "role": "user", "content": "ping" }]
  }'`
})

const quickstartSteps = computed(() =>
  (['account', 'key', 'baseUrl', 'send'] as const).map((key) => ({
    key,
    title: t(`site.docs.quickstart.steps.${key}.title`),
    body: t(`site.docs.quickstart.steps.${key}.body`),
  })),
)

const CLIENT_SNIPPETS: Record<string, string> = {
  claudeCode: `export ANTHROPIC_BASE_URL="${apiBase}"
export ANTHROPIC_AUTH_TOKEN="$TURTLEROUTE_API_KEY"`,
  codex: `export OPENAI_BASE_URL="${apiBase}/v1"
export OPENAI_API_KEY="$TURTLEROUTE_API_KEY"`,
  sdk: `# Anthropic
Anthropic(base_url="${apiBase}", api_key=...)

# OpenAI
OpenAI(base_url="${apiBase}/v1", api_key=...)`,
  gemini: `${apiBase}/v1beta/models`,
}

const clientItems = computed(() =>
  (['claudeCode', 'codex', 'sdk', 'gemini'] as const).map((key) => ({
    key,
    title: t(`site.docs.clients.items.${key}.title`),
    body: t(`site.docs.clients.items.${key}.body`),
    snippet: CLIENT_SNIPPETS[key],
  })),
)

const MEDIA_GROUP_KEYS = ['images', 'video', 'voice', 'realtime']
const mediaGroups = computed(() =>
  ENDPOINT_GROUPS.filter((group) => MEDIA_GROUP_KEYS.includes(group.key)),
)

const errorLimits = computed(() =>
  (['concurrency', 'rate', 'quota', 'upstream'] as const).map((key) => ({
    key,
    term: t(`site.docs.errors.limits.${key}.term`),
    desc: t(`site.docs.errors.limits.${key}.desc`),
  })),
)
</script>

<style scoped>
.endpointrow {
  grid-template-columns: 3.25rem minmax(0, 1fr);
  gap: 0 1rem;
}
</style>
