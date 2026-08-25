<template>
  <!-- Custom Home Content: Full Page Mode -->
  <div v-if="hasHomeContent" class="min-h-screen">
    <!-- iframe mode -->
    <iframe
      v-if="isHomeContentUrl"
      :src="homeContent.trim()"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <!-- HTML mode - SECURITY: homeContent is admin-only setting, XSS risk is acceptable -->
    <div v-else v-html="homeContent"></div>
  </div>

  <!-- Compact Home Page: standalone, deliberately not wrapped in SiteLayout -->
  <div
    v-else-if="compactHomeEnabled"
    data-testid="compact-home"
    class="tr flex min-h-screen flex-col"
  >
    <header class="hairline-b px-5 py-4 sm:px-8">
      <nav class="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-3">
        <div class="flex min-w-0 flex-1 items-center gap-3">
          <img
            :src="siteLogo || '/turtleroute-mark.png'"
            alt=""
            class="h-8 w-8 shrink-0 object-contain"
          />
          <span class="min-w-0 truncate text-[15px] font-semibold tracking-tight">{{ siteName }}</span>
        </div>
        <div class="flex max-w-full shrink-0 flex-wrap items-center justify-end gap-1">
          <LocaleSwitcher />
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="icon-btn"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>
          <router-link
            v-if="showModelPlazaEntry"
            to="/model-plaza"
            class="icon-btn"
            :title="t('nav.modelPlaza')"
          >
            <Icon name="grid" size="md" />
          </router-link>
          <button
            class="icon-btn"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <router-link :to="primaryDestination" class="btn-solid ml-1">
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="flex min-w-0 flex-1 items-center px-5 py-20 sm:px-8">
      <div class="mx-auto min-w-0 max-w-2xl">
        <img
          :src="siteLogo || '/turtleroute-mark.png'"
          alt=""
          class="mb-8 h-16 w-16 object-contain"
        />
        <h1 class="display-xl [overflow-wrap:anywhere]">{{ siteName }}</h1>
        <p class="lede mt-5 whitespace-pre-wrap [overflow-wrap:anywhere]">{{ siteSubtitle }}</p>
        <router-link :to="primaryDestination" class="btn-solid mt-9 h-11 px-6">
          {{ isAuthenticated ? t('home.goToDashboard') : t('home.login') }}
        </router-link>
      </div>
    </main>

    <footer class="hairline-t meta meta-cn min-w-0 px-5 py-6 [overflow-wrap:anywhere] sm:px-8">
      &copy; {{ currentYear }} {{ siteName }}
    </footer>
  </div>

  <!-- Default Home Page: the overview of the public site -->
  <SiteLayout v-else>
    <div data-testid="default-home">
      <!-- ── Statement + shell field ──────────────────────────────
           The mark is rebuilt as page structure: shell plates and an
           orbit drawn in hairline, bleeding off the right edge. -->
      <section class="relative overflow-hidden">
        <svg
          class="shell-field"
          viewBox="0 0 720 380"
          fill="none"
          aria-hidden="true"
          preserveAspectRatio="xMinYMid slice"
        >
          <g transform="translate(430 190)">
            <polygon
              v-for="(plate, i) in plates"
              :key="`plate-${i}`"
              :points="hexPoints(plate.cx, plate.cy, 46)"
              class="plate"
            />
            <ellipse rx="300" ry="108" transform="rotate(-20)" class="orbit" />
            <polygon
              v-for="(node, i) in orbit"
              :key="`node-${i}`"
              :points="hexPoints(node.x, node.y, 7)"
              class="orbit-node"
            />
          </g>
        </svg>

        <div class="relative mx-auto max-w-[1180px] px-5 pb-14 pt-20 sm:px-8 sm:pb-20 sm:pt-28">
          <h1 class="display-xl max-w-[17ch]">{{ t('home.landing.hero.title') }}</h1>
          <p class="lede mt-7 max-w-[46ch]">{{ t('home.landing.hero.lede') }}</p>

          <p class="meta meta-cn mt-10 flex flex-wrap items-center gap-x-3 gap-y-2">
            <span>{{ t('home.landing.hero.facts.providers', { count: providers.length }) }}</span>
            <span class="dot" aria-hidden="true"></span>
            <span>{{ t('home.landing.hero.facts.endpoint') }}</span>
            <span class="dot" aria-hidden="true"></span>
            <span>{{ t('home.landing.hero.facts.protocols') }}</span>
          </p>
          <p class="mt-3 font-mono text-[12px] leading-relaxed text-[color:var(--ink-2)]">
            {{ providers.join('  ·  ') }}
          </p>
        </div>
      </section>

      <!-- ── Route table: the working surface ─────────────────────
           Full-bleed, dense, and the one real interaction here:
           picking a row rewrites the request example below it. -->
      <section class="hairline-t">
        <div class="mx-auto max-w-[1180px] px-5 sm:px-8">
          <div class="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-2 py-5">
            <h2 class="meta-strong">{{ t('home.landing.table.label') }}</h2>
            <p class="meta">{{ t('home.landing.table.count', { shown: filteredModels.length, total: models.length }) }}</p>
          </div>

          <div class="hairline-t flex flex-wrap items-center gap-x-5 gap-y-2 py-3.5">
            <span class="meta shrink-0">{{ t('home.landing.table.filterLabel') }}</span>
            <button
              v-for="filter in modelFilters"
              :key="filter.key"
              type="button"
              class="filterlink"
              :class="{ 'is-on': activeFilter === filter.key }"
              :aria-pressed="activeFilter === filter.key"
              @click="activeFilter = filter.key"
            >
              {{ filter.label }}
            </button>
          </div>

          <!-- Column header: desktop only, rows are self-labelling on mobile -->
          <div class="routerow drow drow-head meta hairline-t py-2.5" aria-hidden="true">
            <span></span>
            <span>{{ t('home.landing.table.cols.model') }}</span>
            <span>{{ t('home.landing.table.cols.provider') }}</span>
            <span>{{ t('home.landing.table.cols.capability') }}</span>
            <span>{{ t('home.landing.table.cols.region') }}</span>
          </div>

          <div class="hairline-t">
            <button
              v-for="model in filteredModels"
              :key="model.id"
              type="button"
              class="routerow drow drow-btn hairline-b"
              :class="{ 'is-on': model.id === selectedModelId }"
              :aria-pressed="model.id === selectedModelId"
              @click="selectedModelId = model.id"
            >
              <span class="hexmark" aria-hidden="true">
                <svg viewBox="-9 -9 18 18" fill="none">
                  <polygon :points="hexPoints(0, 0, 7)" />
                </svg>
              </span>
              <span class="dcell-id">{{ model.id }}</span>
              <span class="dcell-text rowprovider">{{ model.provider }}</span>
              <span class="dcell-meta rowtag">{{ t(`home.landing.table.tags.${model.tag}`) }}</span>
              <span class="dcell-meta rowregion">{{ t(`home.landing.table.regions.${model.region}`) }}</span>
            </button>
          </div>

          <p class="meta meta-cn flex flex-wrap items-center justify-between gap-x-6 gap-y-2 py-4">
            <span class="max-w-[60ch]">{{ t('home.landing.table.note') }}</span>
            <router-link to="/models" class="textlink shrink-0">
              {{ t('site.common.viewAll') }} →
            </router-link>
          </p>
        </div>
      </section>

      <!-- ── Request example, wired to the selection above ────────── -->
      <section class="hairline-t" :style="{ background: 'var(--surface)' }">
        <div class="mx-auto max-w-[1180px] px-5 py-6 sm:px-8 sm:py-8">
          <div class="flex flex-wrap items-center justify-between gap-x-6 gap-y-3">
            <div class="flex min-w-0 items-baseline gap-3">
              <span class="verb">POST</span>
              <span class="truncate font-mono text-[13px] text-[color:var(--ink)]">/v1/messages</span>
            </div>
            <div class="flex flex-wrap items-center gap-x-5 gap-y-2">
              <button
                v-for="tab in codeTabs"
                :key="tab.key"
                type="button"
                class="filterlink"
                :class="{ 'is-on': activeCodeTab === tab.key }"
                :aria-pressed="activeCodeTab === tab.key"
                @click="activeCodeTab = tab.key"
              >
                {{ tab.label }}
              </button>
              <button
                type="button"
                class="btn-ghost"
                @click="copyToClipboard(activeSnippet, t('home.landing.request.copied'))"
              >
                <Icon :name="codeCopied ? 'check' : 'copy'" size="sm" :stroke-width="2" />
                <span>{{ codeCopied ? t('home.landing.request.copied') : t('home.landing.request.copy') }}</span>
              </button>
            </div>
          </div>

          <pre class="snippet hairline-t mt-5 pt-5">{{ activeSnippet }}</pre>

          <p class="meta meta-cn mt-4 flex flex-wrap items-center justify-between gap-x-6 gap-y-2">
            <span>{{ t('home.landing.request.hint') }} · {{ t('home.landing.request.baseHint') }}</span>
            <router-link to="/docs" class="textlink shrink-0">
              {{ t('site.nav.docs') }} →
            </router-link>
          </p>
        </div>
      </section>

      <!-- ── 01 Failover topology ─────────────────────────────────
           Asymmetric: index and prose in a narrow left column, the
           graph takes the rest. Clicking actually re-routes. -->
      <section class="hairline-t">
        <div class="mx-auto grid max-w-[1180px] gap-10 px-5 py-16 sm:px-8 sm:py-20 lg:grid-cols-[minmax(0,20rem)_minmax(0,1fr)] lg:gap-16">
          <div>
            <p class="index">{{ t('home.landing.topology.index') }}</p>
            <h2 class="display-md mt-4">{{ t('home.landing.topology.title') }}</h2>
            <p class="lede-sm mt-5">{{ t('home.landing.topology.lede') }}</p>
            <button type="button" class="btn-outline mt-8" @click="primaryDown = !primaryDown">
              {{ primaryDown ? t('home.landing.topology.restore') : t('home.landing.topology.fail') }}
            </button>
            <p class="mt-6">
              <router-link to="/platform" class="textlink">
                {{ t('site.nav.platform') }} →
              </router-link>
            </p>
          </div>

          <div class="min-w-0">
            <!-- Route identities live in the list below, so the graph stays
                 unlabelled and can use its full width for the paths. -->
            <svg
              class="topology hidden sm:block"
              viewBox="0 0 660 250"
              fill="none"
              aria-hidden="true"
            >
              <!-- app → gateway -->
              <line x1="118" y1="125" x2="252" y2="125" class="edge is-active" />
              <rect x="8" y="107" width="110" height="36" class="node-box" />
              <text x="63" y="130" class="node-label" text-anchor="middle">
                {{ t('home.landing.topology.client') }}
              </text>

              <!-- gateway → upstreams -->
              <path
                v-for="route in routeNodes"
                :key="`edge-${route.id}`"
                :d="`M 340 125 C 460 125 500 ${route.y} 604 ${route.y}`"
                class="edge"
                :class="`is-${routeState(route.id)}`"
              />

              <!-- gateway -->
              <g transform="translate(296 125)">
                <polygon :points="hexPoints(0, 0, 44)" class="node-hex is-gateway" />
                <polygon :points="hexPoints(0, 0, 15)" class="node-core" />
              </g>
              <text x="296" y="196" class="node-label" text-anchor="middle">
                {{ t('home.landing.topology.gateway') }}
              </text>

              <!-- upstream routes -->
              <polygon
                v-for="route in routeNodes"
                :key="`node-${route.id}`"
                :points="hexPoints(630, route.y, 26)"
                class="node-hex"
                :class="`is-${routeState(route.id)}`"
              />
            </svg>

            <!-- Accessible, and the only version shown on narrow screens -->
            <dl class="hairline-t sm:mt-10">
              <div v-for="route in routeNodes" :key="`row-${route.id}`" class="staterow hairline-b">
                <dt class="flex min-w-0 items-center gap-3">
                  <span class="hexmark" :class="`is-${routeState(route.id)}`" aria-hidden="true">
                    <svg viewBox="-9 -9 18 18" fill="none">
                      <polygon :points="hexPoints(0, 0, 7)" />
                    </svg>
                  </span>
                  <span class="truncate font-mono text-[13px]">{{ route.id }}</span>
                </dt>
                <dd class="meta shrink-0" :class="`state-${routeState(route.id)}`">
                  {{ t(`home.landing.topology.states.${routeState(route.id)}`) }}
                </dd>
              </div>
            </dl>

            <p class="meta meta-cn mt-4">{{ t('home.landing.topology.note') }}</p>
          </div>
        </div>
      </section>

      <!-- ── 02 Self-hosted routes ────────────────────────────────
           Narrow measure, reads like a manual excerpt. Deliberately
           the tightest column here. -->
      <section class="hairline-t" :style="{ background: 'var(--surface)' }">
        <div class="mx-auto max-w-[46rem] px-5 py-16 sm:px-8 sm:py-20">
          <p class="index">{{ t('home.landing.local.index') }}</p>
          <h2 class="display-md mt-4">{{ t('home.landing.local.title') }}</h2>
          <p class="lede-sm mt-5">{{ t('home.landing.local.lede') }}</p>

          <figure class="hairline mt-10">
            <figcaption class="meta hairline-b px-4 py-2.5">
              {{ t('home.landing.local.fileName') }}
            </figcaption>
            <div class="listing">
              <p v-for="(line, i) in localConfigLines" :key="i" class="listing-line">
                <span class="listing-no" aria-hidden="true">{{ i + 1 }}</span>
                <span class="listing-code">{{ line || ' ' }}</span>
              </p>
            </div>
          </figure>

          <dl class="hairline-t mt-10">
            <div v-for="point in localPoints" :key="point.term" class="defrow hairline-b">
              <dt class="meta-strong">{{ point.term }}</dt>
              <dd class="body-sm">{{ point.desc }}</dd>
            </div>
          </dl>

          <p class="mt-8">
            <router-link to="/docs" class="textlink">{{ t('site.nav.docs') }} →</router-link>
          </p>
        </div>
      </section>

      <!-- ── 03 Site index: a table of contents, not a CTA block ──── -->
      <section class="hairline-t">
        <div class="mx-auto max-w-[1180px] px-5 py-16 sm:px-8 sm:py-20">
          <p class="index">{{ t('home.landing.next.index') }}</p>
          <h2 class="display-md mt-4">{{ t('home.landing.next.title') }}</h2>

          <ul class="hairline-t mt-10">
            <li v-for="(entry, i) in siteIndex" :key="entry.key" class="hairline-b">
              <component
                :is="entry.to ? 'router-link' : entry.href ? 'a' : 'div'"
                :to="entry.to"
                :href="entry.href"
                :target="entry.href ? '_blank' : undefined"
                :rel="entry.href ? 'noopener noreferrer' : undefined"
                class="indexrow"
                :class="{ 'is-inert': !entry.to && !entry.href }"
              >
                <span class="meta shrink-0 tabular-nums">{{ String(i + 1).padStart(2, '0') }}</span>
                <span class="indexrow-label">{{ entry.label }}</span>
                <span class="indexrow-desc">{{ entry.desc }}</span>
                <span v-if="entry.pending" class="meta shrink-0">{{ t('home.landing.next.pending') }}</span>
                <span v-else class="arrow shrink-0" aria-hidden="true">→</span>
              </component>
            </li>
          </ul>
        </div>
      </section>
    </div>
  </SiteLayout>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import SiteLayout from '@/components/site/SiteLayout.vue'
