import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'bun:test'
import {
  getProvidersByKind,
  isChatProvider,
  MEDIA_PROVIDER_KINDS,
  PROVIDER_CATALOG,
} from './providers'
import { getModelsByProviderId, PROVIDER_ID_TO_ALIAS } from './models'
import { pathToTab, TAB_ROUTES } from './router'

describe('providers & media separation', () => {
  it('correctly classifies chat vs pure media providers', () => {
    const openai = PROVIDER_CATALOG.find((p) => p.id === 'openai')
    const anthropic = PROVIDER_CATALOG.find((p) => p.id === 'anthropic')
    const elevenlabs = PROVIDER_CATALOG.find((p) => p.id === 'elevenlabs')
    const deepgram = PROVIDER_CATALOG.find((p) => p.id === 'deepgram')
    const bfl = PROVIDER_CATALOG.find((p) => p.id === 'black-forest-labs')
    const voyage = PROVIDER_CATALOG.find((p) => p.id === 'voyage-ai')
    const runway = PROVIDER_CATALOG.find((p) => p.id === 'runwayml')
    const brave = PROVIDER_CATALOG.find((p) => p.id === 'brave-search')

    expect(Boolean(openai && isChatProvider(openai))).toBe(true)
    expect(Boolean(anthropic && isChatProvider(anthropic))).toBe(true)
    expect(Boolean(elevenlabs && !isChatProvider(elevenlabs))).toBe(true)
    expect(Boolean(deepgram && !isChatProvider(deepgram))).toBe(true)
    expect(Boolean(bfl && !isChatProvider(bfl))).toBe(true)
    expect(Boolean(voyage && !isChatProvider(voyage))).toBe(true)
    expect(Boolean(runway && !isChatProvider(runway))).toBe(true)
    expect(Boolean(brave && !isChatProvider(brave))).toBe(true)
  })

  it('retrieves providers by kind', () => {
    const ttsProviders = getProvidersByKind('tts')
    expect(ttsProviders.some((p) => p.id === 'elevenlabs')).toBe(true)
    expect(ttsProviders.some((p) => p.id === 'fish-audio')).toBe(true)
    expect(ttsProviders.some((p) => p.id === 'openai')).toBe(true)
    expect(ttsProviders.some((p) => p.id === 'anthropic')).toBe(false)

    const sttProviders = getProvidersByKind('stt')
    expect(sttProviders.some((p) => p.id === 'assemblyai')).toBe(true)
    expect(sttProviders.some((p) => p.id === 'deepgram')).toBe(true)
    expect(sttProviders.some((p) => p.id === 'openai')).toBe(true)

    const imageProviders = getProvidersByKind('image')
    expect(imageProviders.some((p) => p.id === 'black-forest-labs')).toBe(true)
    expect(imageProviders.some((p) => p.id === 'fal-ai')).toBe(true)
    expect(imageProviders.some((p) => p.id === 'runwayml')).toBe(true)

    const videoProviders = getProvidersByKind('video')
    expect(videoProviders.some((p) => p.id === 'runwayml')).toBe(true)

    const embeddingProviders = getProvidersByKind('embedding')
    expect(embeddingProviders.some((p) => p.id === 'voyage-ai')).toBe(true)
    expect(embeddingProviders.some((p) => p.id === 'openai')).toBe(true)

    expect(MEDIA_PROVIDER_KINDS.length >= 6).toBe(true)
  })

  it('maps media router routes correctly', () => {
    expect(pathToTab('/dashboard/media-providers/embedding')).toBe('media-embedding')
    expect(pathToTab('/dashboard/media-providers/image')).toBe('media-image')
    expect(pathToTab('/dashboard/media-providers/tts')).toBe('media-tts')
    expect(pathToTab('/dashboard/media-providers/stt')).toBe('media-stt')
    expect(pathToTab('/dashboard/media-providers/video')).toBe('media-video')
    expect(pathToTab('/dashboard/media-providers/web')).toBe('media-web')

    expect(TAB_ROUTES['media-embedding']).toBe('/dashboard/media-providers/embedding')
    expect(TAB_ROUTES['media-image']).toBe('/dashboard/media-providers/image')
    expect(TAB_ROUTES['media-tts']).toBe('/dashboard/media-providers/tts')
    expect(TAB_ROUTES['media-stt']).toBe('/dashboard/media-providers/stt')
    expect(TAB_ROUTES['media-video']).toBe('/dashboard/media-providers/video')
    expect(TAB_ROUTES['media-web']).toBe('/dashboard/media-providers/web')
  })

  it('marks clinepass as dual-auth (oauth + apikey, upstream parity)', () => {
    const clinepass = PROVIDER_CATALOG.find((p) => p.id === 'clinepass')
    expect(clinepass).toBeDefined()
    if (!clinepass) throw new Error('missing clinepass')
    expect(clinepass.category).toBe('oauth')
    expect(clinepass.authModes?.includes('oauth')).toBe(true)
    expect(clinepass.authModes?.includes('apikey')).toBe(true)
    expect(clinepass.website).toBe('https://cline.bot')
    expect(clinepass.notice?.signupUrl).toBe('https://app.cline.bot')
  })

  it('points SenseNova at the international console key page', () => {
    const sensenova = PROVIDER_CATALOG.find((p) => p.id === 'sensenova')
    expect(sensenova).toBeDefined()
    if (!sensenova) throw new Error('missing sensenova')
    expect(sensenova.website).toBe('https://platform.sensenova.ai')
    expect(sensenova.notice?.apiKeyUrl).toBe('https://platform.sensenova.ai/console/keys')
  })

  it('marks all upstream dual-auth providers (oauth+apikey registry modes)', () => {
    const dual = [
      'clinepass',
      'codebuddy-cn',
      'codebuddy-intl',
      'kimchi',
      'kimi',
      'nous',
      'qoder',
      'windsurf',
      'xai',
      'xiaomi-mimo',
    ]
    for (const id of dual) {
      const p = PROVIDER_CATALOG.find((e) => e.id === id)
      expect(p).toBeDefined()
      if (!p) throw new Error(`missing ${id}`)
      expect(p.authModes?.includes('oauth')).toBe(true)
      expect(p.authModes?.includes('apikey')).toBe(true)
    }
  })

  it('matches upstream oauth category for trae and windsurf', () => {
    expect(PROVIDER_CATALOG.find((p) => p.id === 'trae')?.category).toBe('oauth')
    expect(PROVIDER_CATALOG.find((p) => p.id === 'windsurf')?.category).toBe('oauth')
  })

  it('carries upstream display links (website/notice) for all registry providers', () => {
    // No upstream registry counterpart (Go-only entries), or no display link
    // upstream by design (mimo-free, mmf, opencode).
    const exempt = new Set(['freebuff', 'kimi-coding', 'mimo-free', 'mmf', 'opencode'])
    for (const p of PROVIDER_CATALOG) {
      if (exempt.has(p.id)) continue
      expect(Boolean(p.website || p.notice || p.authType || p.authHint)).toBe(true)
    }
    const grokWeb = PROVIDER_CATALOG.find((p) => p.id === 'grok-web')
    expect(grokWeb?.authType).toBe('cookie')
    expect(Boolean(grokWeb?.authHint)).toBe(true)
    const openai = PROVIDER_CATALOG.find((p) => p.id === 'openai')
    expect(openai?.notice?.apiKeyUrl).toBe('https://platform.openai.com/api-keys')
  })

  it('keeps self-hosted/image providers on required API key (upstream parity)', () => {
    for (const id of ['comfyui', 'sdwebui', 'recraft']) {
      const p = PROVIDER_CATALOG.find((e) => e.id === id)
      expect(p).toBeDefined()
      if (!p) throw new Error(`missing ${id}`)
      expect(p.category).toBe('apikey')
      expect(p.noAuth).toBeUndefined()
    }
  })

  it('marks devin-cli as free noAuth (upstream parity)', () => {
    const p = PROVIDER_CATALOG.find((e) => e.id === 'devin-cli')
    expect(p).toBeDefined()
    if (!p) throw new Error('missing devin-cli')
    expect(p.category).toBe('free')
    expect(p.noAuth).toBe(true)
  })

  it('matches upstream free category for gemini-cli', () => {
    expect(PROVIDER_CATALOG.find((p) => p.id === 'gemini-cli')?.category).toBe('free')
  })

  it('includes hidden mmf provider (upstream parity)', () => {
    const p = PROVIDER_CATALOG.find((e) => e.id === 'mmf')
    expect(p).toBeDefined()
    if (!p) throw new Error('missing mmf')
    expect(p.category).toBe('apikey')
    expect(p.hidden).toBe(true)
  })
  it('wires the five v0.5.91 aggregators (tokenharbor, dahl, atria, agnes, bai)', () => {
    const expected: Record<string, { category: 'apikey' | 'freeTier'; models: number; fetcher: boolean }> = {
      tokenharbor: { category: 'apikey', models: 10, fetcher: true },
      dahl: { category: 'apikey', models: 3, fetcher: true },
      atria: { category: 'apikey', models: 1, fetcher: false },
      agnes: { category: 'freeTier', models: 5, fetcher: false },
      bai: { category: 'apikey', models: 0, fetcher: true },
    }
    for (const [id, want] of Object.entries(expected)) {
      const p = PROVIDER_CATALOG.find((e) => e.id === id)
      expect(p).toBeDefined()
      if (!p) throw new Error(`missing ${id}`)
      expect(p.category).toBe(want.category)
      expect(isChatProvider(p)).toBe(true)
      expect(p.alias).toBe(id)
      expect(getModelsByProviderId(id).length).toBe(want.models)
      expect(Boolean(p.modelsFetcher)).toBe(want.fetcher)
    }
  })

  it('wires the sumopod provider end-to-end', () => {
    const p = PROVIDER_CATALOG.find((e) => e.id === 'sumopod')
    expect(p).toBeDefined()
    if (!p) throw new Error('missing sumopod')
    expect(p.category).toBe('apikey')
    expect(isChatProvider(p)).toBe(true)
    expect(getModelsByProviderId('sumopod').length).toBe(61)
    expect(Boolean(p.modelsFetcher)).toBe(true)
    expect(p.notice?.apiKeyUrl).toBe('https://sumopod.com/dashboard/api-services/keys')
  })

  it('wires the Requesty AI router end-to-end', () => {
    const p = PROVIDER_CATALOG.find((e) => e.id === 'requesty')
    expect(p).toBeDefined()
    if (!p) throw new Error('missing requesty')
    expect(p.category).toBe('apikey')
    expect(p.alias).toBe('rq')
    expect(isChatProvider(p)).toBe(true)
    expect(p.modelsFetcher?.type).toBe('requesty-free')
    expect(p.modelsFetcher?.url).toBe('https://router.requesty.ai/v1/models')
    // The curated fallback must carry the model the operator pasted, and its
    // vendor-prefixed wire id, not the bare card title.
    const models = getModelsByProviderId('requesty').map((m) => m.id)
    expect(models.includes('nvidia/muse-glimmer-30b')).toBe(true)
  })

  it('resolves every provider alias to a catalog with an entry', () => {
    const missing = Object.keys(PROVIDER_ID_TO_ALIAS).filter(
      (id) => !PROVIDER_CATALOG.some((p) => p.id === id || p.alias === id)
    )
    expect(missing).toEqual([])
  })

  it('files NaN.Builders under the OAuth overview section without changing its auth', () => {
    // The operator wants it listed with the OAuth providers, but the provider
    // authenticates with a bearer API key: `category` must stay freeTier so the
    // detail page keeps opening the API-key modal instead of a nonexistent
    // OAuth flow. `uiSection` moves only the card.
    const nan = PROVIDER_CATALOG.find((p) => p.id === 'nan')
    expect(nan).toBeDefined()
    if (!nan) throw new Error('missing nan')
    expect(nan.category).toBe('freeTier')
    expect(nan.uiSection).toBe('oauth')
    expect(nan.authType).toBe('apikey')

    const inOauthSection = PROVIDER_CATALOG.filter(
      (p) => isChatProvider(p) && !p.hidden && (p.category === 'oauth' || p.uiSection === 'oauth')
    )
    expect(inOauthSection.some((p) => p.id === 'nan')).toBe(true)

    const inFreeTierSection = PROVIDER_CATALOG.filter(
      (p) => isChatProvider(p) && !p.hidden && p.uiSection !== 'oauth' && (p.category === 'free' || p.category === 'freeTier')
    )
    expect(inFreeTierSection.some((p) => p.id === 'nan')).toBe(false)
  })

  it('ships artwork for every provider the dashboard can render', () => {
    // getIconPath derives the URL from the catalog id, so a catalog entry
    // without an asset under web/public/providers is a 404 on every surface
    // that draws it. Only the alias table may point at another provider's file.
    const providersDir = join(import.meta.dirname, '../../public/providers')
    const assets = new Set(readdirSync(providersDir))
    const shared = new Set(['zai-search'])
    const missing = PROVIDER_CATALOG.filter(
      (p) => !shared.has(p.id) && !assets.has(`${p.id}.png`) && !assets.has(`${p.id}.svg`)
    ).map((p) => p.id)
    expect(missing).toEqual([])
  })

  it('ships a real brand mark, never an initials badge, for every catalog provider', () => {
    // A letter badge is not a logo: it renders the provider's initials in a
    // flat circle and reads as a placeholder, which is what the operator asked
    // to be replaced with the official, coloured artwork. An SVG that draws
    // text is the fingerprint of that placeholder, so the check is structural
    // rather than a per-provider allowlist that would rot.
    const providersDir = join(import.meta.dirname, '../../public/providers')
    const assets = readdirSync(providersDir)
    const badges = PROVIDER_CATALOG.map((p) => {
      const svg = assets.find((f) => f === `${p.id}.svg`)
      if (!svg) return null
      const content = readFileSync(join(providersDir, svg), 'utf8')
      return /<text[\s>]/.test(content) ? p.id : null
    }).filter((id): id is string => id !== null)
    expect(badges).toEqual([])
  })

  it('wires the 7 freellmapi providers end-to-end', () => {
    const expected: Record<string, { alias: string; models: number; noAuth: boolean }> = {
      iteracompute: { alias: 'itc', models: 1, noAuth: false },
      ainative: { alias: 'ain', models: 2, noAuth: false },
      navyai: { alias: 'navy', models: 3, noAuth: false },
      anyapi: { alias: 'any', models: 4, noAuth: false },
      ovhcloud: { alias: 'ovh', models: 2, noAuth: true },
      sealion: { alias: 'sl', models: 2, noAuth: false },
      longcat: { alias: 'lc', models: 2, noAuth: false },
    }
    for (const [id, want] of Object.entries(expected)) {
      const p = PROVIDER_CATALOG.find((e) => e.id === id)
      expect(p).toBeDefined()
      if (!p) throw new Error(`missing ${id}`)
      expect(p.alias).toBe(want.alias)
      expect(p.noAuth).toBe(want.noAuth)
      expect(p.category).toBe('freeTier')
      expect(isChatProvider(p)).toBe(true)
      expect(getModelsByProviderId(id).length).toBe(want.models)
      expect(Boolean(p.modelsFetcher)).toBe(true)
    }
  })
})
