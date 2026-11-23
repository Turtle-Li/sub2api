<template>
  <header class="cc-topbar">
    <div class="cc-brand">
      <span class="cc-dot" aria-hidden="true"></span>

      <LiquidSelect
        ref="boardSelectRef"
        :model-value="board?.id || ''"
        :options="boardOptions"
        ariaLabel="切换画布"
        wrapper-class="cc-board-select-wrap"
        native-class="cc-board-select"
        compact
        :min-menu-width="216"
        :disabled="busy"
        :placeholder="`画布列表 (${boards.length})`"
        @select="onSelectBoard"
      />

      <input
        v-if="board"
        class="cc-title-input cc-desktop-only"
        type="text"
        maxlength="100"
        aria-label="当前画布名称"
        placeholder="输入画布名称"
        :value="title"
        :disabled="busy"
        @input="emit('rename', ($event.target as HTMLInputElement).value)"
        @keydown.enter="($event.target as HTMLInputElement).blur()"
      />

      <button
        v-if="board"
        ref="statusBtnRef"
        type="button"
        class="cc-status-badge"
        :class="[statusToneClass, { 'is-open': statusOpen, 'is-quiet': !isAlertStatus }]"
        :data-status-kind="statusKind"
        :title="statusTooltip"
        :aria-label="statusAriaLabel"
        aria-haspopup="dialog"
        :aria-expanded="statusOpen"
        :aria-controls="statusOpen ? statusPopoverId : undefined"
        @click="toggleStatusPopover"
      >
        <svg
          v-if="statusKind === 'saved'"
          class="cc-status-icon"
          width="12"
          height="12"
          viewBox="0 0 12 12"
          fill="none"
          stroke="currentColor"
          stroke-width="1.8"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M2.2 6.3 4.8 8.9 9.8 3.3" />
        </svg>
        <svg
          v-else-if="statusKind === 'saving' || statusKind === 'dirty'"
          class="cc-status-icon"
          width="10"
          height="10"
          viewBox="0 0 10 10"
          fill="currentColor"
          aria-hidden="true"
        >
          <circle cx="5" cy="5" r="2.5" />
        </svg>
        <svg
          v-else
          class="cc-status-icon"
          width="12"
          height="12"
          viewBox="0 0 12 12"
          fill="none"
          stroke="currentColor"
          stroke-width="1.6"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <circle cx="6" cy="6" r="4.5" />
          <path d="M6 3.7v2.7M6 8.4h.01" />
        </svg>

        <span class="cc-status-text"><span v-if="statusSegments.leadSr" class="cc-status-sr-mobile">{{ statusSegments.leadSr }}</span><span class="cc-status-core">{{ statusSegments.core }}</span><span v-if="statusSegments.trailSr" class="cc-status-sr-mobile">{{ statusSegments.trailSr }}</span></span>
      </button>

      <span v-if="board" class="cc-sr-only" role="status" aria-live="polite">{{ statusText }}</span>

      <button
        v-if="board && (saveErrorKind === 'offline' || saveErrorKind === 'error' || saveUnconfirmed) && !saving && !conflict"
        type="button"
        class="cc-btn is-sm cc-retry-save-btn"
        :disabled="busy"
        @click="emit('save')"
      >
        重试
      </button>
    </div>

    <div
      v-if="board && statusOpen"
      :id="statusPopoverId"
      ref="statusPopoverRef"
      class="cc-status-popover"
      role="dialog"
      aria-modal="false"
      aria-label="画布保存状态详情"
      :aria-describedby="statusPopoverDescId"
      tabindex="-1"
      @keydown.esc.stop.prevent="closeStatusPopover(true)"
    >
      <div class="cc-status-popover-head">
        <span class="cc-status-popover-pill" :class="statusToneClass">{{ statusHeadline }}</span>
        <button
          ref="statusCloseBtnRef"
          type="button"
          class="cc-status-popover-close"
          aria-label="关闭保存状态详情"
          @click="closeStatusPopover(true)"
        >
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true">
            <path d="M4 4l8 8M12 4l-8 8" />
          </svg>
        </button>
      </div>
      <p :id="statusPopoverDescId" class="cc-status-popover-desc">{{ statusDetailText }}</p>
      <dl class="cc-status-popover-meta">
        <div>
          <dt>{{ statusKind === 'saved' ? '服务端确认时间' : '上次服务端确认' }}</dt>
          <dd>{{ serverClock || '暂无时间记录' }}</dd>
        </div>
        <div>
          <dt>服务端修订号</dt>
          <dd>v{{ board.revision }}</dd>
        </div>
      </dl>
      <div v-if="statusKind !== 'saved' && statusKind !== 'saving'" class="cc-status-popover-actions">
        <button
          v-if="statusKind === 'conflict'"
          type="button"
          class="cc-btn is-sm is-primary"
          :disabled="busy || saving"
          @click="closeStatusPopover(true); emit('reloadBoard')"
        >
          重新加载最新画布
        </button>
        <template v-else>
          <button
            type="button"
            class="cc-btn is-sm is-primary"
            :disabled="!canSave"
            @click="closeStatusPopover(true); emit('save')"
          >
            {{ statusKind === 'dirty' ? '立即保存到服务端' : '重试保存' }}
          </button>
          <button
            v-if="saveUnconfirmed"
            type="button"
            class="cc-btn is-sm"
            :disabled="busy || saving"
            @click="closeStatusPopover(true); emit('reloadBoard')"
          >
            重新加载最新画布
          </button>
        </template>
      </div>
      <p class="cc-status-popover-note">仅保存到服务端画布，不写入本地浏览器存储。</p>
    </div>

    <div class="cc-top-actions">
      <div v-if="board" class="cc-search-wrap cc-desktop-only">
        <input
          ref="searchInputRef"
          v-model="searchQuery"
          type="search"
          class="cc-search-input"
          aria-label="查找画布节点"
          placeholder="查找节点 (/)"
          @focus="searchOpen = true"
          @keydown="onSearchKeydown"
        />
        <div
          v-if="searchOpen && (searchQuery.trim() || searchResults.length)"
          class="cc-search-dropdown"
          role="listbox"
          aria-label="画布节点查找结果"
        >
          <button
            v-for="(item, idx) in searchResults.slice(0, 12)"
            :key="item.id"
            type="button"
            role="option"
            class="cc-search-option"
            :class="{ 'is-highlighted': idx === highlightedIndex }"
            :aria-selected="idx === highlightedIndex"
            @mousedown.prevent="chooseSearchItem(item.id)"
            @click="chooseSearchItem(item.id)"
          >
            <span class="cc-pill" :class="kindClass(item.kind)">{{ kindShort(item.kind) }}</span>
            <span class="cc-search-option-copy">
              <strong>{{ item.title }}</strong>
              <small v-if="item.subtitle">{{ item.subtitle }}</small>
            </span>
          </button>
          <p v-if="!searchResults.length" class="cc-search-empty">没有匹配的节点</p>
        </div>
      </div>

      <button v-if="!board" type="button" class="cc-btn is-primary" :disabled="busy" @click="emit('openCreate')">
        + 新建画布
      </button>

      <template v-else>
        <button
          v-if="canCreateDraft"
          type="button"
          class="cc-btn is-primary"
          :disabled="busy"
          @click="emit('createDraft')"
        >
          {{ createDraftLabel }}
        </button>
        <button
          v-else
          type="button"
          class="cc-btn is-primary"
          :disabled="busy"
          @click="emit('addRootDraft')"
        >
          + 文字视频分镜
        </button>

        <button type="button" class="cc-btn cc-desktop-only" :disabled="busy" @click="emit('addNote')">
          + 便签
        </button>

        <button
          type="button"
          class="cc-btn cc-desktop-only cc-save-btn"
          :class="{ 'is-alert': isAlertStatus && !conflict }"
          :title="saving ? '正在保存到服务端…' : '立即保存画布 (Ctrl/Cmd+S)'"
          :aria-busy="saving ? 'true' : undefined"
          :disabled="!canSave"
          @click="emit('save')"
        >
          保存
        </button>

        <button
          type="button"
          class="cc-btn cc-desktop-only"
          :class="{ 'is-active': focusMode }"
          :aria-pressed="focusMode"
          aria-label="切换专注全屏模式"
          title="切换专注全屏模式"
          @click="emit('toggleFocus')"
        >
          {{ focusMode ? '退出专注' : '专注' }}
        </button>

        <div ref="moreWrapRef" class="cc-more-wrap">
          <button
            type="button"
            class="cc-btn"
            aria-label="更多画布操作"
            aria-haspopup="menu"
            :aria-expanded="moreOpen"
            @click="toggleMoreMenu"
          >
            更多 ▾
          </button>

          <div v-if="moreOpen" class="cc-more-menu" role="menu" aria-label="更多画布操作菜单">
            <div class="cc-more-mobile-fields cc-mobile-only">
              <div class="cc-more-status-card" :class="statusToneClass">
                <div class="cc-more-status-row">
                  <span class="cc-more-status-title">{{ statusHeadline }}</span>
                  <span class="cc-more-status-rev">v{{ board.revision }}</span>
                </div>
                <p class="cc-more-status-sub">{{ moreStatusSubtitle }}</p>
              </div>
              <label class="cc-more-field">
                <span>画布名称</span>
                <input
                  type="text"
                  maxlength="100"
                  aria-label="移动端画布名称"
                  placeholder="输入画布名称"
                  :value="title"
                  :disabled="busy"
                  @input="emit('rename', ($event.target as HTMLInputElement).value)"
                />
              </label>
              <label class="cc-more-field">
                <span>查找节点</span>
                <input
                  v-model="searchQuery"
                  type="search"
                  aria-label="移动端查找画布节点"
                  placeholder="输入节点名称或提示词…"
                />
              </label>
              <div v-if="searchQuery.trim()" class="cc-more-search-list">
                <button
                  v-for="item in searchResults.slice(0, 6)"
                  :key="item.id"
                  type="button"
                  class="cc-search-option"
                  @click="chooseSearchItem(item.id); moreOpen = false"
                >
                  <span class="cc-pill" :class="kindClass(item.kind)">{{ kindShort(item.kind) }}</span>
                  <span class="cc-search-option-copy"><strong>{{ item.title }}</strong></span>
                </button>
                <p v-if="!searchResults.length" class="cc-search-empty">没有匹配的节点</p>
              </div>
            </div>

            <button type="button" role="menuitem" class="cc-more-item cc-mobile-only" :disabled="!canSave" @click="moreOpen = false; emit('save')">
              {{ saving ? '正在保存…' : '立即保存画布' }}
            </button>
            <button type="button" role="menuitem" class="cc-more-item cc-mobile-only" :disabled="busy" @click="moreOpen = false; emit('addNote')">
              + 添加灵感便签
            </button>
            <button type="button" role="menuitem" class="cc-more-item cc-mobile-only" @click="moreOpen = false; emit('toggleFocus')">
              {{ focusMode ? '退出专注模式' : '开启专注模式' }}
            </button>

            <button type="button" role="menuitem" class="cc-more-item" :disabled="busy" @click="moreOpen = false; emit('openCreate')">
              + 新建画布
            </button>
            <button type="button" role="menuitem" class="cc-more-item" :disabled="busy || !canCompare" @click="moreOpen = false; emit('compare')">
              对比同源分支
            </button>
            <button type="button" role="menuitem" class="cc-more-item" :disabled="busy || !canUndo" @click="moreOpen = false; emit('undo')">
              撤销上一步 (⌘Z)
            </button>
            <button type="button" role="menuitem" class="cc-more-item" :disabled="busy || !canRedo" @click="moreOpen = false; emit('redo')">
              重做下一步 (⇧⌘Z)
            </button>
            <button v-if="conflict || saveUnconfirmed" type="button" role="menuitem" class="cc-more-item" :disabled="busy || saving" @click="moreOpen = false; emit('reloadBoard')">
              重新加载最新画布
            </button>
            <hr class="cc-more-sep" />
            <button type="button" role="menuitem" class="cc-more-item is-danger" :disabled="busy || saving" @click="moreOpen = false; emit('deleteBoard')">
              删除当前画布
            </button>
          </div>
        </div>
      </template>
    </div>
  </header>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import LiquidSelect, { type LiquidSelectOption } from '@/features/studio-ui/LiquidSelect.vue'