import { useSite, GITHUB_URL } from '@/components/site/useSite'
import { hexPoints, shellPlates, orbitNodes } from '@/components/site/geometry'
import { useClipboard } from '@/composables/useClipboard'
import { useAppStore, useAuthStore } from '@/stores'

// 紧凑首页与自定义首页分支不套 SiteLayout，样式表得自己引一次。
// 打包器会去重，重复导入没有额外成本。
import '@/styles/site.css'

const {
  t,
  siteName,
  siteLogo,
  siteSubtitle,
  docUrl,
  isAuthenticated,
  primaryDestination,
  statusDestination,
  showModelPlazaEntry,
} = useSite()

const appStore = useAppStore()
const authStore = useAuthStore()

const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')
const hasHomeContent = computed(() => homeContent.value.trim().length > 0)
const compactHomeEnabled = computed(() => appStore.cachedPublicSettings?.compact_home_enabled === true)

// Check if homeContent is a URL (for iframe display)
const isHomeContentUrl = computed(() => {
  const content = homeContent.value.trim()
  return content.startsWith('http://') || content.startsWith('https://')
})

const currentYear = computed(() => new Date().getFullYear())

// 主题：初始值由 main.ts 写好，这里只切换。默认首页的切换在 SiteHeader，
// 这份是给不套 SiteLayout 的紧凑首页用的。
const isDark = ref(document.documentElement.classList.contains('dark'))
function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

