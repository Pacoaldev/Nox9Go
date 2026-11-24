import { describe, expect, it } from 'bun:test'
import { COMBO_STRATEGIES, parseCapacityAdapterSettings, resolveComboStrategy } from './types'

const OPTION_VALUES = COMBO_STRATEGIES.map((s) => s.value)

describe('resolveComboStrategy', () => {
  // The server fills combo.strategy from settings.comboStrategies[name] and
  // otherwise from the global `comboStrategy`, so a card can receive either
  // vocabulary. Anything it can receive has to render as one of the options.
  it('maps every value the server can send onto an existing option', () => {
    const serverValues = [
      undefined,
      null,
      '',
      'fallback',
      'round-robin',
      'first-model',
      'sticky',
      'capacity',
      'fusion',
      'nonsense-from-a-hand-edited-backup'
    ]

    for (const value of serverValues) {
      expect(OPTION_VALUES).toContain(resolveComboStrategy(value))
    }
  })

  it('treats the global routing mode as the card fallback', () => {
    // `first-model` is the global Combo Routing Mode and the default when a
    // combo has no per-combo entry. It is try-in-order, which is exactly what
    // the card calls `fallback`.
    expect(resolveComboStrategy('first-model')).toBe('fallback')
  })

  it('passes a real per-combo strategy through unchanged', () => {
    for (const value of ['round-robin', 'sticky', 'capacity', 'fusion']) {
      expect(resolveComboStrategy(value)).toBe(value)
    }
  })

  it('falls back for a missing or unrecognised value', () => {
    expect(resolveComboStrategy(undefined)).toBe('fallback')
    expect(resolveComboStrategy(null)).toBe('fallback')
    expect(resolveComboStrategy('')).toBe('fallback')
    expect(resolveComboStrategy('nonsense')).toBe('fallback')
  })

  it('lists every option with a non-empty label', () => {
    for (const strategy of COMBO_STRATEGIES) {
      expect(strategy.value.length).toBeGreaterThan(0)
      expect(strategy.label.length).toBeGreaterThan(0)
    }
  })
})

// The card's toggle has to state what the gateway will actually do. The backend
// reads `capacityAdapter` from the settings row and treats a MISSING entry as
// disabled (`normalizeCapEntry` returns the zero value -> Enabled false), so a
// card that defaulted an absent pool to ON read ON while nothing was injected:
// the operator saw a working Vision Adapter and still got "this model cannot
// read images". An entry that IS stored keeps the backend's own default, where a
// map without an explicit `enabled` is enabled and the legacy array form is
// enabled too.
describe('parseCapacityAdapterSettings', () => {
  it('reports an absent pool as disabled, matching the gateway', () => {
    expect(parseCapacityAdapterSettings(undefined).vision.enabled).toBe(false)
    expect(parseCapacityAdapterSettings({}).vision.enabled).toBe(false)
    expect(parseCapacityAdapterSettings({ vision: undefined }).audioInput.enabled).toBe(false)
  })

  it('keeps an explicit flag and the backend defaults for a stored entry', () => {
    expect(parseCapacityAdapterSettings({ vision: { enabled: true } }).vision.enabled).toBe(true)
    expect(parseCapacityAdapterSettings({ vision: { enabled: false } }).vision.enabled).toBe(false)
    // Stored map without the flag, and the legacy array form: enabled upstream.
    expect(parseCapacityAdapterSettings({ vision: { models: ['ag/x'] } }).vision.enabled).toBe(true)
    expect(parseCapacityAdapterSettings({ vision: ['ag/x'] }).vision.enabled).toBe(true)
  })

  it('carries the models list through and defaults it to empty', () => {
    expect(parseCapacityAdapterSettings({ vision: { models: ['a/b', 'c/d'] } }).vision.models).toEqual([
      'a/b',
      'c/d',
    ])
    expect(parseCapacityAdapterSettings({ vision: { enabled: true } }).vision.models).toEqual([])
  })
})