import type { CanvasNodeKind, CanvasSummary, CreativeCanvas } from './types'
import type { CanvasSaveErrorKind } from './useCanvasEditor'

export interface SearchableNodeEntry {
  readonly id: string
  readonly kind: CanvasNodeKind
  readonly title: string
  readonly subtitle?: string
}

const clockFormat = new Intl.DateTimeFormat('zh-CN', {
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
})

function formatServerClock(iso: string | undefined): string {
  if (!iso) return ''
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  return clockFormat.format(date)
}

const props = withDefaults(defineProps<{
  boards: readonly CanvasSummary[]
  board: CreativeCanvas | null
  title: string
  dirty: boolean
  busy: boolean
  saving?: boolean
  conflict: boolean
  saveErrorKind?: CanvasSaveErrorKind
  saveUnconfirmed?: boolean
  lastSavedAt?: string
  hasSavedInSession?: boolean
  focusMode?: boolean
  canUndo: boolean
  canRedo: boolean
  canCreateDraft: boolean
  createDraftLabel?: string
  canCompare: boolean
  searchableNodes?: readonly SearchableNodeEntry[]
}>(), {
  saving: false,
  saveErrorKind: null,
  saveUnconfirmed: false,
  lastSavedAt: '',
  hasSavedInSession: false,
  focusMode: false,
  createDraftLabel: '+ 续接下一镜',
  searchableNodes: () => [],
})