// ── Shell field geometry ───────────────────────────────────────
const plates = shellPlates(46, 6)
const orbit = orbitNodes([25, 150, 265])

// ── Route table ────────────────────────────────────────────────
// 代表性目录，权威列表在 /models 和模型广场。
const providers = ['Anthropic', 'OpenAI', 'Google', 'DeepSeek', 'Moonshot', 'Zhipu', 'Alibaba', 'MiniMax']

type ModelTag = 'coding' | 'reasoning' | 'multimodal' | 'general'
type ModelRegion = 'international' | 'china'
type ModelFilter = 'all' | ModelRegion | 'reasoning' | 'multimodal'

const models: Array<{ id: string; provider: string; tag: ModelTag; region: ModelRegion }> = [
  { id: 'claude-sonnet-4-5', provider: 'Anthropic', tag: 'coding', region: 'international' },
  { id: 'claude-opus-4-1', provider: 'Anthropic', tag: 'reasoning', region: 'international' },
  { id: 'gpt-4o', provider: 'OpenAI', tag: 'multimodal', region: 'international' },
  { id: 'gemini-2.5-pro', provider: 'Google', tag: 'reasoning', region: 'international' },
  { id: 'deepseek-v3', provider: 'DeepSeek', tag: 'general', region: 'china' },
  { id: 'kimi-k2', provider: 'Moonshot', tag: 'coding', region: 'china' },
  { id: 'glm-4.6', provider: 'Zhipu', tag: 'general', region: 'china' },
  { id: 'qwen3-max', provider: 'Alibaba', tag: 'general', region: 'china' },
]

