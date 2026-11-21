<template>
  <aside v-show="!mobile" ref="rail" class="vs-rail" aria-labelledby="vs-compose-title"></aside>
  <dialog id="vs-composer" ref="sheet" class="vs-sheet" aria-labelledby="vs-compose-title" @close="restoreFocus" @keydown.esc="close"></dialog>
  <Teleport v-if="rail && sheet" :to="mobile ? sheet : rail">
    <form class="vs-form" novalidate @submit.prevent="submit">
      <div class="vs-form-head">
        <div><h2 id="vs-compose-title" ref="heading" tabindex="-1">创作设置</h2><p>按所选模型与服务渠道能力动态适配</p></div>
        <button v-if="mobile" type="button" class="vs-icon" aria-label="关闭创作栏" @click="close"><Icon name="x" size="sm" aria-hidden="true" /></button>
      </div>
      <div class="vs-fields">
        <div v-if="loading && !model" class="vs-hint" role="status">正在读取可用模型…</div>
        <div v-if="availability" class="vs-notice" role="status">
          <strong>{{ availability }}</strong><p>{{ catalogError ? '未取得有效报价，暂不能生成。' : '已有视频仍可查看。需要开通视频权限时，请联系管理员。' }}</p>
          <button class="vs-text" type="button" :disabled="busy || loading" @click="loadModels">重新读取模型</button>
        </div>
        <div v-if="canvasVerifying" class="vs-hint vs-canvas-status" role="status">
          <strong>正在验证画布草稿…</strong>
          <p>正在核对登录账号、画布修订号、草稿内容与关联原图授权，核对完成前不可提交。</p>
        </div>
        <div v-else-if="canvasErrorBanner" class="vs-notice vs-canvas-error" role="alert">
          <strong>{{ canvasErrorTitle }}</strong>
          <p>{{ canvasErrorBanner.message }}</p>
          <div class="vs-actions">
            <button v-if="canvasErrorBanner.retryable" type="button" class="vs-button" :disabled="busy" @click="emit('retryCanvasDraft')">重新验证</button>
            <button type="button" class="vs-text" :disabled="busy || !!heldId" @click="clearDraft">离开画布草稿</button>
          </div>
        </div>
        <div v-else-if="canvasReady && !draftCleared" class="vs-draft" role="status">
          <div>
            <strong>来自画布草稿 · 尚未提交</strong>
            <div class="vs-draft-actions">
              <button v-if="canReturnCanvas" type="button" class="vs-text" :disabled="busy" @click="emit('returnCanvas')">返回画布</button>
              <button type="button" class="vs-text" :disabled="busy || !!heldId" @click="clearDraft">清除草稿</button>
            </div>
          </div>
          <p class="vs-canvas-meta">画布「{{ canvasReady.canvasTitle }}」· {{ canvasReady.draftTitle }}（v{{ canvasReady.revision }}）</p>
          <p>{{ canvasReady.hasSourceImage ? '已通过私有工作台核验并恢复关联原图，不会自动生成。请确认模型、素材用途与报价。' : '已核验画布草稿与提示词，不会自动生成。请确认模型与报价。' }}</p>
          <p v-if="canvasReady.unavailableAttempt" class="vs-error" role="alert">该草稿曾关联的私有任务已过期或授权已失效，无法继续读取旧结果。</p>
        </div>
        <div v-else-if="!canvasActive && source && !draftCleared" class="vs-draft" role="status">
          <div><strong>来自生图草稿 · 尚未提交</strong><button type="button" class="vs-text" :disabled="busy || !!heldId" @click="clearDraft">清除草稿</button></div>
          <p>已带入本人原图，不会自动生成。请确认模型、素材用途与报价。</p>
        </div>
        <fieldset class="vs-controls" :disabled="busy || !!heldId || loading || canvasBlocked">
          <div v-if="model || models.length" class="vs-field">
            <span class="vs-label-row"><span>视频模型与服务渠道</span><small>点击切换</small></span>
            <button type="button" class="vs-model-trigger" aria-label="选择视频模型与渠道" aria-haspopup="dialog" @click="openModelPicker">
              <span class="vs-picker-badge" aria-hidden="true">{{ model?.label.slice(0, 1) }}</span>
              <span class="vs-model-trigger-copy">
                <strong>{{ model?.label }}</strong>
                <small v-if="model">{{ providerLabel(model) }} · {{ durationSummary(model) }} · {{ materialSummary(model) }}</small>
              </span>
              <span class="vs-model-trigger-chevron" aria-hidden="true">›</span>
            </button>
          </div>
          <div v-if="reconcileNotice" class="vs-notice vs-reconcile" role="status">
            <span>{{ reconcileNotice }}</span>
            <button type="button" class="vs-text" aria-label="关闭参数适配提示" @click="reconcileNotice = ''"><Icon name="x" size="xs" aria-hidden="true" /></button>
          </div>
          <label class="vs-field"><span class="vs-label-row"><span>画面与运镜描述</span><small>{{ promptBytes }} / 8192 字节</small></span>
            <textarea ref="promptInput" v-model="prompt" rows="4" aria-label="画面与运动" :aria-invalid="tooLong"
              placeholder="描述画面主体、镜头运动、光影节奏与氛围…" />
          </label>
          <p v-if="tooLong" class="vs-error" role="alert">描述超过 8192 字节，请缩短文字。</p>
          <VideoMaterials v-if="model && supportsMaterials" ref="materials" :model="model" :source="draftCleared ? null : source" :user-id="userId" :disabled="busy || !!heldId" @choose="choose" />
          <div v-if="model && isAutomatic(model)" class="vs-field">
            <span class="vs-label-row"><span>视频时长</span><small>无需选择</small></span>
            <div class="vs-auto-duration">
              <strong>渠道自动</strong>
              <span>当前模型不支持自定义时长，最终长度以实际成片为准。</span>
            </div>
          </div>
          <div v-else-if="model && activeBounds" class="vs-field">
            <span class="vs-label-row"><span>请求时长</span><output class="vs-range-value">{{ seconds }} 秒</output></span>
            <div class="vs-range-wrap">
              <input v-model.number="seconds" class="vs-range" type="range" aria-label="请求时长" :min="activeBounds.min" :max="activeBounds.max" step="1" />
              <div class="vs-range-ticks" aria-hidden="true"><span>{{ activeBounds.min }} 秒</span><span>{{ activeBounds.max }} 秒</span></div>
            </div>
          </div>
          <div v-if="model?.sizes?.length" class="vs-field">
            <span class="vs-label-row">
              <span>{{ model.provider === 'muse' ? '画幅比例' : '提交尺寸' }}</span>
              <small>{{ model.provider === 'muse' ? '创作意图，不保证成片尺寸' : '像素规格' }}</small>
            </span>
            <div class="vs-choice-grid" role="group" aria-label="画幅或提交尺寸">
              <button type="button" :aria-pressed="!size && !nativeSize.trim()" @click="pickAutoSize">渠道自动</button>
              <button v-for="option in model.sizes" :key="option" type="button" :aria-pressed="size === option" @click="pickSize(option)">{{ formatSize(option) }}</button>
            </div>
          </div>
          <template v-if="model">
            <div v-if="hasSeed || model.muse_options" class="vs-channel-params">
              <div class="vs-label-row vs-channel-params-head"><span>渠道可配参数</span><small>按服务端能力显示</small></div>
              <div v-if="hasSeed" class="vs-field"><span class="vs-label-row"><span>随机种子（Seed）</span><small>0–2147483647，留空随机</small></span>
                <div class="vs-number-row"><input v-model="seed" aria-label="随机种子" type="text" inputmode="numeric" placeholder="留空则随机" /><button type="button" class="vs-button" @click="randomSeed">随机</button></div>
              </div>
              <template v-if="model.muse_options">
                <label class="vs-field"><span class="vs-label-row"><span>原生画布意图</span><small>与画幅比例互斥</small></span><input v-model="nativeSize" aria-label="原生画布意图" type="text" placeholder="选填，如 1280x720，由渠道解释" @input="onNativeSizeInput" /></label>
                <label class="vs-field"><span class="vs-label-row"><span>画质文字意图</span><small>选填</small></span><input v-model="resolution" aria-label="画质文字意图" type="text" placeholder="选填，不代表计费分辨率" /></label>
                <label class="vs-field"><span class="vs-label-row"><span>原生任务等待上限</span><small>1–{{ model.muse_options.timeout_max }} 秒，留空自动</small></span><input v-model="timeout" aria-label="原生任务等待上限" type="text" inputmode="numeric" placeholder="留空由渠道决定" /></label>
                <label class="vs-field"><span class="vs-label-row"><span>补充描述</span><small>{{ extraBytes }} / {{ model.muse_options.extra_max_bytes }} 字节</small></span><textarea v-model="extra" rows="3" aria-label="补充描述" placeholder="选填；补充创作意图，不保证上游全部满足" /></label>
              </template>
            </div>
            <p v-if="hasSeed && !seedValid" class="vs-error" role="alert">请输入 0 至 2147483647 之间的整数。</p>
            <p v-if="!advancedValid" class="vs-error" role="alert">{{ advancedError }}</p>
          </template>
        </fieldset>
      </div>
      <footer class="vs-submit">
        <div v-if="heldId && !busy" class="vs-receipt" role="status">
          <strong>{{ unconfirmed ? '未收到服务端回执 · 已锁定原任务号' : failed ? '本次生成失败' : '已提交本次创作' }}</strong>
          <p class="vs-hint">{{ unconfirmed ? '请勿重发。只能凭原任务号核对结果；明确分类后才能新建。' : failed ? '已查询原任务，未自动重发。' : '查看原任务不需要再次生成。' }}</p>
          <code>{{ heldId }}</code>
          <div class="vs-actions">
            <button class="vs-button" type="button" @click="emit('lookup', heldId)">查询原任务</button>
            <button v-if="canReturnCanvas" class="vs-text" type="button" @click="emit('returnCanvas')">返回画布</button>
            <button v-if="!unconfirmed" class="vs-text" type="button" @click="freshDraft">另起新草稿</button>
          </div>
        </div>
        <template v-else>
          <div v-if="notice" class="vs-notice" role="alert">{{ notice }}</div>
          <div class="vs-price" aria-live="polite"><span>{{ model?.provider === 'goodput' ? '最高冻结' : '预计消耗' }}</span><strong>{{ amount === null ? '报价不可用' : `${amount} ${model?.currency}` }}</strong></div>
          <div v-if="model && enabled && eligible && !catalogError && amount === null" class="vs-hint">金额或币种不完整，暂不能生成。<button type="button" class="vs-text" :disabled="busy || loading" @click="loadModels">刷新报价</button></div>
          <p v-else class="vs-hint">{{ model?.provider === 'goodput' ? '按核实的秒数结算，多余冻结将释放。' : model ? '按当前渠道默认档报价，首次明确完成时结算；最终时长与画幅以成片为准。' : '以当前账号的可用报价为准。' }}</p>
          <button class="vs-primary" type="submit" :disabled="!canSubmit"><Icon name="play" size="sm" aria-hidden="true" />{{ phase || '确认生成视频' }}</button>
        </template>
      </footer>
    </form>
  </Teleport>
  <VideoModelPicker ref="picker" :models="models" :mobile="mobile" @choose="requestModel" />
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { useAuthStore } from '@/stores/auth'
import { creativeCanvasApi } from '@/features/creative-canvas/api'
import { videoDurationMode, videoStudio, type VideoModel, type VideoJob, type VideoSubmission } from '@/features/video-studio/api'
import type { pendingImage } from '@/features/video-studio/source'
import { formatCanvasBindError, type CanvasDraftState } from './canvasDraft'
import VideoMaterials from './VideoMaterials.vue'
import VideoModelPicker from './VideoModelPicker.vue'
import './composer.css'