const emit = defineEmits<{
  selectBoard: [id: string]
  openCreate: []
  rename: [title: string]
  addNote: []
  createDraft: []
  addRootDraft: []
  compare: []
  undo: []
  redo: []
  save: []
  reloadBoard: []
  toggleFocus: []
  deleteBoard: []
  locateNode: [nodeId: string]
}>()

const statusPopoverId = `cc-status-popover-${useId()}`
const statusPopoverDescId = `cc-status-popover-desc-${useId()}`
const boardSelectRef = ref<InstanceType<typeof LiquidSelect>>()
const searchInputRef = ref<HTMLInputElement>()
const statusBtnRef = ref<HTMLButtonElement>()
const statusPopoverRef = ref<HTMLElement>()
const statusCloseBtnRef = ref<HTMLButtonElement>()
const moreWrapRef = ref<HTMLElement>()
const searchQuery = ref('')
const searchOpen = ref(false)
const statusOpen = ref(false)
const moreOpen = ref(false)
const highlightedIndex = ref(0)

const boardOptions = computed<LiquidSelectOption[]>(() => [
  { value: '', label: `画布列表 (${props.boards.length})` },
  ...props.boards.map(item => ({
    value: item.id,
    label: item.title || '未命名画布',
    meta: `v${item.revision}`,
  })),
])