const activeFilter = ref<ModelFilter>('all')
const modelFilters = computed(() =>
  (['all', 'international', 'china', 'reasoning', 'multimodal'] as const).map((key) => ({
    key,
    label: t(`home.landing.table.filters.${key}`),
  })),
)

const filteredModels = computed(() => {
  const filter = activeFilter.value
  if (filter === 'all') return models
  if (filter === 'reasoning' || filter === 'multimodal') {
    return models.filter((model) => model.tag === filter)
  }
  return models.filter((model) => model.region === filter)
})

const selectedModelId = ref(models[0].id)

// 选中项被筛掉时落到可见的第一行，示例始终对应看得见的那一行
watch(filteredModels, (visible) => {
  if (visible.length && !visible.some((model) => model.id === selectedModelId.value)) {
    selectedModelId.value = visible[0].id
  }
})

// ── Request example ────────────────────────────────────────────
const apiBase =
  typeof window !== 'undefined' ? window.location.origin : 'https://turtleroute.example.com'

const codeTabs = computed(() =>
  (['curl', 'python', 'node'] as const).map((key) => ({
    key,
    label: { curl: 'cURL', python: 'Python', node: 'Node.js' }[key],
  })),
)
type CodeTab = 'curl' | 'python' | 'node'
const activeCodeTab = ref<CodeTab>('curl')

