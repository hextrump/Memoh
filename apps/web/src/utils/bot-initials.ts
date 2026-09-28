/** Up to two uppercase initials from a bot label, for avatar fallbacks. */
export function botInitials(label: string): string {
  const initials = label
    .trim()
    .split(/[\s_-]+/)
    .filter(Boolean)
    .slice(0, 2)
    .map(word => Array.from(word)[0] ?? '')
    .join('')
    .toUpperCase()
  return initials || 'B'
}
