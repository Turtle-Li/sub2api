import type { PlanEntitlements, SubscriptionPlan } from '@/types/payment'

type TranslateFn = (key: string) => string

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
  const unit = String(plan.validity_unit || 'day').trim().toLowerCase()
  const base = unit.endsWith('s') ? unit.slice(0, -1) : unit
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
 * 重置卡有效期展示（"14天" / "8周" / "3个月"）。
 *
 * 与套餐有效期同构：数值 + 单位。历史套餐没有 reset_card_expiry_unit，
 * 后端把空单位当作「天」，这里保持一致。单位归一化后与
 * PlanEntitlements.ResetCardValidityDays 一一对应，避免用户看到的有效期
 * 与实际发放的有效期不同。
 */
export function resetCardValidityLabel(
  entitlements: Pick<PlanEntitlements, 'reset_card_expiry_days' | 'reset_card_expiry_unit'>,
  t: TranslateFn,
): string {
  const count = entitlements.reset_card_expiry_days
  if (!count || count <= 0) return ''
  const unit = String(entitlements.reset_card_expiry_unit || 'day').trim().toLowerCase()
  const base = unit.endsWith('s') ? unit.slice(0, -1) : unit
  if (base === 'month') return `${count}${t('payment.months')}`
  if (base === 'week') return `${count}${t('payment.weeks')}`
  return `${count}${t('payment.days')}`
}
