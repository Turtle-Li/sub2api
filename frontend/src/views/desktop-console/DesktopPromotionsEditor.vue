<template>
  <section class="rounded-xl border border-gray-200 bg-gray-50/70 p-4 dark:border-dark-700 dark:bg-dark-800/50">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
          TT Switch 合作推广
        </h3>
        <p class="mt-1 max-w-3xl text-xs leading-5 text-gray-500 dark:text-gray-400">
          配置会在桌面端按启用状态、排序和时间窗口展示。未配置时，桌面端不会显示推广入口。
        </p>
      </div>
      <button type="button" class="btn btn-secondary btn-sm" @click="addItem">
        添加推广
      </button>
    </div>

    <div v-if="modelValue.length" class="mt-4 space-y-4">
      <article
        v-for="(item, index) in modelValue"
        :key="item.id || index"
        class="rounded-lg border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900"
      >
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div class="flex items-center gap-3">
            <label class="inline-flex items-center gap-2 text-sm font-medium text-gray-800 dark:text-gray-200">
              <input
                :checked="item.enabled"
                type="checkbox"
                class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                @change="updateItem(index, 'enabled', ($event.target as HTMLInputElement).checked)"
              />
              启用
            </label>
            <span class="font-mono text-xs text-gray-400">{{ item.id || "未设置 ID" }}</span>
          </div>
          <button
            type="button"
            class="text-xs font-medium text-red-600 hover:text-red-700 dark:text-red-400"
            @click="removeItem(index)"
          >
            删除
          </button>
        </div>

        <div class="grid gap-4 md:grid-cols-2">
          <label class="block">
            <span class="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-300">唯一 ID</span>
            <input
              :value="item.id"
              type="text"
              class="input font-mono text-sm"
              maxlength="64"
              placeholder="turtle-chat"
              @input="updateItem(index, 'id', normalizeID(($event.target as HTMLInputElement).value))"
            />
          </label>
          <label class="block">
            <span class="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-300">标题</span>
            <input
              :value="item.title"
              type="text"
              class="input"
              maxlength="80"
              placeholder="Turtle Chat"
              @input="updateItem(index, 'title', ($event.target as HTMLInputElement).value)"
            />
          </label>
          <label class="block md:col-span-2">
            <span class="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-300">简介</span>
            <textarea
              :value="item.summary"
              class="input min-h-20 resize-y"
              maxlength="240"
              placeholder="一句话介绍这个工具"
              @input="updateItem(index, 'summary', ($event.target as HTMLTextAreaElement).value)"
            />
          </label>
          <label class="block md:col-span-2">
            <span class="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-300">跳转地址</span>
            <input
              :value="item.target_url"
              type="url"
              inputmode="url"
              autocomplete="off"
              class="input font-mono text-sm"
              maxlength="2048"
              placeholder="https://..."
              @input="updateItem(index, 'target_url', ($event.target as HTMLInputElement).value)"
            />
          </label>
          <label class="block">
            <span class="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-300">按钮文字</span>
            <input
              :value="item.cta_label"
              type="text"
              class="input"
              maxlength="24"
              placeholder="打开"
              @input="updateItem(index, 'cta_label', ($event.target as HTMLInputElement).value)"
            />
          </label>
          <label class="block">
            <span class="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-300">角标</span>
            <input
              :value="item.badge || ''"
              type="text"
              class="input"
              maxlength="24"
              placeholder="推荐"
              @input="updateItem(index, 'badge', ($event.target as HTMLInputElement).value)"
            />
          </label>
          <label class="block">
            <span class="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-300">图标</span>
            <select
              :value="item.icon"
              class="input"
              @change="updateIcon(index, ($event.target as HTMLSelectElement).value)"
            >
              <option v-for="option in iconOptions" :key="option.value" :value="option.value">
                {{ option.label }}
              </option>
            </select>
          </label>
          <label class="block">
            <span class="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-300">排序</span>
            <input
              :value="item.sort_order"
              type="number"
              class="input"
              min="-10000"
              max="10000"
              @input="updateItem(index, 'sort_order', Number(($event.target as HTMLInputElement).value) || 0)"
            />
          </label>
          <fieldset class="md:col-span-2">
            <legend class="mb-1.5 text-xs font-medium text-gray-600 dark:text-gray-300">展示位置</legend>
            <div class="flex flex-wrap gap-4">
              <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
                <input
                  :checked="item.surfaces.includes('discover')"
                  type="checkbox"
                  class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                  @change="toggleSurface(index, 'discover', ($event.target as HTMLInputElement).checked)"
                />
                工具推荐页
              </label>
              <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
                <input
                  :checked="item.surfaces.includes('overview')"
                  type="checkbox"
                  class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                  @change="toggleSurface(index, 'overview', ($event.target as HTMLInputElement).checked)"
                />
                概览页底部
              </label>
            </div>
          </fieldset>
          <label class="block">
            <span class="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-300">开始时间</span>
            <input
              :value="toLocalDateTime(item.starts_at)"
              type="datetime-local"
              class="input"
              @input="updateDate(index, 'starts_at', ($event.target as HTMLInputElement).value)"
            />
          </label>
          <label class="block">
            <span class="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-300">结束时间</span>
            <input
              :value="toLocalDateTime(item.ends_at)"
              type="datetime-local"
              class="input"
              @input="updateDate(index, 'ends_at', ($event.target as HTMLInputElement).value)"
            />
          </label>
        </div>
      </article>
    </div>

    <div
      v-else
      class="mt-4 rounded-lg border border-dashed border-gray-300 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400"
    >
      暂无推广内容，TT Switch 将自动隐藏整个入口。
    </div>
  </section>
