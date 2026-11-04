import { describe, expect, it } from 'bun:test'
import { formatCheckedAt, summarizeSweep } from './catalogSweep'
import type { CatalogSweepEntry } from '../api/client'

const entry = (overrides: Partial<CatalogSweepEntry> = {}): CatalogSweepEntry => ({
  provider: 'nvidia',
  checkedAt: '2026-11-05T10:00:00Z',
  listed: 42,
  revived: 0,
  ...overrides,
})

describe('summarizeSweep', () => {
  it('reports a missing entry as never checked', () => {
    expect(summarizeSweep(undefined)).toEqual({ state: 'never', unlistedCount: 0, listed: 0 })
  })

  it('reports an entry without a check timestamp as never checked', () => {
    expect(summarizeSweep(entry({ checkedAt: '' }))).toEqual({
      state: 'never',
      unlistedCount: 0,
      listed: 0,
    })
  })

  it('reports a provider without a queryable catalogue as skipped', () => {
    expect(summarizeSweep(entry({ skipped: true }))).toEqual({
      state: 'skipped',
      unlistedCount: 0,
      listed: 42,
    })
  })

  it('prefers the error state over skipped', () => {
    expect(summarizeSweep(entry({ skipped: true, error: 'catalogue read failed' }))).toEqual({
      state: 'error',
      unlistedCount: 0,
      listed: 42,
    })
  })

  it('counts the models the provider no longer lists', () => {
    expect(summarizeSweep(entry({ unlisted: ['old-model', 'another'] }))).toEqual({
      state: 'ok',
      unlistedCount: 2,
      listed: 42,
    })
  })

  it('reports zero unlisted models when the provider lists them all', () => {
    expect(summarizeSweep(entry())).toEqual({ state: 'ok', unlistedCount: 0, listed: 42 })
  })
})

describe('formatCheckedAt', () => {
  it('renders nothing for an invalid timestamp', () => {
    expect(formatCheckedAt('not-a-date')).toBe('')
  })

  it('renders nothing for an empty timestamp', () => {
    expect(formatCheckedAt('')).toBe('')
    expect(formatCheckedAt(undefined)).toBe('')
  })

  it('renders a date-time for a valid timestamp', () => {
    expect(formatCheckedAt('2026-11-05T10:00:00Z')).not.toBe('')
  })
})
