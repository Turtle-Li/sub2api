const timeFormat = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai', year: 'numeric', month: 'numeric', day: 'numeric',
  hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
})

export function daypassDateTime(seconds: number, reference: number): string {
  const parts = new Map(timeFormat.formatToParts(seconds * 1000).map(part => [part.type, part.value]))
  const year = parts.get('year')
  const referenceYear = timeFormat.formatToParts(reference * 1000).find(part => part.type === 'year')?.value
  const date = `${parts.get('month')}月${parts.get('day')}日 ${parts.get('hour')}:${parts.get('minute')}`
  return year !== referenceYear ? `${year}年${date}` : date
}
