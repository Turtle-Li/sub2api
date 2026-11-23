/**
 * Shared Model Theme & Brand Icon Registry
 * Adapted & refined from 一键生图's per-model theme system (gemini / openai / wan / seedream / minimax)
 * extended with dedicated Liquid Glass palettes for Muse, Hunyuan, and H3 Fast.
 * Consumed by both Image Studio (React) and Video Studio (Vue).
 */

export type ModelThemeKey =
  | 'gemini'
  | 'openai'
  | 'seedream'
  | 'wan'
  | 'muse'
  | 'minimax'
  | 'h3-fast'
  | 'hunyuan'
  | 'neutral'

export interface ModelThemeMeta {
  readonly key: ModelThemeKey
  readonly mark: string
  readonly viewBox: string
  readonly paths: readonly string[]
}

/**
 * Clean monochrome vector glyphs rendered via `fill="currentColor"` inside themed Liquid Glass tiles.
 * Preserves sharp rendering across Retina displays, Light & Dark modes, and zero external image requests.
 */
export const MODEL_THEME_META: Readonly<Record<ModelThemeKey, ModelThemeMeta>> = {
  gemini: {
    key: 'gemini',
    mark: 'G',
    viewBox: '0 0 24 24',
    paths: [
      'M11.04 19.32Q12 21.51 12 24q0-2.49.93-4.68.96-2.19 2.58-3.81t3.81-2.55Q21.51 12 24 12q-2.49 0-4.68-.93a12.3 12.3 0 0 1-3.81-2.58 12.3 12.3 0 0 1-2.58-3.81Q12 2.49 12 0q0 2.49-.96 4.68-.93 2.19-2.55 3.81a12.3 12.3 0 0 1-3.81 2.58Q2.49 12 0 12q2.49 0 4.68.96 2.19.93 3.81 2.55t2.55 3.81',
    ],
  },
  openai: {
    key: 'openai',
    mark: 'O',
    viewBox: '0 0 24 24',
    paths: [
      'M22.2819 9.8211a5.9847 5.9847 0 0 0-.5157-4.9108 6.0462 6.0462 0 0 0-6.5098-2.9A6.0651 6.0651 0 0 0 4.9807 4.1818a5.9847 5.9847 0 0 0-3.9977 2.9 6.0462 6.0462 0 0 0 .7427 7.0966 5.98 5.98 0 0 0 .511 4.9107 6.051 6.051 0 0 0 6.5146 2.9001A5.9847 5.9847 0 0 0 13.2599 24a6.0557 6.0557 0 0 0 5.7718-4.2058 5.9894 5.9894 0 0 0 3.9977-2.9001 6.0557 6.0557 0 0 0-.7475-7.0729zm-9.022 12.6081a4.4755 4.4755 0 0 1-2.8764-1.0408l.1419-.0804 4.7783-2.7582a.7948.7948 0 0 0 .3927-.6813v-6.7369l2.02 1.1686a.071.071 0 0 1 .038.052v5.5826a4.504 4.504 0 0 1-4.4945 4.4944zm-9.6607-4.1254a4.4708 4.4708 0 0 1-.5346-3.0137l.142.0852 4.783 2.7582a.7712.7712 0 0 0 .7806 0l5.8428-3.3685v2.3324a.0804.0804 0 0 1-.0332.0615L9.74 19.9502a4.4992 4.4992 0 0 1-6.1408-1.6464zM2.3408 7.8956a4.485 4.485 0 0 1 2.3655-1.9728V11.6a.7664.7664 0 0 0 .3879.6765l5.8144 3.3543-2.0201 1.1685a.0757.0757 0 0 1-.071 0l-4.8303-2.7865A4.504 4.504 0 0 1 2.3408 7.872zm16.5963 3.8558L13.1038 8.364 15.1192 7.2a.0757.0757 0 0 1 .071 0l4.8303 2.7913a4.4944 4.4944 0 0 1-.6765 8.1042v-5.6772a.79.79 0 0 0-.407-.667zm2.0107-3.0231l-.142-.0852-4.7735-2.7818a.7759.7759 0 0 0-.7854 0L9.409 9.2297V6.8974a.0662.0662 0 0 1 .0284-.0615l4.8303-2.7866a4.4992 4.4992 0 0 1 6.6802 4.66zM8.3065 12.863l-2.02-1.1638a.0804.0804 0 0 1-.038-.0567V6.0742a4.4992 4.4992 0 0 1 7.3757-3.4537l-.142.0805L8.704 5.459a.7948.7948 0 0 0-.3927.6813zm1.0976-2.3654l2.602-1.4998 2.6069 1.4998v2.9994l-2.5974 1.4997-2.6067-1.4997Z',
    ],
  },
  seedream: {
    key: 'seedream',
    mark: 'S',
    viewBox: '0 0 24 24',
    paths: [
      'M12 2.2c2.1 0 4.3 1.2 5.6 3.1 1.4 2 1.8 4.7 1.1 7.2-.9 3.3-3.5 6.4-6.7 9.3-3.2-2.9-5.8-6-6.7-9.3-.7-2.5-.3-5.2 1.1-7.2C7.7 3.4 9.9 2.2 12 2.2zm0 4.1a4.3 4.3 0 0 0-3.7 2.2c-.8 1.4-.9 3.1-.3 4.6.7 1.8 2.1 3.4 4 5 1.9-1.6 3.3-3.2 4-5 .6-1.5.5-3.2-.3-4.6A4.3 4.3 0 0 0 12 6.3zm0 2.5a2.1 2.1 0 1 1 0 4.2 2.1 2.1 0 0 1 0-4.2z',
    ],
  },
  wan: {
    key: 'wan',
    mark: 'W',
    viewBox: '0 0 24 24',
    paths: [
      'M3.2 6.4a1.2 1.2 0 0 1 2.25-.42l3.15 8.18 2.3-6.06a1.2 1.2 0 0 1 2.2 0l2.3 6.06 3.15-8.18a1.2 1.2 0 1 1 2.24.86l-4.28 11.1a1.2 1.2 0 0 1-2.24.01L12 11.98l-2.27 5.97a1.2 1.2 0 0 1-2.24-.01L3.21 6.84a1.2 1.2 0 0 1-.01-.44z',
    ],
  },
  muse: {
    key: 'muse',
    mark: 'M',
    viewBox: '0 0 24 24',
    paths: [
      'M12 2.5a9.5 9.5 0 1 0 9.5 9.5A9.51 9.51 0 0 0 12 2.5zm0 2.6a6.9 6.9 0 0 1 6.45 4.44l-4.08 1.56a2.8 2.8 0 0 0-2.37-1.92zm-6.45 4.44A6.9 6.9 0 0 1 11 5.1v4.08a2.8 2.8 0 0 0-2.37 1.92zm.42 5.44a6.9 6.9 0 0 1-.82-3.44l4.12 1.58a2.8 2.8 0 0 0 1.48 2.32l-1.56 3.5a6.9 6.9 0 0 1-3.22-3.96zm7.03 3.92l-1.56-3.5a2.8 2.8 0 0 0 1.92-2.32l4.12-1.58a6.9 6.9 0 0 1-4.48 7.4z',
    ],
  },
  minimax: {
    key: 'minimax',
    mark: 'H',
    viewBox: '0 0 24 24',
    paths: [
      'M11.43 3.92a.86.86 0 1 0-1.718 0v14.236a1.999 1.999 0 0 1-3.997 0V9.022a.86.86 0 1 0-1.718 0v3.87a1.999 1.999 0 0 1-3.997 0V11.49a.57.57 0 0 1 1.139 0v1.404a.86.86 0 0 0 1.719 0V9.022a1.999 1.999 0 0 1 3.997 0v9.134a.86.86 0 0 0 1.719 0V3.92a1.998 1.998 0 1 1 3.996 0v11.788a.57.57 0 1 1-1.139 0zm10.572 3.105a2 2 0 0 0-1.999 1.997v7.63a.86.86 0 0 1-1.718 0V3.923a1.999 1.999 0 0 0-3.997 0v16.16a.86.86 0 0 1-1.719 0V18.08a.57.57 0 1 0-1.138 0v2a1.998 1.998 0 0 0 3.996 0V3.92a.86.86 0 0 1 1.719 0v12.73a1.999 1.999 0 0 0 3.996 0V9.023a.86.86 0 1 1 1.72 0v6.686a.57.57 0 0 0 1.138 0V9.022a2 2 0 0 0-1.998-1.997',
    ],
  },
  'h3-fast': {
    key: 'h3-fast',
    mark: 'H',
    viewBox: '0 0 24 24',
    paths: [
      'M13.6 2.3a.8.8 0 0 1 .74 1.09L11.7 10h6.4a.8.8 0 0 1 .64 1.28l-8.5 10.6a.8.8 0 0 1-1.39-.77L11.4 14H5.8a.8.8 0 0 1-.63-1.29l7.75-10.1a.8.8 0 0 1 .68-.31z',
    ],
  },
  hunyuan: {
    key: 'hunyuan',
    mark: 'H',
    viewBox: '0 0 24 24',
    paths: [
      'M12 3a9 9 0 1 0 9 9 9 9 0 0 0-9-9zm0 2.6a6.4 6.4 0 0 1 5.95 4.08c-1.8-.8-3.85-.82-5.95.08-2.1-.9-4.15-.88-5.95-.08A6.4 6.4 0 0 1 12 5.6zm0 12.8a6.4 6.4 0 0 1-5.95-4.08c1.8.8 3.85.82 5.95-.08 2.1.9 4.15.88 5.95.08A6.4 6.4 0 0 1 12 18.4z',
    ],
  },
  neutral: {
    key: 'neutral',
    mark: 'AI',
    viewBox: '0 0 24 24',
    paths: [
      'M12 2.8l2.2 6.2 6.2 2.2-6.2 2.2-2.2 6.2-2.2-6.2-6.2-2.2 6.2-2.2L12 2.8z',
    ],
  },
}

