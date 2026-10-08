import { describe, expect, it } from 'vitest'
import { parseSharedTheme, sharedThemeCookie } from './shared-theme'

describe('shared theme cookie', () => {
  it('parses the cookie among others', () => {
    expect(parseSharedTheme('a=1; yosi-theme=dark; b=2')).toBe('dark')
    expect(parseSharedTheme('yosi-theme=light')).toBe('light')
    expect(parseSharedTheme('yosi-theme=blue')).toBeNull()
    expect(parseSharedTheme('xyosi-theme=dark')).toBeNull()
  })

  it('scopes the cookie to the parent domain only on yosiyo.si', () => {
    expect(sharedThemeCookie('dark', 'app.yosiyo.si', true)).toContain('; domain=yosiyo.si; secure')
    expect(sharedThemeCookie('light', 'localhost', false)).not.toContain('domain=')
  })
})
