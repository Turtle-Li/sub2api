<template>
  <SiteLayout>
    <!-- 声明 + 协议面表。这页的核心主张是「四套协议面」，所以它
         占首屏，且以宽表呈现而不是几张卡片。 -->
    <section class="mx-auto max-w-[1180px] px-5 pb-12 pt-20 sm:px-8 sm:pt-24">
      <h1 class="display-xl max-w-[18ch]">{{ t('site.platform.title') }}</h1>
      <p class="lede mt-7 max-w-[52ch]">{{ t('site.platform.lede') }}</p>
    </section>

    <section class="hairline-t">
      <div class="mx-auto max-w-[1180px] px-5 sm:px-8">
        <div class="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-2 py-5">
          <h2 class="meta-strong">
            <span class="index mr-3">{{ t('site.platform.protocols.index') }}</span>
            {{ t('site.platform.protocols.title') }}
          </h2>
        </div>
        <!-- 分隔线要走满栏宽，所以限宽放在内层文字上 -->
        <div class="hairline-t py-6">
          <p class="lede-sm max-w-[62ch]">{{ t('site.platform.protocols.lede') }}</p>
        </div>

        <div class="surfacerow drow drow-head meta hairline-t py-2.5" aria-hidden="true">
          <span>{{ t('site.platform.protocols.cols.surface') }}</span>
          <span>{{ t('site.platform.protocols.cols.base') }}</span>
          <span>{{ t('site.platform.protocols.cols.desc') }}</span>
        </div>

        <div class="hairline-t">
          <div
            v-for="surface in surfaces"
            :key="surface.key"
            class="surfacerow drow hairline-b py-4"
          >
            <span class="dcell-text font-medium text-[color:var(--ink)]">
              {{ t(`site.platform.protocols.items.${surface.key}.name`) }}
            </span>
            <span class="dcell-id">{{ surface.base }}</span>
            <span class="dcell-text">
              {{ t(`site.platform.protocols.items.${surface.key}.desc`) }}
            </span>
          </div>
        </div>

        <p class="meta meta-cn py-4">
          <router-link to="/docs/protocols" class="textlink">
            {{ t('site.docs.sections.protocols') }} →
          </router-link>
        </p>
      </div>
    </section>

    <!-- 02 调度与绕行：唯一带图的一节，图讲的是号池，不是首页那张
         故障切换拓扑，两张图不重复。 -->
    <section class="hairline-t">
      <div class="mx-auto max-w-[1180px] px-5 py-16 sm:px-8 sm:py-20">
        <p class="index">{{ t('site.platform.routing.index') }}</p>
        <h2 class="display-md mt-4 max-w-[22ch]">{{ t('site.platform.routing.title') }}</h2>
        <p class="lede-sm mt-5 max-w-[58ch]">{{ t('site.platform.routing.lede') }}</p>

        <svg class="pooldiagram mt-12 hidden sm:block" viewBox="0 0 900 300" fill="none" aria-hidden="true">
          <!-- 端点 -->
          <g transform="translate(90 150)">
            <polygon :points="hexPoints(0, 0, 46)" class="node-hex is-gateway" />
            <polygon :points="hexPoints(0, 0, 16)" class="node-core" />
          </g>
          <text x="90" y="224" class="node-label" text-anchor="middle">
            {{ t('site.platform.routing.diagram.gateway') }}
          </text>

          <!-- 端点 → 三条上游 -->
          <path
            v-for="row in poolRows"
            :key="`edge-${row.y}`"
            :d="`M 140 150 C 240 150 260 ${row.y} 340 ${row.y}`"
            class="edge"
          />

          <!-- 每条上游后面挂一串账号 -->
          <g v-for="row in poolRows" :key="`row-${row.y}`">
            <polygon :points="hexPoints(366, row.y, 22)" class="node-hex is-route" />
            <line :x1="392" :y1="row.y" :x2="452" :y2="row.y" class="edge is-thin" />
            <polygon
              v-for="(account, i) in row.accounts"
              :key="i"
              :points="hexPoints(478 + i * 52, row.y, 15)"
              class="node-hex"
              :class="account ? 'is-healthy' : 'is-spent'"
            />
          </g>

          <text x="478" y="278" class="node-label">
            {{ t('site.platform.routing.diagram.pool') }}
          </text>
        </svg>

        <p class="meta meta-cn mt-5">{{ t('site.platform.routing.diagram.note') }}</p>

        <dl class="pointgrid hairline-t mt-12">
          <div v-for="point in routingPoints" :key="point.key" class="pointcell hairline-b">
            <dt class="meta-strong">{{ point.term }}</dt>
            <dd class="body-sm mt-2">{{ point.desc }}</dd>
          </div>
        </dl>
      </div>
    </section>

    <!-- 03–06：统一的「序号 + 标题 + 引言 + 两列定义网格」节奏 -->
    <section v-for="section in textSections" :key="section.key" class="hairline-t">
      <div class="mx-auto max-w-[1180px] px-5 py-16 sm:px-8 sm:py-20">
        <p class="index">{{ section.index }}</p>
        <h2 class="display-md mt-4 max-w-[24ch]">{{ section.title }}</h2>
        <p class="lede-sm mt-5 max-w-[58ch]">{{ section.lede }}</p>

        <dl class="pointgrid hairline-t mt-12">
          <div v-for="point in section.points" :key="point.key" class="pointcell hairline-b">
            <dt class="meta-strong">{{ point.term }}</dt>
            <dd class="body-sm mt-2">{{ point.desc }}</dd>
          </div>
        </dl>
      </div>
    </section>

    <section class="hairline-t" :style="{ background: 'var(--surface)' }">
      <div class="mx-auto max-w-[1180px] px-5 py-16 sm:px-8">
        <h2 class="display-md max-w-[20ch]">{{ t('site.platform.cta.title') }}</h2>
        <p class="lede-sm mt-4 max-w-[52ch]">{{ t('site.platform.cta.lede') }}</p>
        <div class="mt-8 flex flex-wrap gap-x-8 gap-y-3">
          <router-link to="/docs" class="btn-outline">{{ t('site.nav.docs') }}</router-link>
          <router-link to="/models" class="btn-outline">{{ t('site.nav.models') }}</router-link>
        </div>
      </div>
    </section>
  </SiteLayout>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import SiteLayout from '@/components/site/SiteLayout.vue'
