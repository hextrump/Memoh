/**
 * Compact timestamp for conversation lists: time of day for today, weekday
 * within the last week, otherwise a short date.
 */
export function formatConversationTime(value: string, locale?: string, now: Date = new Date()): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
  const dayDiff = Math.round((startOfDay(now) - startOfDay(date)) / 86_400_000)
  if (dayDiff <= 0) return date.toLocaleTimeString(locale, { hour: 'numeric', minute: '2-digit' })
  if (dayDiff < 7) return date.toLocaleDateString(locale, { weekday: 'short' })
  return date.toLocaleDateString(locale, {
    year: date.getFullYear() === now.getFullYear() ? undefined : 'numeric',
    month: 'short',
    day: 'numeric',
  })
}
