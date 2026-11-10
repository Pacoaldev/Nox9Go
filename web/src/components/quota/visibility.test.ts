import { describe, expect, it } from 'bun:test'
import {
  ACCOUNT_FILTER_OPTIONS,
  filterPinnedConnections,
  formatCompactCount,
  getConnectionsEmptyMessage,
  isCardPinned,
  parseQuotaData,
  setCardPinned,
  type CardVisibility,
} from './types'

// The card toggle decides what the `Custom` view lists. It must never read as
// "disabled": a connection with no stored choice is part of the shortlist, and
// unpinning one leaves every other choice untouched.
describe('card visibility', () => {
  it('treats a connection with no stored choice as pinned', () => {
    expect(isCardPinned({}, 'conn-1')).toBe(true)
  })

  it('keeps a connection out of the shortlist once it is unpinned', () => {
    const next = setCardPinned({}, 'conn-1', false)
    expect(isCardPinned(next, 'conn-1')).toBe(false)
    expect(next).toEqual({ 'conn-1': false })
  })

  it('drops the stored key when a connection is pinned again', () => {
    const unpinned = setCardPinned({}, 'conn-1', false)
    const pinned = setCardPinned(unpinned, 'conn-1', true)
    expect(pinned).toEqual({})
    expect(isCardPinned(pinned, 'conn-1')).toBe(true)
  })

  it('does not touch the other connections', () => {
    const start: CardVisibility = { 'conn-1': false, 'conn-2': false }
    const next = setCardPinned(start, 'conn-2', true)
    expect(isCardPinned(next, 'conn-1')).toBe(false)
    expect(isCardPinned(next, 'conn-2')).toBe(true)
  })

  it('lists only the pinned connections in the custom view', () => {
    const connections = [
      { id: 'conn-1', provider: 'a' },
      { id: 'conn-2', provider: 'b' },
      { id: 'conn-3', provider: 'c' },
    ]
    const visibility: CardVisibility = { 'conn-2': false }
    const visible = filterPinnedConnections(connections, visibility)
    expect(visible.map((c) => c.id)).toEqual(['conn-1', 'conn-3'])
  })
})

describe('account filter options', () => {
  it('offers the custom view next to the built-in filters', () => {
    const values = ACCOUNT_FILTER_OPTIONS.map((option) => option.value)
    expect(values).toContain('custom')
    expect(values).toContain('metered')
  })
})

describe('custom view empty state', () => {
  it('explains the eye button when the custom view is empty', () => {
    const message = getConnectionsEmptyMessage(
      { eligibleConnections: 4, providerFilteredConnections: 4 },
      'all',
      'custom',
    )
    expect(message.title).toBe('No Cards In The Custom View')
  })

  it('keeps the provider-empty message when there is nothing connected', () => {
    const message = getConnectionsEmptyMessage(
      { eligibleConnections: 0, providerFilteredConnections: 0 },
      'all',
      'custom',
    )
    expect(message.title).toBe('No Providers Connected')
  })
})

// A token quota is counted in millions or billions. The raw digits do not fit
// in a card, and the operator has to read the remaining allowance at a glance.
describe('formatCompactCount', () => {
  it('keeps small counts exact', () => {
    expect(formatCompactCount(0)).toBe('0')
    expect(formatCompactCount(999)).toBe('999')
  })

  it('reads thousands, millions and billions', () => {
    expect(formatCompactCount(12_500)).toBe('12.5K')
    expect(formatCompactCount(500_000_000)).toBe('500.0M')
    expect(formatCompactCount(1_000_000_000)).toBe('1.0B')
  })
})

// NaN.Builders answers per-model token counters. Each model becomes its own row
// so the two caps (1.0B for DeepSeek, 500.0M for GLM 5.3) are visible.
describe('parseQuotaData for NaN.Builders', () => {
  const payload = {
    quotas: {
      'glm5.3': {
        used: 2_500_000,
        total: 500_000_000,
        remainingPercentage: 99.5,
        resetAt: '2026-12-01T00:00:00Z',
        displayName: 'glm5.3 (tokens)',
        unit: 'tokens',
      },
      deepseek: {
        used: 0,
        total: 1_000_000_000,
        remainingPercentage: 100,
        resetAt: '2026-12-01T00:00:00Z',
        displayName: 'deepseek (tokens)',
        unit: 'tokens',
      },
    },
  }

  it('keeps one row per model with its token cap', () => {
    const rows = parseQuotaData('nan', payload)
    expect(rows).toHaveLength(2)
    const glm = rows.find((row) => row.modelKey === 'glm5.3')
    expect(glm?.name).toBe('glm5.3 (tokens)')
    expect(glm?.total).toBe(500_000_000)
    expect(glm?.unit).toBe('tokens')
    expect(rows.map((row) => row.name)).toContain('deepseek (tokens)')
  })

  it('treats the alias id the same way', () => {
    expect(parseQuotaData('nanbuilders', payload)).toHaveLength(2)
  })
})