const searchResults = computed(() => {
  const q = searchQuery.value.trim().toLowerCase()
  if (!q) return props.searchableNodes
  return props.searchableNodes.filter(item =>
    `${item.title} ${item.subtitle || ''} ${kindShort(item.kind)}`.toLowerCase().includes(q),
  )
})

watch(searchQuery, val => {
  highlightedIndex.value = 0
  if (val.trim()) {
    searchOpen.value = true
  }
})

watch(() => props.board?.id, () => {
  statusOpen.value = false
  moreOpen.value = false
})

watch(
  statusOpen,
  open => {
    if (!open) return
    const focusTarget = statusCloseBtnRef.value ?? statusPopoverRef.value
    focusTarget?.focus({ preventScroll: true })
  },
  { flush: 'post' },
)

type TopbarStatusKind = 'conflict' | 'saving' | 'offline' | 'error' | 'unconfirmed' | 'dirty' | 'saved'

const serverClock = computed(() => formatServerClock(props.lastSavedAt || props.board?.updated_at))

const canSave = computed(
  () =>
    Boolean(props.board) &&
    !props.busy &&
    !props.saving &&
    !props.conflict &&
    (props.dirty || props.saveUnconfirmed),
)

const statusKind = computed<TopbarStatusKind>(() => {
  if (props.conflict) return 'conflict'
  if (props.saving || props.busy) return 'saving'
  if (props.saveErrorKind === 'offline') return 'offline'
  if (props.saveErrorKind === 'error') return 'error'
  if (props.saveUnconfirmed) return 'unconfirmed'
  if (props.dirty) return 'dirty'
  return 'saved'
})

