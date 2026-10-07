// External SSO (yosiyosi portal). When VITE_SSO_LOGIN_URL is set at build time
// the built-in login page is bypassed: the portal owns accounts and seeds the
// Memoh token for this origin.
export const ssoLoginUrl = (import.meta.env.VITE_SSO_LOGIN_URL as string | undefined) ?? ''
export const ssoLogoutUrl = (import.meta.env.VITE_SSO_LOGOUT_URL as string | undefined) ?? ''

let leaving = false

// First caller wins so a logout redirect is not overridden by the login one.
export function ssoRedirect(url: string) {
  if (leaving || !url) return
  leaving = true
  window.location.assign(url)
}
