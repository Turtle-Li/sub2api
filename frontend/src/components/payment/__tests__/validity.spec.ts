import { describe, expect, it } from 'vitest'
import { monthlyResetCardDeliveryLabel, monthlyResetCardIssueCount, planValidityLabel, planValiditySuffix } from '../validity'

const t = (key: string): string =>
  ({
    'payment.perMonth': '月',
    'payment.days': '天',
    'payment.weeks': '周',
    'payment.months': '个月',
    'payment.quarter': '季度',
    'payment.quarters': '季度',
    'payment.perYear': '年',
    'payment.years': '年',
    'payment.validityUnits.dayOne': '天',
    'payment.validityUnits.dayMany': '天',
    'payment.validityUnits.weekOne': '周',
    'payment.validityUnits.weekMany': '周',
    'payment.validityUnits.monthOne': '个月',
    'payment.validityUnits.monthMany': '个月',
    'payment.validityUnits.quarterOne': '个季度',
    'payment.validityUnits.quarterMany': '个季度',
    'payment.validityUnits.yearOne': '年',
    'payment.validityUnits.yearMany': '年',
  })[key] ?? key

const suffix = (validity_days: number, validity_unit: string) =>
  planValiditySuffix({ validity_days, validity_unit }, t)

describe('planValiditySuffix', () => {
  // #4607：管理端表单保存的是复数 'months'，此前用户侧只匹配单数 'month'，
  // 「1 个月」的套餐被显示成「1天」。
  it('renders admin-form plural months correctly', () => {
    expect(suffix(1, 'months')).toBe('月')
    expect(suffix(3, 'months')).toBe('3个月')
  })

  it('renders singular month the same way', () => {
    expect(suffix(1, 'month')).toBe('月')
    expect(suffix(6, 'month')).toBe('6个月')
  })

  // 计费侧 weeks 按 ×7 天换算；显示必须是周数而非天数。
  it('renders weeks as weeks instead of mislabeled days', () => {
    expect(suffix(2, 'weeks')).toBe('2周')
    expect(suffix(1, 'week')).toBe('1周')
  })

  it('renders day-based and legacy units as days', () => {
    expect(suffix(30, 'days')).toBe('30天')
    expect(suffix(30, 'day')).toBe('30天') // 数据库默认值
    expect(suffix(30, '')).toBe('30天')
  })

  it('renders quarter and year plans with the billed period', () => {
    expect(suffix(1, 'quarter')).toBe('季度')
    expect(suffix(2, 'quarters')).toBe('2季度')
    expect(suffix(1, 'year')).toBe('年')
    expect(suffix(2, 'years')).toBe('2年')
  })

  // 后端 psComputeValidityDays 对未知单位一律按天计费，显示保持一致。
  it('falls back to days for units billing does not honor', () => {
    expect(suffix(365, 'unknown')).toBe('365天')
  })

  it('normalizes casing and whitespace', () => {
    expect(suffix(1, ' Months ')).toBe('月')
    expect(suffix(2, 'WEEKS')).toBe('2周')
  })
})

describe('planValidityLabel', () => {
  it('renders full localized labels for the admin catalogue', () => {
    expect(planValidityLabel({ validity_days: 1, validity_unit: 'months' }, t)).toBe('1 个月')
    expect(planValidityLabel({ validity_days: 3, validity_unit: 'month' }, t)).toBe('3 个月')
    expect(planValidityLabel({ validity_days: 1, validity_unit: 'quarter' }, t)).toBe('1 个季度')
    expect(planValidityLabel({ validity_days: 1, validity_unit: 'year' }, t)).toBe('1 年')
  })

  it('matches backend day fallback for legacy or unknown units', () => {
    expect(planValidityLabel({ validity_days: 30, validity_unit: '' }, t)).toBe('30 天')
    expect(planValidityLabel({ validity_days: 365, validity_unit: 'unknown' }, t)).toBe('365 天')
  })
})

describe('monthly reset-card delivery', () => {
  it('derives the committed periods only from calendar month, quarter, and year terms', () => {
    expect(monthlyResetCardIssueCount({ validity_days: 2, validity_unit: 'months' },)).toBe(2)
    expect(monthlyResetCardIssueCount({ validity_days: 2, validity_unit: 'quarters' },)).toBe(6)
    expect(monthlyResetCardIssueCount({ validity_days: 10, validity_unit: 'years' },)).toBe(120)
    expect(monthlyResetCardIssueCount({ validity_days: 1, validity_unit: 'month' },)).toBe(1)
    expect(monthlyResetCardIssueCount({ validity_days: 121, validity_unit: 'month' },)).toBeNull()
    expect(monthlyResetCardIssueCount({ validity_days: 90, validity_unit: 'days' },)).toBeNull()
  })

  it('keeps one-time grants on their existing label and describes monthly cards by period', () => {
    const monthlyT = (key: string, params?: Record<string, unknown>): string =>
      key === 'payment.entitlements.monthlyResetCards'
        ? `每月 ${params?.count} 张，共 ${params?.issues} 期，每张有效 ${params?.validity}`
        : t(key)
    const monthly = monthlyResetCardDeliveryLabel({
      reset_card_count: 2,
      reset_card_delivery_mode: 'monthly',
      reset_card_issue_count: 6,
      reset_card_expiry_days: 14,
      reset_card_expiry_unit: 'day',
    }, monthlyT)
    expect(monthly).toBe('每月 2 张，共 6 期，每张有效 14天')

    expect(monthlyResetCardDeliveryLabel({
      reset_card_count: 2,
      reset_card_delivery_mode: 'immediate',
      reset_card_issue_count: 1,
      reset_card_expiry_days: 14,
      reset_card_expiry_unit: 'day',
    }, t)).toBe('')
  })
})
