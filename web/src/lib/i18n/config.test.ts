import { describe, expect, test } from 'bun:test'
import { normalizeLocale, isSupportedLocale, LOCALES } from './config'

describe('i18n config', () => {
  test('only en and es are supported', () => {
    expect(LOCALES).toEqual(['en', 'es'])
    expect(isSupportedLocale('en')).toBe(true)
    expect(isSupportedLocale('es')).toBe(true)
    expect(isSupportedLocale('fr')).toBe(false)
  })

  test('normalizeLocale maps Spain and regional Spanish to es', () => {
    expect(normalizeLocale('es')).toBe('es')
    expect(normalizeLocale('es-ES')).toBe('es')
    expect(normalizeLocale('es-MX')).toBe('es')
    expect(normalizeLocale('en-GB')).toBe('en')
    expect(normalizeLocale('fr')).toBe('en')
    expect(normalizeLocale(undefined)).toBe('en')
  })
})