const isAlertStatus = computed(
  () =>
    props.conflict ||
    props.saveErrorKind === 'offline' ||
    props.saveErrorKind === 'error' ||
    props.saveUnconfirmed,
)

const statusSegments = computed<{ leadSr: string; core: string; trailSr: string }>(() => {
  switch (statusKind.value) {
    case 'conflict':
      return { leadSr: '此画布在', core: '另一处更新', trailSr: ' · 本次修改已保留' }
    case 'saving':
      return { leadSr: '正在', core: '保存…', trailSr: '' }
    case 'offline':
      return { leadSr: '', core: '离线', trailSr: ' · 尚未同步' }
    case 'error':
      return { leadSr: '保存', core: '失败', trailSr: ' · 尚未同步' }
    case 'unconfirmed':
      return { leadSr: '保存', core: '未确认', trailSr: ' · 尚未同步' }
    case 'dirty':
      return { leadSr: '有改动', core: '待保存', trailSr: '' }
    default: {
      const prefix = props.hasSavedInSession ? '已自动保存' : '已保存'
      const core = serverClock.value || `v${props.board?.revision ?? 0}`
      return { leadSr: `${prefix} · `, core, trailSr: '' }
    }
  }
})

const statusText = computed(() => `${statusSegments.value.leadSr}${statusSegments.value.core}${statusSegments.value.trailSr}`)

const statusHeadline = computed(() => {
  switch (statusKind.value) {
    case 'conflict':
      return '此画布在另一处更新'
    case 'saving':
      return '正在保存到服务端…'
    case 'offline':
      return '离线 · 尚未同步'
    case 'error':
      return '保存失败 · 尚未同步'
    case 'unconfirmed':
      return '保存未确认 · 尚未同步'
    case 'dirty':
      return '有改动待保存'
    default:
      return props.hasSavedInSession ? '已自动保存到服务端' : '已与服务端同步'
  }
})

const statusDetailText = computed(() => {
  switch (statusKind.value) {
    case 'conflict':
      return '服务端版本已被另一处更新（或包含先前未确认的保存），本次本地修改已保留且未覆盖服务端。'
    case 'saving':
      return '正在将当前画布改动同步到服务端，请等待确认。'
    case 'offline':
      return props.saveUnconfirmed
        ? '连接中断或当前离线，服务端是否已保存先前改动尚未确认；本地内容已保留，恢复联网后可重试核验或重新加载。'
        : '当前网络不可用，本次改动尚未写入服务端，恢复联网后可自动或手动重试。'
    case 'error':
      return props.saveUnconfirmed
        ? '先前保存请求未收到服务端确认，服务端内容可能已变化；本地内容已保留，可重试核验或重新加载。'
        : '服务端尚未确认本次改动，可点击下方按钮重试保存。'
    case 'unconfirmed':
      return '先前保存请求未收到服务端确认，当前内容尚未确认与服务端一致；重试将沿用原修订号核验，不会静默覆盖其他会话。'
    case 'dirty':
      return '本次改动尚未同步到服务端，停顿约 1.5 秒会自动保存。'
    default:
      return props.hasSavedInSession
        ? '当前画布内容已由服务端确认保存。'
        : '当前内容与服务端最新修订一致。'
  }
})

const moreStatusSubtitle = computed(() => {
  const clock = serverClock.value
  if (statusKind.value === 'saved') {
    return clock ? `服务端确认时间 ${clock}` : '已与服务端同步'
  }
  return clock ? `本次尚未同步 · 上次服务端确认 ${clock}` : '本次改动尚未同步到服务端'
})

const statusToneClass = computed(() => ({
  'is-conflict':
    props.conflict ||
    props.saveErrorKind === 'offline' ||
    props.saveErrorKind === 'error' ||
    props.saveUnconfirmed,
  'is-dirty':
    !props.conflict &&
    !props.saveErrorKind &&
    !props.saveUnconfirmed &&
    (props.dirty || props.saving),
}))