const props = withDefaults(defineProps<{
  userId: number
  source: ReturnType<typeof pendingImage>
  mobile: boolean
  heldId: string
  unconfirmed: boolean
  failed: boolean
  example: string
  reserve: (id: string) => void
  canvasDraft?: CanvasDraftState
  canReturnCanvas?: boolean
}>(), {
  canvasDraft: () => ({ status: 'none' }),
  canReturnCanvas: false,
})
const emit = defineEmits<{
  accepted: [job: VideoJob]
  rejected: [id: string]
  lookup: [id: string]
  newDraft: []
  clearCanvasDraft: []
  retryCanvasDraft: []
  returnCanvas: []
  canvasConflict: [message: string]
}>()
const auth = useAuthStore()
const sameOwner = () => auth.isAuthenticated && auth.user?.id === props.userId
const rail = ref<HTMLElement>()
const sheet = ref<HTMLDialogElement>()
const heading = ref<HTMLElement>()
const promptInput = ref<HTMLTextAreaElement>()
const materials = ref<InstanceType<typeof VideoMaterials>>()
const picker = ref<InstanceType<typeof VideoModelPicker>>()
const canvasActive = computed(() => props.canvasDraft.status !== 'none')
const canvasReady = computed(() => props.canvasDraft.status === 'ready' ? props.canvasDraft : null)
const canvasVerifying = computed(() => props.canvasDraft.status === 'verifying')
const canvasBlocked = computed(() => canvasActive.value && props.canvasDraft.status !== 'ready')
const canvasErrorBanner = computed(() => (
  props.canvasDraft.status === 'invalid' ||
  props.canvasDraft.status === 'source-unavailable' ||
  props.canvasDraft.status === 'conflict' ||
  props.canvasDraft.status === 'error'
) ? props.canvasDraft : null)
const canvasErrorTitle = computed(() => {
  const status = canvasErrorBanner.value?.status
  if (status === 'source-unavailable') return '画布来源原图不可用'
  if (status === 'conflict') return '画布版本已变更'
  if (status === 'invalid') return '画布草稿无效'
  return '无法验证画布草稿'
})
const prompt = ref(props.canvasDraft.status === 'ready' ? props.canvasDraft.prompt : (!canvasActive.value && props.source ? props.example : ''))
const draftCleared = ref(false)
const model = shallowRef<VideoModel | null>(null)
const models = shallowRef<VideoModel[]>([])
const enabled = ref(false)
const loading = ref(true)
const catalogError = ref(false)
const seconds = ref(0)
const size = ref('')
const seed = ref('')
const nativeSize = ref('')
const resolution = ref('')
const timeout = ref('')
const extra = ref('')
const notice = ref('')
const reconcileNotice = ref('')
const phase = ref('')
const busy = computed(() => !!phase.value)
const lifetime = new AbortController()
const { signal } = lifetime
let opener: HTMLElement | undefined