const activeSnippet = computed(() => {
  const model = selectedModelId.value
  if (activeCodeTab.value === 'python') {
    return `import os
from anthropic import Anthropic

client = Anthropic(
    base_url="${apiBase}",
    api_key=os.environ["TURTLEROUTE_API_KEY"],
)
message = client.messages.create(
    model="${model}",
    max_tokens=1024,
    messages=[{"role": "user", "content": "ping"}],
)`
  }
  if (activeCodeTab.value === 'node') {
    return `import Anthropic from '@anthropic-ai/sdk'

const client = new Anthropic({
  baseURL: '${apiBase}',
  apiKey: process.env.TURTLEROUTE_API_KEY,
})

const message = await client.messages.create({
  model: '${model}',
  max_tokens: 1024,
  messages: [{ role: 'user', content: 'ping' }],
})`
  }
  return `curl ${apiBase}/v1/messages \\
  -H "Authorization: Bearer $TURTLEROUTE_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${model}",
    "max_tokens": 1024,
    "messages": [{ "role": "user", "content": "ping" }]
  }'`
})

const { copied: codeCopied, copyToClipboard } = useClipboard()

// ── Failover topology ──────────────────────────────────────────
// 示意，并已在页面上标注。状态变化是真的：切换会重算每条边和每个节点。
type RouteState = 'active' | 'standby' | 'degraded'
const routeNodes = [
  { id: 'anthropic-primary', y: 48 },
  { id: 'anthropic-backup', y: 125 },
  { id: 'openai-compat', y: 202 },
]
const primaryDown = ref(false)

