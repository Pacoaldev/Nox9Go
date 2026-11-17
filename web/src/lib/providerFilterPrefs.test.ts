import { afterEach, beforeEach, describe, expect, it } from 'bun:test'
import { loadProviderStatusFilter, saveProviderStatusFilter } from './providerFilterPrefs'

// Bun has no DOM storage; providerFilterPrefs guards on `typeof localStorage`,
// so a stub is installed per test and the previous descriptor is restored after
// (same shape as compactNumber.test.ts / displayPrefs.test.ts).
let entries: Record<string, string> = {}
const storageStub = {
  getItem: (key: string) => entries[key] ?? null,
  setItem: (key: string, value: string) => {
    entries[key] = value
  },
  removeItem: (key: string) => {
    delete entries[key]
  },
  clear: () => {
    entries = {}
  },
}
const previousDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')

beforeEach(() => {
  entries = {}
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    writable: true,
    value: storageStub,
  })
})

afterEach(() => {
  if (previousDescriptor) {
    Object.defineProperty(globalThis, 'localStorage', previousDescriptor)
  } else {
    Reflect.deleteProperty(globalThis, 'localStorage')
  }
})

describe('provider status filter preference', () => {
  it('defaults to all on a browser that never chose a filter', () => {
    expect(loadProviderStatusFilter()).toBe('all')
  })

  it('round-trips every filter value', () => {
    for (const value of ['connected', 'error', 'disabled', 'not_connected', 'all'] as const) {
      saveProviderStatusFilter(value)
      expect(loadProviderStatusFilter()).toBe(value)
    }
  })

  it('treats a hand-edited or stale value as all', () => {
    localStorage.setItem('providerStatusFilter', 'bogus')
    expect(loadProviderStatusFilter()).toBe('all')
  })
})