const statusTooltip = computed(() => {
  const rev = props.board ? `服务端修订号 v${props.board.revision}` : ''
  const clock = serverClock.value
    ? (statusKind.value === 'saved' ? `服务端时间 ${serverClock.value}` : `上次服务端确认 ${serverClock.value}`)
    : ''
  return [statusText.value, rev, clock].filter(Boolean).join(' · ')
})

const statusAriaLabel = computed(() => `${statusText.value}，查看保存状态详情`)

function toggleStatusPopover() {
  const next = !statusOpen.value
  statusOpen.value = next
  if (next) {
    boardSelectRef.value?.close(false)
    moreOpen.value = false
    searchOpen.value = false
  }
}

function closeStatusPopover(restoreFocus: boolean) {
  if (!statusOpen.value) return
  statusOpen.value = false
  if (restoreFocus) {
    statusBtnRef.value?.focus({ preventScroll: true })
  }
}

function toggleMoreMenu() {
  const next = !moreOpen.value
  moreOpen.value = next
  if (next) {
    boardSelectRef.value?.close(false)
    statusOpen.value = false
    searchOpen.value = false
  }
}

function kindShort(kind: CanvasNodeKind): string {
  if (kind === 'image') return '原图'
  if (kind === 'video') return '视频'
  if (kind === 'video-draft') return '分镜'
  return '便签'
}

function kindClass(kind: CanvasNodeKind): string {
  if (kind === 'image') return 'is-img'
  if (kind === 'video') return 'is-vid'
  if (kind === 'video-draft') return 'is-draft'
  return ''
}

function chooseSearchItem(nodeId: string) {
  searchQuery.value = ''
  searchOpen.value = false
  searchInputRef.value?.blur()
  emit('locateNode', nodeId)
}

function onSearchKeydown(event: KeyboardEvent) {
  const visible = searchResults.value.slice(0, 12)
  if (event.key === 'Escape') {
    event.preventDefault()
    event.stopPropagation()
    searchOpen.value = false
    searchQuery.value = ''
    searchInputRef.value?.blur()
    return
  }
  if (event.key === 'ArrowDown') {
    if (!visible.length) return
    event.preventDefault()
    searchOpen.value = true
    highlightedIndex.value = (highlightedIndex.value + 1) % visible.length
    return
  }
  if (event.key === 'ArrowUp') {
    if (!visible.length) return
    event.preventDefault()
    searchOpen.value = true
    highlightedIndex.value = (highlightedIndex.value - 1 + visible.length) % visible.length
    return
  }
  if (event.key === 'Enter' && visible.length) {
    event.preventDefault()
    const picked = visible[highlightedIndex.value] ?? visible[0]
    if (picked) chooseSearchItem(picked.id)
  }
}

function onOutsidePointerDown(event: PointerEvent) {
  const target = event.target as HTMLElement | null
  if (searchOpen.value && !target?.closest('.cc-search-wrap')) {
    searchOpen.value = false
  }
  if (statusOpen.value && !target?.closest('.cc-status-badge') && !target?.closest('.cc-status-popover')) {
    statusOpen.value = false
  }
  if (moreOpen.value && !target?.closest('.cc-more-wrap')) {
    moreOpen.value = false
  }
}

function closeMenus(): boolean {
  if (boardSelectRef.value?.isOpen) {
    boardSelectRef.value.close(true)
    return true
  }
  if (statusOpen.value) {
    closeStatusPopover(true)
    return true
  }
  if (moreOpen.value) {
    moreOpen.value = false
    return true
  }
  if (searchOpen.value) {
    searchOpen.value = false
    return true
  }
  return false
}

function focusSearch() {
  boardSelectRef.value?.close(false)
  statusOpen.value = false
  moreOpen.value = false
  searchOpen.value = true
  searchInputRef.value?.focus()
  searchInputRef.value?.select()
}

function onSelectBoard(nextId: string) {
  emit('selectBoard', nextId)
}

onMounted(() => window.addEventListener('pointerdown', onOutsidePointerDown))
onBeforeUnmount(() => window.removeEventListener('pointerdown', onOutsidePointerDown))

defineExpose({ focusSearch, closeMenus })
</script>

<style scoped>
.cc-board-select-wrap {
  width: auto;
  max-width: 184px;
  min-width: 0;
  flex: 0 1 auto;
}
</style>
