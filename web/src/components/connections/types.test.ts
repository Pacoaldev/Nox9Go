import { describe, expect, test } from 'bun:test'
import { getIconCandidates, getIconPath, getProviderStats, getRemoteSources, providerAuthTypes } from './types'
import type { ProviderConnection } from '../../api/client'

function conn(overrides: Partial<ProviderConnection> = {}): ProviderConnection {
  return {
    id: 'conn-1',
    provider: 'antigravity',
    authType: 'oauth',
    isActive: 1,
    testStatus: 'active',
    lastError: null,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    ...overrides,
  } as ProviderConnection
}

describe('providerAuthTypes', () => {
  test('counts a cookie connection rendered in the API key section', () => {
    // deepseek-web stores authType 'cookie' (its add modal derives it from the
    // catalog's webCookie category) while its card lives in the API Key
    // section, so a fixed ['apikey','api_key'] list matched nothing and the
    // tile read "No connections" beside a live connection.
    const types = providerAuthTypes({ authType: 'cookie', authModes: undefined }, ['apikey', 'api_key'])
    expect(types).toContain('cookie')

    const stats = getProviderStats(
      [conn({ provider: 'deepseek-web', authType: 'cookie', isActive: 1, testStatus: 'active' })],
      'deepseek-web',
      types
    )
    expect(stats.total).toBe(1)
    expect(stats.connected).toBe(1)
  })

  test('keeps the apikey and api_key spellings interchangeable', () => {
    expect(providerAuthTypes(undefined, ['apikey'])).toContain('api_key')
    expect(providerAuthTypes(undefined, ['api_key'])).toContain('apikey')
  })

  test('leaves a section list alone when the catalog declares nothing', () => {
    expect(providerAuthTypes(undefined, ['oauth'])).toEqual(['oauth'])
  })
})


describe('getProviderStats', () => {
  test('counts active connection as connected even when soft lastError is present', () => {
    const list = [
      conn({ id: 'c1', testStatus: 'active', lastError: 'Model mimo-v2.5-free is not supported' }),
      conn({ id: 'c2', testStatus: 'active', lastError: null }),
    ]
    const stats = getProviderStats(list, 'antigravity')
    expect(stats.connected).toBe(2)
    expect(stats.errorCount).toBe(0)
  })

  test('counts failed or error status in errorCount', () => {
    const list = [
      conn({ id: 'c1', testStatus: 'error', lastError: 'Invalid API key' }),
      conn({ id: 'c2', testStatus: 'failed', lastError: 'Network timeout' }),
      conn({ id: 'c3', testStatus: 'active', lastError: null }),
    ]
    const stats = getProviderStats(list, 'antigravity')
    expect(stats.connected).toBe(1)
    expect(stats.errorCount).toBe(2)
  })

  test('counts inactive connections correctly', () => {
    const list = [
      conn({ id: 'c1', isActive: 0, testStatus: 'active' }),
      conn({ id: 'c2', isActive: 0, testStatus: 'active' }),
    ]
    const stats = getProviderStats(list, 'antigravity')
    expect(stats.allDisabled).toBe(true)
    expect(stats.connected).toBe(0)
    expect(stats.errorCount).toBe(0)
  })
})

describe('getIconPath', () => {
  test('resolves a catalog alias to the id its artwork is filed under', () => {
    expect(getIconPath('ag')).toBe('/providers/antigravity.png')
    expect(getIconPath('antigravity')).toBe('/providers/antigravity.png')
  })

  test('borrows another provider mark for an id that shares its brand', () => {
    // zai-search is a Go registry entry with no upstream counterpart, so
    // /providers/zai-search.png 404s; it resolves to GLM's mark instead.
    expect(getIconPath('zai-search')).toBe('/providers/glm.png')
  })

  test('keeps the generic glyphs for compatible nodes and unknown ids', () => {
    expect(getIconPath('openai-compatible-chat-x')).toBe('/providers/oai-cc.png')
    expect(getIconPath('openai-compatible-chat-x', 'responses')).toBe('/providers/oai-r.png')
    expect(getIconPath('anthropic-compatible-x')).toBe('/providers/anthropic-m.png')
    expect(getIconPath('my-reverse-proxy')).toBe('/providers/oai-cc.png')
    expect(getIconPath(undefined)).toBe('/providers/oai-cc.png')
  })

  test('resolves SVG artwork, not only PNG', () => {
    // The folder is mixed: lobe-icons and the providers' own brand kits ship
    // SVG. Appending ".png" unconditionally turned every one of these into a
    // 404 followed by an initials badge.
    expect(getIconPath('nararouter')).toBe('/providers/nararouter.svg')
    expect(getIconPath('onomeo')).toBe('/providers/onomeo.svg')
    expect(getIconPath('mixlayer')).toBe('/providers/mixlayer.png')
    expect(getIconPath('gonkabroker')).toBe('/providers/gonkabroker.png')
  })
})

describe('getIconCandidates', () => {
  test('stops at the shipped file, so a covered provider costs no CDN request', () => {
    expect(getIconCandidates('mistral')).toEqual(['/providers/mistral.png'])
    expect(getIconCandidates('nararouter')).toEqual(['/providers/nararouter.svg'])
    expect(getIconCandidates('onomeo')).toEqual(['/providers/onomeo.svg'])
  })

  test('an alias that resolves to shipped artwork stays local', () => {
    // zai-search is the one catalog id with no file of its own; it wears GLM's.
    expect(getIconCandidates('zai-search')).toEqual(['/providers/glm.png'])
  })

  test('never guesses for a custom-endpoint node', () => {
    // No brand to look up: a CDN lookup here would paint a random provider's
    // mark over a deliberate generic glyph.
    expect(getIconCandidates('my-reverse-proxy')).toEqual(['/providers/oai-cc.png'])
    expect(getIconCandidates(undefined)).toEqual(['/providers/oai-cc.png'])
  })
})

describe('getRemoteSources', () => {
  test('offers a colour logo before a monochrome one', () => {
    // The rule the cascade is built around: a colour mark reads as the brand on
    // either theme, a monochrome one inherits the text colour. Once a mono
    // source appears, no colour source may follow it.
    const sources = getRemoteSources('mistral')
    const firstMono = sources.findIndex((s) => s.mono)
    expect(firstMono).toBeGreaterThan(0)
    expect(sources.slice(firstMono).every((s) => s.mono)).toBe(true)
    expect(sources.slice(0, firstMono).every((s) => !s.mono)).toBe(true)
  })

  test('asks LobeHub for the "-color" variant, not the plain currentColor file', () => {
    const [first] = getRemoteSources('mistral')
    expect(first.mono).toBe(false)
    expect(first.url).toContain('/icons/mistral-color.svg')
  })

  test('only offers models.dev for ids it is known to carry', () => {
    // models.dev answers 200 with a generic mark for unknown ids, so it cannot
    // be probed — the id list is what keeps a wrong logo off the tile.
    expect(getRemoteSources('mistral').some((s) => s.url.includes('models.dev'))).toBe(true)
    expect(getRemoteSources('gonkabroker').some((s) => s.url.includes('models.dev'))).toBe(false)
  })
})