function routeState(id: string): RouteState {
  if (id === 'anthropic-primary') return primaryDown.value ? 'degraded' : 'active'
  if (id === 'anthropic-backup') return primaryDown.value ? 'active' : 'standby'
  return 'standby'
}

// ── Self-hosted routes ─────────────────────────────────────────
const localConfigLines = `routes:
  - name: local-llama
    kind: ollama
    base_url: http://127.0.0.1:11434
    model: llama3.1

  - name: anthropic-primary
    kind: anthropic
    api_key_ref: ANTHROPIC_KEY

# Callers keep sending:
#   POST /v1/messages  { "model": "local-llama", ... }`.split('\n')

const localPoints = computed(() =>
  (['endpoint', 'scheduling', 'policy'] as const).map((key) => ({
    term: t(`home.landing.local.points.${key}.term`),
    desc: t(`home.landing.local.points.${key}.desc`),
  })),
)

// ── Site index ─────────────────────────────────────────────────
// 首页是概览页，结尾这份索引就是通往各子页的入口。
const siteIndex = computed(() => {
  const entries: Array<{
    key: string
    label: string
    desc: string
    to?: string
    href?: string
    pending?: boolean
  }> = [
    { key: 'models', label: t('site.nav.models'), desc: t('site.nav.desc.models'), to: '/models' },
    { key: 'platform', label: t('site.nav.platform'), desc: t('site.nav.desc.platform'), to: '/platform' },
    { key: 'docs', label: t('site.nav.docs'), desc: t('site.nav.desc.docs'), to: '/docs' },
    { key: 'why', label: t('site.nav.why'), desc: t('site.nav.desc.why'), to: '/why' },
    { key: 'changelog', label: t('site.nav.changelog'), desc: t('site.nav.desc.changelog'), to: '/changelog' },
    {
      key: 'status',
      label: t('site.footer.links.status'),
      desc: t('home.landing.next.items.status.desc'),
      to: statusDestination.value,
    },
  ]

  if (showModelPlazaEntry.value) {
    entries.push({
      key: 'plaza',
      label: t('site.footer.links.plaza'),
      desc: t('home.landing.next.items.plaza.desc'),
      to: '/model-plaza',
    })
  }

  entries.push(
    {
      key: 'github',
      label: t('home.landing.next.items.github.label'),
      desc: t('home.landing.next.items.github.desc'),
      href: GITHUB_URL,
    },
    {
      key: 'app',
      label: t('home.landing.next.items.app.label'),
      desc: t('home.landing.next.items.app.desc'),
      pending: true,
    },
  )

  return entries
})

