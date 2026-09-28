import { describe, expect, it } from 'vitest'
import { formatConversationTime } from './conversation-time'
import { botInitials } from './bot-initials'

describe('formatConversationTime', () => {
  const now = new Date(2026, 8, 28, 15, 0)

  it('shows time of day for today', () => {
    expect(formatConversationTime(new Date(2026, 8, 28, 9, 5).toISOString(), 'en-US', now)).toBe('9:05 AM')
  })

  it('shows weekday within a week', () => {
    expect(formatConversationTime(new Date(2026, 8, 25, 9, 5).toISOString(), 'en-US', now)).toBe('Fri')
  })

  it('shows a short date for older items', () => {
    expect(formatConversationTime(new Date(2026, 5, 1).toISOString(), 'en-US', now)).toBe('Jun 1')
    expect(formatConversationTime(new Date(2025, 5, 1).toISOString(), 'en-US', now)).toBe('Jun 1, 2025')
  })

  it('returns empty for invalid input', () => {
    expect(formatConversationTime('nope', 'en-US', now)).toBe('')
  })
})

describe('botInitials', () => {
  it('takes up to two initials', () => {
    expect(botInitials('crypto price-bot')).toBe('CP')
    expect(botInitials('Bot 设计师')).toBe('B设')
    expect(botInitials('  ')).toBe('B')
  })
})
