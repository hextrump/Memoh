/** Stable client-side avatar for bots whose server record has no avatar URL. */
export function pokeballAvatar(seed: string | null | undefined): string {
  let hash = 2166136261
  for (const char of seed || 'pokeagent') hash = Math.imul(hash ^ char.charCodeAt(0), 16777619)
  const hue = Math.abs(hash) % 360
  const side = hash % 2 ? 1 : -1
  const tilt = (Math.abs(hash) % 7) - 3
  const accent = (hue + 55) % 360
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128"><defs><linearGradient id="t" x2="0" y2="1"><stop stop-color="hsl(${hue} 86% 70%)"/><stop offset="1" stop-color="hsl(${hue} 78% 48%)"/></linearGradient><clipPath id="c"><circle cx="64" cy="69" r="45"/></clipPath></defs><path d="M${64 + side * 27} 47l${side * 15}-11-3 15 14-3" fill="hsl(${accent} 82% 62%)" stroke="#513b36" stroke-width="2" stroke-linejoin="round"/><circle cx="64" cy="69" r="45" fill="#f7f1ea" stroke="#282225" stroke-opacity=".2" stroke-width="2"/><path d="M19 69a45 45 0 0 0 90 0Z" fill="url(#t)" clip-path="url(#c)"/><rect x="19" y="65" width="90" height="8" fill="#282225" clip-path="url(#c)"/><circle cx="64" cy="69" r="12" fill="#282225"/><circle cx="64" cy="69" r="8" fill="#f7f1ea" stroke="hsl(${accent} 82% 62%)" stroke-width="2"/><g transform="rotate(${tilt} 64 69)"><ellipse cx="46" cy="61" rx="6" ry="8" fill="#282225"/><ellipse cx="82" cy="61" rx="6" ry="8" fill="#282225"/><circle cx="48" cy="58" r="2" fill="white"/><circle cx="84" cy="58" r="2" fill="white"/><path d="M58 86q6 5 12 0" fill="none" stroke="#704d4e" stroke-width="2" stroke-linecap="round"/></g></svg>`
  return `data:image/svg+xml,${encodeURIComponent(svg)}`
}