const isAutomatic = (m: VideoModel | null) => !!m && videoDurationMode(m) === 'automatic'
function durationBounds(m: VideoModel): { min: number; max: number } | null {
  if (isAutomatic(m)) return null
  let min = m.min_seconds
  let max = m.max_seconds
  if (m.id === 'minimax/h3-fast') { min = Math.max(5, min); max = Math.min(14, max) }
  else if (m.id === 'minimax/h3') { min = Math.max(4, min); max = Math.min(15, max) }
  return max >= min ? { min, max } : null
}
function secondsOptions(m: VideoModel) {
  const bounds = durationBounds(m)
  if (!bounds) return []
  return Array.from({ length: bounds.max - bounds.min + 1 }, (_, i) => bounds.min + i)
}

const activeBounds = computed(() => model.value ? durationBounds(model.value) : null)
const eligible = computed(() => models.value.some(m => m.id === model.value?.id))
const availability = computed(() => catalogError.value ? '暂时无法读取模型' : loading.value ? '' : !enabled.value ? '新建视频已暂停' : !models.value.length ? '暂无可用的视频模型' : !eligible.value ? '所选模型暂不可用，请重新选择' : '')
const hasSeed = computed(() => !!model.value?.supports_seed)
const supportsMaterials = computed(() => !!model.value && (model.value.supports_frames || model.value.max_references > 0))
const rawSeed = computed(() => String(seed.value ?? '').trim())
const rawTimeout = computed(() => String(timeout.value ?? '').trim())
const seedValid = computed(() => !hasSeed.value || rawSeed.value === '' || (/^\d+$/.test(rawSeed.value) && Number(rawSeed.value) <= 2147483647))
const bytes = (value: string) => new TextEncoder().encode(value).byteLength
const promptBytes = computed(() => bytes(prompt.value.trim()))
const extraBytes = computed(() => bytes(extra.value))
const tooLong = computed(() => promptBytes.value > 8192)
const advancedError = computed(() => {
  const options = model.value?.muse_options
  if (!options) return ''
  if (bytes(nativeSize.value) > options.native_size_max_bytes) return `原生画布意图不能超过 ${options.native_size_max_bytes} 字节。`
  if (bytes(resolution.value) > options.resolution_max_bytes) return `画质文字意图不能超过 ${options.resolution_max_bytes} 字节。`
  if (extraBytes.value > options.extra_max_bytes) return `补充描述不能超过 ${options.extra_max_bytes} 字节。`
  if (rawTimeout.value !== '' && (!/^\d+$/.test(rawTimeout.value) || Number(rawTimeout.value) < 1 || Number(rawTimeout.value) > options.timeout_max)) {
    return `任务等待上限须为 1–${options.timeout_max} 秒的整数，留空由渠道决定。`
  }
  if (nativeSize.value.trim() && size.value) return '原生画布意图与画幅比例只能选择一项。'
  return ''
})
const advancedValid = computed(() => !advancedError.value)
const quoteSeconds = (m: VideoModel | null) => !m ? 0 : isAutomatic(m) ? m.default_seconds : seconds.value
const price = (m: VideoModel | null, s: number) => {
  const n = m?.quotes?.[s]
  return m?.quote_id?.trim() && (m.currency === 'USD' || m.currency === 'CNY') && typeof n === 'number' && Number.isFinite(n) && n > 0 ? n : null
}
const amount = computed(() => enabled.value && eligible.value && !catalogError.value ? price(model.value, quoteSeconds(model.value)) : null)
const canSubmit = computed(() => !props.heldId && !busy.value && !loading.value && !canvasBlocked.value && !!prompt.value.trim() && !tooLong.value && amount.value !== null && seedValid.value && advancedValid.value && (!supportsMaterials.value || materials.value?.ready))

