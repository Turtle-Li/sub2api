<template>
  <SiteLayout>
    <!-- 构图：左侧日期栏 + 右侧条目，单列时间轴。刻意区别于
         /models 的宽表和 /docs 的双栏正文。 -->
    <section class="mx-auto max-w-[1180px] px-5 pb-10 pt-20 sm:px-8 sm:pt-24">
      <h1 class="display-xl max-w-[16ch]">{{ t('site.changelog.title') }}</h1>
      <p class="lede mt-6 max-w-[46ch]">{{ t('site.changelog.lede') }}</p>
      <p class="note mt-8 max-w-[60ch]">{{ t('site.changelog.note') }}</p>
    </section>

    <section class="mx-auto max-w-[1180px] px-5 pb-24 sm:px-8">
      <p v-if="!entries.length" class="meta hairline-t py-10">{{ t('site.changelog.empty') }}</p>

      <ol v-else class="hairline-t">
        <li v-for="entry in entries" :key="entry.id" class="entry hairline-b">
          <div class="entry-when">
            <time class="meta block tabular-nums" :datetime="entry.date">{{ formatDate(entry.date) }}</time>
            <span class="entry-tag meta" :class="`tag-${entry.tag}`">
              {{ t(`site.changelog.tags.${entry.tag}`) }}
            </span>
          </div>

          <div class="min-w-0">
            <h2 class="display-sm">
              {{ pick(entry.title) }}
              <span v-if="entry.placeholder" class="placeholder meta ml-2 align-middle">
                {{ t('site.changelog.placeholderMark') }}
              </span>
            </h2>
            <p class="body-sm mt-2 max-w-[62ch]">{{ pick(entry.body) }}</p>
          </div>
        </li>
      </ol>
    </section>
  </SiteLayout>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import SiteLayout from '@/components/site/SiteLayout.vue'
import { CHANGELOG } from '@/data/changelog'

const { t, locale } = useI18n()

const entries = computed(() =>
  [...CHANGELOG].sort((a, b) => (a.date < b.date ? 1 : a.date > b.date ? -1 : 0)),
)

/** 条目内容是数据不是界面文案，按当前语言取一份。 */
function pick(value: { zh: string; en: string }): string {
  return locale.value === 'zh' ? value.zh : value.en
}

function formatDate(iso: string): string {
  const date = new Date(`${iso}T00:00:00`)
  if (Number.isNaN(date.getTime())) return iso
  return new Intl.DateTimeFormat(locale.value === 'zh' ? 'zh-CN' : 'en-GB', {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
  }).format(date)
}
</script>

<style scoped>
.entry {
  display: grid;
  grid-template-columns: minmax(0, 9rem) minmax(0, 1fr);
  gap: 1.5rem 2.5rem;
  padding: 2rem 0;
}

.entry-when {
  position: sticky;
  top: 5.5rem;
  align-self: start;
}

.entry-tag {
  display: block;
  margin-top: 0.375rem;
}

.tag-feature {
  color: var(--accent);
}
.tag-notice {
  color: var(--warn);
}

/* 窄屏收成单列，日期和标签并排成一行元信息 */
@media (max-width: 767px) {
  .entry {
    grid-template-columns: minmax(0, 1fr);
    gap: 0.75rem;
    padding: 1.75rem 0;
  }
  .entry-when {
    position: static;
    display: flex;
    align-items: baseline;
    gap: 0.875rem;
  }
  .entry-tag {
    margin-top: 0;
  }
}
</style>
