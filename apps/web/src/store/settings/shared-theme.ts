// The yosiyo.si portal and this app (app.yosiyo.si) share the light/dark
// choice through a cookie on the parent domain, so flipping it on either side
// carries over to the other.
export type SharedTheme = 'light' | 'dark'

export const SHARED_THEME_COOKIE = 'yosi-theme'
const SHARED_THEME_DOMAIN = 'yosiyo.si'

export function parseSharedTheme(cookie: string): SharedTheme | null {
  const match = cookie.match(/(?:^|;\s*)yosi-theme=(light|dark)(?:;|$)/)
  return match ? (match[1] as SharedTheme) : null
}

export function sharedThemeCookie(mode: SharedTheme, hostname: string, secure: boolean): string {
  const onSharedDomain = hostname === SHARED_THEME_DOMAIN || hostname.endsWith(`.${SHARED_THEME_DOMAIN}`)
  return `${SHARED_THEME_COOKIE}=${mode}; path=/; max-age=31536000; samesite=lax`
    + (onSharedDomain ? `; domain=${SHARED_THEME_DOMAIN}` : '')
    + (secure ? '; secure' : '')
}

export function readSharedTheme(): SharedTheme | null {
  if (typeof document === 'undefined') return null
  return parseSharedTheme(document.cookie)
}

export function writeSharedTheme(mode: SharedTheme) {
  if (typeof document === 'undefined') return
  if (readSharedTheme() === mode) return
  document.cookie = sharedThemeCookie(mode, location.hostname, location.protocol === 'https:')
}