export function resolveImageModelTheme(provider: string, modelId = ''): ModelThemeKey {
  const p = provider.trim().toLowerCase()
  const m = modelId.trim().toLowerCase()
  if (p === 'gemini_api' || p === 'vertex' || m.startsWith('gemini-')) return 'gemini'
  if (p === 'openai_images' || m.startsWith('gpt-image') || m.startsWith('dall-e')) return 'openai'
  if (p === 'seedream_images' || m.includes('seedream')) return 'seedream'
  if (p === 'wan_images' || m.startsWith('wan')) return 'wan'
  if (p === 'muse_images' || m.startsWith('muse')) return 'muse'
  if (p === 'workbuddy_images' || /(^|:)(hunyuan|hy-image)/.test(m)) return 'hunyuan'
  return 'neutral'
}

export function resolveVideoModelTheme(provider: string, modelId = ''): ModelThemeKey {
  const p = provider.trim().toLowerCase()
  const m = modelId.trim().toLowerCase()
  if (p === 'muse' || m.includes('muse')) return 'muse'
  if (m.includes('h3-fast') || m.includes('fast')) return 'h3-fast'
  if (p === 'goodput' || m.includes('minimax') || m.includes('h3')) return 'minimax'
  if (m.includes('gemini') || m.includes('veo')) return 'gemini'
  if (m.includes('seedance') || m.includes('seedream')) return 'seedream'
  if (m.includes('wan')) return 'wan'
  return 'neutral'
}

export function getModelThemeMeta(theme: ModelThemeKey): ModelThemeMeta {
  return MODEL_THEME_META[theme] || MODEL_THEME_META.neutral
}