onMounted(() => {
  // 默认首页那一支由 SiteLayout 负责初始化；这里只覆盖不经过 SiteLayout
  // 的两支。checkAuth() 会顺带发一次 refreshUser，重复调用等于多发请求。
  if (hasHomeContent.value || compactHomeEnabled.value) {
    authStore.checkAuth()
  }
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }
})
</script>

<style scoped>
/* 只留这页私有的东西；通用令牌、字号、控件和结构原语都在
   @/styles/site.css 里，由 SiteLayout 引入。 */

/* ── Hero shell field ──────────────────────────────────────────
   The mark redrawn at page scale, bleeding off the right edge. */
.shell-field {
  position: absolute;
  inset: 0 auto 0 0;
  width: 100%;
  height: 100%;
  pointer-events: none;
  opacity: 0.85;
}
.shell-field .plate {
  stroke: var(--rule);
  stroke-width: 1;
}
.shell-field .orbit {
  stroke: var(--accent);
  stroke-width: 1;
  opacity: 0.42;
}
.shell-field .orbit-node {
  fill: var(--accent);
  opacity: 0.6;
}

/* ── Route table geometry ──────────────────────────────────────
   Typography comes from .drow / .dcell-* in site.css; only the
   column geometry is local. */
.routerow {
  grid-template-columns: 1.25rem minmax(0, 2.3fr) minmax(0, 1.1fr) minmax(0, 0.8fr) minmax(0, 0.6fr);
}

/* Narrow screens fold the row into two lines: id on top, then provider
   on the left with capability and region trailing right. */
@media (max-width: 639px) {
  .routerow {
    grid-template-columns: 1.25rem minmax(0, 1fr) auto auto;
    gap: 0.25rem 0.625rem;
  }
  .hexmark {
    grid-row: 1;
  }
  .dcell-id {
    grid-column: 2 / 5;
    grid-row: 1;
  }
  .rowprovider {
    grid-column: 2;
    grid-row: 2;
  }
  .rowtag {
    grid-column: 3;
    grid-row: 2;
    justify-self: end;
  }
  .rowregion {
    grid-column: 4;
    grid-row: 2;
    justify-self: end;
  }
}

/* ── Topology ──────────────────────────────────────────────────
   Hexagons carry every node state; there are no coloured dots. */
.topology {
  width: 100%;
  height: auto;
}
.topology .edge {
  stroke: var(--rule);
  stroke-width: 1;
  transition: stroke 0.3s ease, stroke-width 0.3s ease, opacity 0.3s ease;
}
.topology .edge.is-active {
  stroke: var(--accent);
  stroke-width: 1.75;
}
.topology .edge.is-standby {
  stroke-dasharray: 3 5;
}
.topology .edge.is-degraded {
  stroke: var(--warn);
  stroke-dasharray: 2 6;
  opacity: 0.55;
}
.topology .node-box {
  stroke: var(--rule);
  stroke-width: 1;
  fill: none;
}
.topology .node-hex {
  fill: none;
  stroke: var(--rule);
  stroke-width: 1;
  transition: stroke 0.3s ease, fill 0.3s ease, opacity 0.3s ease;
}
.topology .node-hex.is-gateway {
  stroke: var(--ink);
}
.topology .node-hex.is-active {
  stroke: var(--accent);
  fill: var(--accent-wash);
  stroke-width: 1.75;
}
.topology .node-hex.is-degraded {
  stroke: var(--warn);
  stroke-dasharray: 3 4;
  opacity: 0.6;
}
.topology .node-core {
  fill: var(--accent);
}
.topology .node-label {
  font-family: var(--mono);
  font-size: 11px;
  letter-spacing: 0.1em;
  fill: var(--muted);
}
</style>
