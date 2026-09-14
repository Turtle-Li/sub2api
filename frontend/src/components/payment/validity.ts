import type { PlanEntitlements, SubscriptionPlan } from '@/types/payment'

type TranslateFn = (key: string, params?: Record<string, unknown>) => string

type PlanValidityUnit = 'day' | 'week' | 'month' | 'quarter' | 'year'

function normalizedPlanValidityUnit(raw: string | undefined): PlanValidityUnit {
  const unit = String(raw || 'day').trim().toLowerCase()
  const base = unit.endsWith('s') ? unit.slice(0, -1) : unit
  if (base === 'week' || base === 'month' || base === 'quarter' || base === 'year') return base
  // Keep the label aligned with psComputeValidityDays: unknown units are billed as days.
  return 'day'
}

/**
 * 用户侧套餐有效期后缀（"$9.9 / 月"、"$9.9 / 30天"）。
 *
 * 管理端表单保存的单位是复数（days/weeks/months），而数据库默认值与部分
 * 历史数据是单数（day）。后端计费 psComputeValidityDays 对 week/weeks、
 * month/months 分别按 ×7、×30 天换算，其余一律按天。此前用户侧只匹配
 * 单数 'month'，管理端存的 'months' 永远落进默认分支，「1 个月」的套餐
 * 被显示成「1天」（#4607）；'weeks' 则会显示成周数的「N天」。
 *
 * 这里把单位归一化后与计费语义一一对应，保证用户看到的周期与实际
 * 生效周期一致。
 */
export function planValiditySuffix(
  plan: Pick<SubscriptionPlan, 'validity_days' | 'validity_unit'>,
  t: TranslateFn,
): string {
  const base = normalizedPlanValidityUnit(plan.validity_unit)
  const days = plan.validity_days
  if (base === 'month') {
    return days === 1 ? t('payment.perMonth') : `${days}${t('payment.months')}`
  }
  if (base === 'week') {
    return `${days}${t('payment.weeks')}`
  }
  if (base === 'quarter') {
    return days === 1 ? t('payment.quarter') : `${days}${t('payment.quarters')}`
  }
  if (base === 'year') {
    return days === 1 ? t('payment.perYear') : `${days}${t('payment.years')}`
  }
  // 其余单位（含数据库默认的 day 与未知值）后端一律按天计费，展示保持一致。
  return `${days}${t('payment.days')}`
}

/**
 * 管理端使用的完整有效期标签（“1 个月”“3 个月”“1 年”）。
 *
 * 用户侧价格后缀会把一个月简写成“月”，管理端配置列表则需要显示完整的
 * 数量和中文单位，避免把数据库里的 `month(s)` 等英文枚举直接暴露出来。
 */
export function planValidityLabel(
  plan: Pick<SubscriptionPlan, 'validity_days' | 'validity_unit'>,
  t: TranslateFn,
): string {
  const count = Number.isFinite(plan.validity_days) ? Math.max(0, plan.validity_days) : 0
  const unit = normalizedPlanValidityUnit(plan.validity_unit)
  const plurality = count === 1 ? 'One' : 'Many'
  return `${count} ${t(`payment.validityUnits.${unit}${plurality}`)}`
}

/**
 * The payment service commits monthly reset cards against calendar periods,
 * never an approximate day count. Keep the admin form on the same exact rule.
 */
export function monthlyResetCardIssueCount(
  plan: Pick<SubscriptionPlan, 'validity_days' | 'validity_unit'>,
): number | null {
  const value = Number(plan.validity_days)
  if (!Number.isSafeInteger(value) || value <= 0) return null

  const unit = normalizedPlanValidityUnit(plan.validity_unit)
  const multiplier = unit === 'month' ? 1 : unit === 'quarter' ? 3 : unit === 'year' ? 12 : 0
  if (multiplier === 0 || value > 120 / multiplier) return null

  const issues = value * multiplier
  return issues >= 2 && issues <= 120 ? issues : null
}

/**
 * 重置卡有效期展示（"14天" / "8周" / "3个月"）。
 *
 * 与套餐有效期同构：数值 + 单位。历史套餐没有 reset_card_expiry_unit，
 * 后端把空单位当作「天」，这里保持一致。单位归一化后与
 * PlanEntitlements.ResetCardValidityDays 一一对应，避免用户看到的有效期
 * 与实际发放的有效期不同。
 */
export function resetCardValidityLabel(
  entitlements: Partial<Pick<PlanEntitlements, 'reset_card_expiry_days' | 'reset_card_expiry_unit'>>,
  t: TranslateFn,
): string {
  const count = Number(entitlements.reset_card_expiry_days)
  if (!Number.isFinite(count) || count <= 0) return ''
  const unit = String(entitlements.reset_card_expiry_unit || 'day').trim().toLowerCase()
  const base = unit.endsWith('s') ? unit.slice(0, -1) : unit
  if (base === 'month') return `${count}${t('payment.months')}`
  if (base === 'week') return `${count}${t('payment.weeks')}`
  return `${count}${t('payment.days')}`
}

/** A buyer-facing summary for scheduled reset cards; empty for one-time grants. */
export function monthlyResetCardDeliveryLabel(
  entitlements: Partial<Pick<PlanEntitlements,
    'reset_card_count' | 'reset_card_delivery_mode' | 'reset_card_issue_count' | 'reset_card_expiry_days' | 'reset_card_expiry_unit'
  >>,
  t: TranslateFn,
): string {
  if (entitlements.reset_card_delivery_mode !== 'monthly') return ''
  const count = Number(entitlements.reset_card_count)
  const issues = Number(entitlements.reset_card_issue_count)
  const validity = resetCardValidityLabel(entitlements, t)
  if (!Number.isSafeInteger(count) || count <= 0 || !Number.isSafeInteger(issues) || issues < 2 || !validity) return ''
  return t('payment.entitlements.monthlyResetCards', { count, issues, validity })
}
