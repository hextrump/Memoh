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

/** Flattens a markdown message into a one-line preview (drops emphasis, code, heading and quote markers). */
export function plainPreview(text: string): string {
  return text
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/(\*\*|__|~~|`+)/g, '')
    .replace(/^\s{0,3}(#{1,6}|>|[-*+])\s+/gm, '')
    .replace(/\s+/g, ' ')
    .trim()
}