const providerLabel = (m: VideoModel) => m.provider === 'muse' ? 'Muse 服务渠道' : 'GoodPut H3 服务渠道'
function durationSummary(m: VideoModel) {
  if (isAutomatic(m)) return '时长自动'
  const bounds = durationBounds(m)
  return bounds ? `${bounds.min}–${bounds.max} 秒` : '时长不可用'
}
function materialSummary(m: VideoModel) {
  if (m.supports_frames) return '首尾帧 / 有序参考'
  if (m.max_references > 0) return `最多 ${Math.min(10, m.max_references)} 个有序参考`
  return '纯文字生成'
}
const formatSize = (value: string) => value.replace(/x/i, ' × ')

function choose(m: VideoModel) {
  const previous = model.value
  const different = previous?.id !== m.id
  const adjustments: string[] = []
  model.value = m
  if (isAutomatic(m)) {
    if (previous && !isAutomatic(previous) && seconds.value > 0) adjustments.push('时长改为渠道自动')
    seconds.value = 0
  } else {
    const options = secondsOptions(m)
    if (!options.includes(seconds.value)) {
      const hadSelectable = !!previous && !isAutomatic(previous) && seconds.value > 0
      seconds.value = options.includes(m.default_seconds) ? m.default_seconds : options[0] || 0
      if (hadSelectable) adjustments.push(`时长调整为 ${seconds.value} 秒`)
    }
  }
  if (!m.muse_options) {
    if (nativeSize.value || resolution.value || timeout.value || extra.value) adjustments.push('不支持的渠道扩展参数已清除')
    nativeSize.value = ''; resolution.value = ''; timeout.value = ''; extra.value = ''
  }
  if (!previous || different) {
    if (!m.sizes?.includes(size.value)) {
      size.value = m.sizes?.[0] || ''
      if (previous) adjustments.push('画幅/尺寸已按新能力重置')
    }
  } else if (size.value && !m.sizes?.includes(size.value)) {
    size.value = m.sizes?.[0] || ''
    adjustments.push('画幅/尺寸已按新能力重置')
  }
  if (!m.supports_seed) { if (seed.value) adjustments.push('Seed 已清除'); seed.value = '' }
  if (different) notice.value = ''
  if (adjustments.length) reconcileNotice.value = `已按新渠道规则调整：${adjustments.join('；')}。`
}
function requestModel(target: VideoModel) {
  if (materials.value) {
    materials.value.chooseModel(target)
    return
  }
  choose(target)
}
function openModelPicker(event: MouseEvent) { if (model.value) picker.value?.open(model.value, event.currentTarget as HTMLElement) }
function pickAutoSize() { size.value = ''; nativeSize.value = '' }
function pickSize(value: string) { size.value = value; if (value) nativeSize.value = '' }
function onNativeSizeInput() { if (nativeSize.value.trim()) size.value = '' }
function randomSeed() {
  const values = new Uint32Array(1); crypto.getRandomValues(values)
  seed.value = String(values[0] % 2147483648)
}
function directory(result: Awaited<ReturnType<typeof videoStudio.models>>) {
  enabled.value = result.enabled
  models.value = (result.items || []).filter(m => m.provider === 'muse' ? m.id === 'muse-video' : m.provider === 'goodput' && ['minimax/h3', 'minimax/h3-fast'].includes(m.id))
  catalogError.value = false
  const selected = model.value ? models.value.find(m => m.id === model.value?.id) : models.value[0]
  if (selected) choose(selected)
}
async function loadModels() {
  loading.value = true
  try { const result = await videoStudio.models(signal); if (!signal.aborted) directory(result) }
  catch { if (!signal.aborted) catalogError.value = true }
  finally { if (!signal.aborted) loading.value = false }
}
function snapshot(m: VideoModel) {
  const qSec = quoteSeconds(m)
  return JSON.stringify([
    m.id, m.quote_id, m.currency, videoDurationMode(m), price(m, qSec), qSec, size.value,
    m.supports_frames, m.supports_seed, m.max_references, m.reference_kinds || null, m.muse_options || null,
    rawSeed.value, nativeSize.value.trim(), resolution.value.trim(), rawTimeout.value, extra.value.trim(),
  ])
}
function errorInfo(error: unknown) {
  const e = error as { status?: number; response?: { status?: number; data?: { message?: string } }; message?: string; code?: string; reason?: string } | null
  return { status: e?.status || e?.response?.status || 0, message: e?.response?.data?.message || e?.message || '', code: e?.reason || e?.code || '' }
}
async function submit() {
  if (!sameOwner() || !canSubmit.value || !model.value || (supportsMaterials.value && !materials.value)) return
  const activeCanvas = canvasReady.value && !draftCleared.value ? canvasReady.value : null
  const seen = snapshot(model.value)
  let sentId = ''
  notice.value = ''; phase.value = '正在核对报价…'
  try {
    await loadModels()
    signal.throwIfAborted()
    if (canvasBlocked.value) { notice.value = '画布草稿状态已变化，暂不能提交。'; return }
    if (availability.value || amount.value === null) { notice.value = '未提交。当前无法确认此模型的授权或报价。'; return }
    if (snapshot(model.value) !== seen || (supportsMaterials.value && !materials.value?.ready)) { notice.value = '报价、通道或可用设置已变化。请核对新设置，再次确认生成。'; return }
    phase.value = '正在检查素材…'
    const images = supportsMaterials.value ? await materials.value!.prepare(signal) : {}
    signal.throwIfAborted()
    const muse = model.value.muse_options
    const body: VideoSubmission = {
      request_id: crypto.randomUUID(),
      model: model.value.id,
      quote_id: model.value.quote_id,
      prompt: prompt.value.trim(),
      ...(isAutomatic(model.value) ? {} : { seconds: seconds.value }),
      ...(size.value ? { size: size.value } : {}),
      ...(hasSeed.value && rawSeed.value !== '' ? { seed: Number(rawSeed.value) } : {}),
      ...images,
      ...(muse && nativeSize.value.trim() ? { native_size: nativeSize.value.trim() } : {}),
      ...(muse && resolution.value.trim() ? { resolution: resolution.value.trim() } : {}),
      ...(muse && rawTimeout.value !== '' ? { timeout: Number(rawTimeout.value) } : {}),
      ...(muse && extra.value.trim() ? { extra: extra.value.trim() } : {}),
    }
    const h3 = model.value.provider === 'goodput'
    const wireSize = new Blob([JSON.stringify(body).replace(/[<>&\u2028\u2029]/g, c => `\\u${c.charCodeAt(0).toString(16).padStart(4, '0')}`)]).size
    if (wireSize > (h3 ? 8 : 24) * 1024 * 1024) { notice.value = `编码后的请求超过 ${h3 ? 8 : 24} MiB，请缩小或减少素材。未提交。`; return }
    if (!sameOwner()) return
    if (activeCanvas) {
      const current = canvasReady.value
      if (
        !current ||
        draftCleared.value ||
        current.canvasId !== activeCanvas.canvasId ||
        current.nodeId !== activeCanvas.nodeId ||
        current.revision !== activeCanvas.revision
      ) {
        notice.value = '画布草稿已离开或变更，本次未提交视频任务。'
        return
      }
      phase.value = '正在绑定画布草稿…'
      try {
        await creativeCanvasApi.bindAttempt(
          activeCanvas.canvasId,
          activeCanvas.nodeId,
          body.request_id,
          activeCanvas.revision,
          signal,
        )
        signal.throwIfAborted()
      } catch (bindError) {
        if (signal.aborted) return
        const formatted = formatCanvasBindError(bindError)
        notice.value = formatted.message
        if (formatted.conflict) emit('canvasConflict', formatted.message)
        return
      }
    }
    if (!sameOwner()) return
    props.reserve(body.request_id); sentId = body.request_id; phase.value = '正在提交…'
    const job = await videoStudio.create(body, signal)
    signal.throwIfAborted()
    if (!job || job.id !== sentId) throw new Error('任务回执不匹配')
    materials.value?.reset(); draftCleared.value = true; emit('accepted', job)
  } catch (error) {
    if (signal.aborted) return
    const { status, message, code } = errorInfo(error)
    if (!sentId) { notice.value = message || '准备失败，未提交视频。'; return }
    if (![400, 401, 402, 403, 404, 405, 409, 413, 415, 422, 429].includes(status)) return
    const conflict = status === 409
    if (conflict && /同一任务标识|idempoten|fingerprint|request_id_conflict/i.test(`${message} ${code}`)) return
    emit('rejected', sentId)
    if (conflict && /报价|价格|通道已变化|quote|price/i.test(`${message} ${code}`)) {
      await loadModels(); notice.value = '报价或通道已变化，本次请求未获接受。请核对新报价后重新确认。'
    } else if (status === 401) notice.value = '登录已失效，本次请求被拒绝。请重新登录；不会自动重发。'
    else if (status === 429 || conflict) notice.value = '本次请求被拒绝，请先查看已有任务或稍后再操作。不会自动切换通道。'
    else if (status === 402) notice.value = '余额或额度不足，本次请求被拒绝。'
    else if (status === 403) { await loadModels(); notice.value = '权限、可用状态或内容校验未通过，本次请求被拒绝。' }
    else notice.value = message || '请求被拒绝，请检查素材与设置后再确认。'
  } finally { phase.value = '' }
}
function restoreFocus() { if (opener?.isConnected) opener.focus(); opener = undefined }
function close() { if (sheet.value?.open) sheet.value.close() }
function open(trigger?: HTMLElement) {
  if (props.mobile && sheet.value && !sheet.value.open) { opener = trigger; sheet.value.showModal() }
  void nextTick(() => (props.heldId || promptInput.value?.disabled ? heading.value : promptInput.value)?.focus())
}
function useExample(trigger: HTMLElement) {
  if (!props.heldId && !busy.value && !prompt.value.includes(props.example)) prompt.value = [prompt.value.trimEnd(), props.example].filter(Boolean).join('\n')
  open(trigger)
}
function clearDraft() {
  draftCleared.value = true
  materials.value?.reset()
  if (canvasActive.value) {
    prompt.value = ''
    notice.value = ''
    emit('clearCanvasDraft')
  }
  void nextTick(() => promptInput.value?.focus())
}
function freshDraft() {
  if (props.unconfirmed) return
  emit('newDraft')
  if (canvasActive.value) emit('clearCanvasDraft')
  materials.value?.reset(); prompt.value = ''; seed.value = ''; nativeSize.value = ''; resolution.value = ''; timeout.value = ''; extra.value = ''; notice.value = ''
  void nextTick(() => promptInput.value?.focus())
}
function reuse(job: VideoJob, trigger?: HTMLElement) {
  if (props.unconfirmed || busy.value) return
  const target = models.value.find(item => item.id === job.model)
  if (!target) { notice.value = '原模型当前不可用，未回填参数。'; return }
  if (props.heldId) emit('newDraft')
  if (canvasActive.value) emit('clearCanvasDraft')
  materials.value?.reset(); draftCleared.value = true; choose(target)
  if (!isAutomatic(target) && secondsOptions(target).includes(job.seconds)) seconds.value = job.seconds
  nativeSize.value = target.muse_options && job.parameters?.native_size ? job.parameters.native_size : ''
  if (nativeSize.value) size.value = ''
  else if (job.size && target.sizes.includes(job.size)) size.value = job.size
  else if (!job.size) size.value = ''
  seed.value = target.supports_seed && job.parameters?.seed !== undefined ? String(job.parameters.seed) : ''
  resolution.value = target.muse_options && job.parameters?.resolution ? job.parameters.resolution : ''
  timeout.value = target.muse_options && job.parameters?.timeout !== undefined ? String(job.parameters.timeout) : ''
  extra.value = ''; prompt.value = ''; notice.value = '已回填仍兼容的已记录参数。历史未保存文字描述、补充文本和本地素材，请重新提供并确认当前报价。'
  open(trigger)
}
watch(() => props.canvasDraft, (next, prev) => {
  if (next.status === 'ready') {
    const changed =
      prev?.status !== 'ready' ||
      prev.canvasId !== next.canvasId ||
      prev.nodeId !== next.nodeId ||
      prev.revision !== next.revision
    if (changed) {
      draftCleared.value = false
      if (!props.heldId) prompt.value = next.prompt
      notice.value = ''
    }
  }
})
watch(() => props.mobile, v => { if (!v) close() })
onMounted(loadModels)
onBeforeUnmount(() => { lifetime.abort(); picker.value?.close(); close() })
defineExpose({ open, close, useExample, reuse })
</script>