</template>

<script setup lang="ts">
import type {
  DesktopPromotion,
  DesktopPromotionIcon,
  DesktopPromotionSurface,
} from "@/api/desktopConsole";

const props = defineProps<{
  modelValue: DesktopPromotion[];
}>();

const emit = defineEmits<{
  "update:modelValue": [items: DesktopPromotion[]];
}>();

const iconOptions: Array<{ value: DesktopPromotionIcon; label: string }> = [
  { value: "chat", label: "聊天" },
  { value: "agent", label: "Agent" },
  { value: "globe", label: "网页" },
  { value: "tool", label: "工具" },
  { value: "sparkles", label: "精选" },
  { value: "link", label: "链接" },
];

function addItem(): void {
  const suffix = Date.now().toString(36);
  emit("update:modelValue", [
    ...props.modelValue,
    {
      id: `promotion-${suffix}`,
      title: "",
      summary: "",
      target_url: "",
      cta_label: "打开",
      badge: "",
      icon: "link",
      surfaces: ["discover"],
      enabled: true,
      sort_order: props.modelValue.length * 10,
      starts_at: "",
      ends_at: "",
    },
  ]);
}

function updateItem<K extends keyof DesktopPromotion>(
  index: number,
  key: K,
  value: DesktopPromotion[K],
): void {
  emit(
    "update:modelValue",
    props.modelValue.map((item, itemIndex) =>
      itemIndex === index ? { ...item, [key]: value } : item,
    ),
  );
}

function removeItem(index: number): void {
  emit(
    "update:modelValue",
    props.modelValue.filter((_, itemIndex) => itemIndex !== index),
  );
}

function updateIcon(index: number, value: string): void {
  const option = iconOptions.find((item) => item.value === value);
  if (option) updateItem(index, "icon", option.value);
}

function normalizeID(value: string): string {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, "-")
    .replace(/^[-_]+/, "")
    .slice(0, 64);
}

function toggleSurface(
  index: number,
  surface: DesktopPromotionSurface,
  checked: boolean,
): void {
  const current = props.modelValue[index]?.surfaces ?? [];
  const next = checked
    ? Array.from(new Set([...current, surface]))
    : current.filter((value) => value !== surface);
  updateItem(index, "surfaces", next.length ? next : ["discover"]);
}

function toLocalDateTime(value?: string): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const offset = date.getTimezoneOffset() * 60_000;
  return new Date(date.getTime() - offset).toISOString().slice(0, 16);
}

function updateDate(
  index: number,
  key: "starts_at" | "ends_at",
  value: string,
): void {
  updateItem(index, key, value ? new Date(value).toISOString() : "");
}
</script>