import { useSite } from '@/components/site/useSite'
import { hexPoints } from '@/components/site/geometry'
import { PROTOCOL_SURFACES } from '@/data/siteEndpoints'

const { t } = useSite()

const surfaces = PROTOCOL_SURFACES

/** 示意号池：true = 健康，false = 已用尽/受限。纯示意，不是实时数据。 */
const poolRows = [
  { y: 70, accounts: [true, true, true, false] },
  { y: 150, accounts: [true, true, false, false] },
  { y: 230, accounts: [true, true, true, true] },
]

const routingPoints = computed(() =>
  (['multiAccount', 'sticky', 'failover', 'local'] as const).map((key) => ({
    key,
    term: t(`site.platform.routing.points.${key}.term`),
    desc: t(`site.platform.routing.points.${key}.desc`),
  })),
)

/** 03–06 四节共用同一种排版，只有内容不同。 */
const textSections = computed(() => [
  {
    key: 'billing',
    index: t('site.platform.billing.index'),
    title: t('site.platform.billing.title'),
    lede: t('site.platform.billing.lede'),
    points: (['token', 'catalog', 'quota', 'query'] as const).map((key) => ({
      key,
      term: t(`site.platform.billing.points.${key}.term`),
      desc: t(`site.platform.billing.points.${key}.desc`),
    })),
  },
  {
    key: 'limits',
    index: t('site.platform.limits.index'),
    title: t('site.platform.limits.title'),
    lede: t('site.platform.limits.lede'),
    points: (['userConcurrency', 'accountConcurrency', 'rate', 'publicIp'] as const).map((key) => ({
      key,
      term: t(`site.platform.limits.points.${key}.term`),
      desc: t(`site.platform.limits.points.${key}.desc`),
    })),
  },
  {
    key: 'capabilities',
    index: t('site.platform.capabilities.index'),
    title: t('site.platform.capabilities.title'),
    lede: t('site.platform.capabilities.lede'),
    points: (['images', 'video', 'voice', 'realtime'] as const).map((key) => ({
      key,
      term: t(`site.platform.capabilities.groups.${key}.term`),
      desc: t(`site.platform.capabilities.groups.${key}.desc`),
    })),
  },
  {
    key: 'ops',
    index: t('site.platform.ops.index'),
    title: t('site.platform.ops.title'),
    lede: t('site.platform.ops.lede'),
    points: (['admin', 'monitor', 'payment', 'embed'] as const).map((key) => ({
      key,
      term: t(`site.platform.ops.points.${key}.term`),
      desc: t(`site.platform.ops.points.${key}.desc`),
    })),
  },
])
</script>

<style scoped>
.surfacerow {
  grid-template-columns: minmax(0, 1.1fr) minmax(0, 0.9fr) minmax(0, 2.4fr);
}

@media (max-width: 767px) {
  .surfacerow {
    grid-template-columns: minmax(0, 1fr);
    gap: 0.375rem;
  }
}

/* 定义网格：两列的 hairline 网格，不是卡片。 */
.pointgrid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 4rem;
}
.pointcell {
  padding: 1.5rem 0;
  min-width: 0;
}

@media (max-width: 767px) {
  .pointgrid {
    grid-template-columns: minmax(0, 1fr);
  }
}

/* 号池示意图，沿用六边形语言 */
.pooldiagram {
  width: 100%;
  height: auto;
}
.pooldiagram .edge {
  stroke: var(--rule);
  stroke-width: 1;
}
.pooldiagram .edge.is-thin {
  stroke-dasharray: 3 4;
}
.pooldiagram .node-hex {
  fill: none;
  stroke: var(--rule);
  stroke-width: 1;
}
.pooldiagram .node-hex.is-gateway {
  stroke: var(--ink);
}
.pooldiagram .node-hex.is-route {
  stroke: var(--accent);
}
.pooldiagram .node-hex.is-healthy {
  stroke: var(--accent);
  fill: var(--accent-wash);
}
.pooldiagram .node-hex.is-spent {
  stroke: var(--rule);
  stroke-dasharray: 3 3;
}
.pooldiagram .node-core {
  fill: var(--accent);
}
.pooldiagram .node-label {
  font-family: var(--mono);
  font-size: 11px;
  letter-spacing: 0.1em;
  fill: var(--muted);
}
</style>
