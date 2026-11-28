const beijingDateTimeFormat = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit',
  hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
})

/**
 * 把订单里的时间统一显示为北京时间（UTC+8），不跟随浏览器所在时区。
 * 站点对外承诺「订单中的时间统一使用北京时间」，海外用户与 UTC 机器都要一致。
 */
export function shopBeijingDateTime(value: string | number | Date | null | undefined): string {
  if (value === null || value === undefined || value === '') return ''
  const date = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const parts = new Map(beijingDateTimeFormat.formatToParts(date).map(part => [part.type, part.value]))
  return `${parts.get('year')}-${parts.get('month')}-${parts.get('day')} ${parts.get('hour')}:${parts.get('minute')}:${parts.get('second')}`
}
