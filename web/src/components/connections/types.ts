import { api, getAuthHeaders, type ProviderConnection } from '../../api/client'
import { parseCustomModelsResponse, parseDisabledModelsMap } from '../../lib/customModels'
import { getModelCaps, getModelKind } from '../../lib/models'
import { PROVIDER_CATALOG, PROVIDER_CATALOG_MAP, type ProviderCatalogItem } from '../../lib/providers'

export const MEDIA_KINDS: Record<string, true> = {
  image: true,
  tts: true,
  stt: true,
  embedding: true,
  video: true,
}

export function isChatModel(m: unknown): boolean {
  const kind = getModelKind(m)
  if (!kind || kind === 'llm' || kind === 'chat') {
    const obj = typeof m === 'object' && m !== null ? (m as { kind?: string; type?: string }) : null
    return !(obj?.kind && MEDIA_KINDS[obj.kind]) && !(obj?.type && MEDIA_KINDS[obj.type])
  }
  return false
}

export interface ProviderStats {
  total: number
  connected: number
  errorCount: number
  allDisabled: boolean
  latestError?: string | null
  connections: ProviderConnection[]
}

export interface ModelItem {
  id: string
  name?: string
  isCustom?: boolean
  caps: { vision: boolean; reasoning: boolean }
  kind?: string
}

export interface CustomModelData {
  id: string
  name?: string
  providerAlias?: string
  type?: string
  /** Capability flags saved from the Add Custom Model modal (upstream parity). */
  caps?: { vision?: boolean; reasoning?: boolean }
}

// Artwork is filed under another provider's id when the two genuinely share a
// brand. `zai-search` is a Go registry entry with no upstream counterpart: it is
// a web-search seat on the GLM/Zhipu account pool (CredentialFallbacks maps it
// to `glm`), so it wears GLM's mark. Without this the tile asks for a file that
// is never shipped and pays a 404 before the badge fallback hides it. Upstream
// aliases several other ids the same way (`perplexity-agent`, `vercel-ai-gateway`,
// `minimax-code`, `opencode-zen`, `ollama-search`) but this repo ships distinct
// artwork for all of those, so aliasing them here would throw away a real logo.
const ICON_ALIASES: Record<string, string> = {
  'zai-search': 'glm',
}

// Which extension each provider's artwork is filed under. Upstream ships PNG
// only, but lobe-icons and the providers' own brand kits hand us SVG, so the
// folder is mixed. getIconPath used to append ".png" unconditionally, which
// turned every SVG-backed provider — Replicate, Moonshot, Zhipu, Alibaba,
// ModelScope, NaraRouter — into a 404 and then into an initials badge, as if no
// artwork had shipped at all. Listing the real filename keeps that a lookup
// instead of a guess, so a missing entry still resolves to the generic glyph
// rather than to a file that is not there.
const ICON_FILES: Record<string, string> = {
  '360ai': '360ai.svg',
  'agentrouter': 'agentrouter.png',
  'agnes': 'agnes.png',
  'alibaba': 'alibaba.svg',
  'alicode-intl': 'alicode-intl.png',
  'alicode': 'alicode.png',
  'alims-intl': 'alims-intl.png',
  'alitp-intl': 'alitp-intl.png',
  'amp': 'amp.png',
  'anthropic-m': 'anthropic-m.png',
  'anthropic': 'anthropic.png',
  'antigravity': 'antigravity.png',
  'api-airforce': 'api-airforce.png',
  'arcee-ai': 'arcee-ai.svg',
  'arcee': 'arcee.svg',
  'assemblyai': 'assemblyai.png',
  'atria': 'atria.png',
  'auggie': 'auggie.svg',
  'aws-polly': 'aws-polly.png',
  'aws': 'aws.svg',
  'azure': 'azure.png',
  'azureai': 'azureai.svg',
  'bai': 'bai.png',
  'baichuan': 'baichuan.svg',
  'baidu': 'baidu.png',
  'bailian': 'bailian.svg',
  'baseten': 'baseten.svg',
  'bazaarlink': 'bazaarlink.png',
  'bedrock-xai': 'bedrock-xai.png',
  'bedrock': 'bedrock.png',
  'black-forest-labs': 'black-forest-labs.png',
  'blackbox': 'blackbox.png',
  'bluesminds': 'bluesminds.png',
  'brave-search': 'brave-search.png',
  'byteplus': 'byteplus.png',
  'bytez': 'bytez.svg',
  'cartesia': 'cartesia.png',
  'cerebras': 'cerebras.png',
  'charm-hyper': 'charm-hyper.svg',
  'chutes': 'chutes.png',
  'claude-web': 'claude-web.svg',
  'claude': 'claude.png',
  'cli-generic': 'cli-generic.svg',
  'cline': 'cline.png',
  'clinepass': 'clinepass.png',
  'cloudflare-ai': 'cloudflare-ai.png',
  'cloudflare': 'cloudflare.svg',
  'codebuddy-cn': 'codebuddy-cn.png',
  'codebuddy-intl': 'codebuddy-intl.png',
  'codewhale': 'codewhale.svg',
  'codex': 'codex.png',
  'cohere': 'cohere.png',
  'comfyui': 'comfyui.png',
  'commandcode': 'commandcode.png',
  'continue': 'continue.png',
  'copilot': 'copilot.png',
  'coqui': 'coqui.png',
  'coze': 'coze.svg',
  'crof': 'crof.svg',
  'crush': 'crush.png',
  'cursor': 'cursor.png',
  'dahl': 'dahl.png',
  'deepgram': 'deepgram.png',
  'deepinfra': 'deepinfra.svg',
  'deepseek-tui': 'deepseek-tui.png',
  'deepseek-web': 'deepseek-web.png',
  'deepseek': 'deepseek.png',
  'devin-cli': 'devin-cli.png',
  'dgrid': 'dgrid.svg',
  'dify': 'dify.svg',
  'dit': 'dit.svg',
  'doubao': 'doubao.svg',
  'droid': 'droid.png',
  'edge-tts': 'edge-tts.png',
  'elevenlabs': 'elevenlabs.png',
  'exa': 'exa.png',
  'factory': 'factory.svg',
  'fal-ai': 'fal-ai.png',
  'fal': 'fal.svg',
  'featherless': 'featherless.png',
  'firecrawl': 'firecrawl.png',
  'fireworks': 'fireworks.png',
  'fish-audio': 'fish-audio.png',
  'forge': 'forge.png',
  'freeaiapikey': 'freeaiapikey.svg',
  'freebuff': 'freebuff.png',
  'freemodel-dev': 'freemodel-dev.svg',
  'friendli': 'friendli.svg',
  'galadriel': 'galadriel.svg',
  'gemini-cli': 'gemini-cli.png',
  'gemini-web': 'gemini-web.png',
  'gemini': 'gemini.png',
  'github': 'github.png',
  'gitlab': 'gitlab.png',
  'gitlawb-gmi': 'gitlawb-gmi.svg',
  'gitlawb': 'gitlawb.svg',
  'glm-cn': 'glm-cn.png',
  'glm': 'glm.png',
  'gonkabroker': 'gonkabroker.png',
  'google-pse': 'google-pse.png',
  'google-tts': 'google-tts.png',
  'google': 'google.svg',
  'grok-cli': 'grok-cli.png',
  'grok-web': 'grok-web.png',
  'grok': 'grok.svg',
  'groq': 'groq.png',
  'hcnsec': 'hcnsec.svg',
  'hermes': 'hermes.png',
  'heroku': 'heroku.svg',
  'huggingchat': 'huggingchat.svg',
  'huggingface': 'huggingface.png',
  'hyperbolic': 'hyperbolic.png',
  'ibm': 'ibm.svg',
  'iflow': 'iflow.png',
  'iflytek': 'iflytek.svg',
  'inclusionai': 'inclusionai.svg',
  'inference': 'inference.svg',
  'inworld': 'inworld.png',
  'jcode': 'jcode.png',
  'jina-ai': 'jina-ai.png',
  'jina-reader': 'jina-reader.png',
  'kenari': 'kenari.svg',
  'kilo-gateway': 'kilo-gateway.png',
  'kilocode': 'kilocode.png',
  'kimi-coding': 'kimi-coding.png',
  'kimi-logomark-dark': 'kimi-logomark-dark.svg',
  'kimi-logomark-light': 'kimi-logomark-light.svg',
  'kimi': 'kimi.png',
  'kiro': 'kiro.png',
  'krutrim': 'krutrim.svg',
  'lambda': 'lambda.svg',
  'linkup': 'linkup.png',
  'liquid': 'liquid.svg',
  'llm7': 'llm7.png',
  'local-device': 'local-device.png',
  'longcat': 'longcat.png',
  'meta': 'meta.svg',
  'metaai': 'metaai.svg',
  'mimo-free': 'mimo-free.png',
  'minimax-cn': 'minimax-cn.png',
  'minimax-code-global': 'minimax-code-global.png',
  'minimax-code': 'minimax-code.png',
  'minimax': 'minimax.png',
  'mistral': 'mistral.png',
  'mixlayer': 'mixlayer.png',
  'mmf': 'mmf.png',
  'modelscope': 'modelscope.svg',
  'monsterapi': 'monsterapi.svg',
  'moonshot': 'moonshot.svg',
  'morph': 'morph.png',
  'muse': 'muse.png',
  'nanobanana': 'nanobanana.png',
  'nararouter': 'nararouter.svg',
  'nebius': 'nebius.png',
  'nomic': 'nomic.svg',
  'notrack-web': 'notrack-web.svg',
  'nous': 'nous.png',
  'novita': 'novita.png',
  'nube': 'nube.svg',
  'nvidia': 'nvidia.png',
  'oai-cc': 'oai-cc.png',
  'oai-r': 'oai-r.png',
  'ollama-local': 'ollama-local.png',
  'ollama-search': 'ollama-search.png',
  'ollama': 'ollama.png',
  'omp': 'omp.png',
  'onomeo': 'onomeo.svg',
  'openai': 'openai.png',
  'openclaw': 'openclaw.png',
  'opencode-go': 'opencode-go.png',
  'opencode-zen': 'opencode-zen.png',
  'opencode': 'opencode.png',
  'opendesign': 'opendesign.png',
  'openrouter': 'openrouter.png',
  'opper': 'opper.svg',
  'orcarouter': 'orcarouter.svg',
  'ovhcloud': 'ovhcloud.svg',
  'perplexity-agent': 'perplexity-agent.png',
  'perplexity-web': 'perplexity-web.png',
  'perplexity': 'perplexity.png',
  'phind': 'phind.svg',
  'picoclaw': 'picoclaw.svg',
  'pioneer': 'pioneer.svg',
  'playht': 'playht.png',
  'poe': 'poe.svg',
  'pollinations': 'pollinations.svg',
  'poolside': 'poolside.png',
  'publicai': 'publicai.svg',
  'qoder-cn': 'qoder-cn.png',
  'qoder': 'qoder.png',
  'qwen': 'qwen.png',
  'recraft': 'recraft.png',
  'reka': 'reka.png',
  'replicate': 'replicate.svg',
  'requesty': 'requesty.svg',
  'roo': 'roo.png',
  'roocode': 'roocode.svg',
  'runway': 'runway.svg',
  'runwayml': 'runwayml.png',
  'sambanova': 'sambanova.png',
  'sdwebui': 'sdwebui.png',
  'searchapi': 'searchapi.png',
  'searxng': 'searxng.png',
  'selfhosted-embedding': 'selfhosted-embedding.png',
  'selfhosted-stt': 'selfhosted-stt.png',
  'selfhosted-tts': 'selfhosted-tts.png',
  'sensenova': 'sensenova.svg',
  'serper': 'serper.png',
  'siliconflow': 'siliconflow.png',
  'smelt': 'smelt.svg',
  'snowflake': 'snowflake.svg',
  'sparkdesk': 'sparkdesk.svg',
  'stability-ai': 'stability-ai.png',
  'stepfun': 'stepfun.svg',
  'sumopod': 'sumopod.png',
  'suno': 'suno.svg',
  't3-web': 't3-web.svg',
  'tavily': 'tavily.png',
  'tencent': 'tencent.png',
  'tinyfish': 'tinyfish.png',
  'together': 'together.png',
  'tokenharbor': 'tokenharbor.png',
  'tokenrouter': 'tokenrouter.png',
  'topaz': 'topaz.png',
  'topazlabs': 'topazlabs.svg',
  'tortoise': 'tortoise.png',
  'trae': 'trae.png',
  'udio': 'udio.svg',
  'uncloseai': 'uncloseai.svg',
  'upstage': 'upstage.svg',
  'v0': 'v0.svg',
  'v1m': 'v1m.png',
  'venice': 'venice.png',
  'veoaifree-web': 'veoaifree-web.svg',
  'vercel-ai-gateway': 'vercel-ai-gateway.png',
  'vercel': 'vercel.png',
  'vertex-partner': 'vertex-partner.png',
  'vertex': 'vertex.png',
  'vllm': 'vllm.svg',
  'volcengine-ark': 'volcengine-ark.png',
  'volcengine': 'volcengine.svg',
  'voyage-ai': 'voyage-ai.png',
  'voyage': 'voyage.svg',
  'wafer': 'wafer.svg',
  'windsurf': 'windsurf.png',
  'workbuddy': 'workbuddy.png',
  'x5lab': 'x5lab.svg',
  'xai': 'xai.png',
  'xiaomi-mimo': 'xiaomi-mimo.png',
  'xiaomi-tokenplan': 'xiaomi-tokenplan.png',
  'xinference': 'xinference.svg',
  'xquik': 'xquik.png',
  'yi': 'yi.svg',
  'youcom': 'youcom.png',
  'yuanbao-web': 'yuanbao-web.svg',
  'zed-hosted': 'zed-hosted.svg',
  'zed': 'zed.png',
  'zenmux-free': 'zenmux-free.svg',
  'zenmux': 'zenmux.svg',
  'zhipu': 'zhipu.svg',
}

function genericGlyph(apiType?: string): string {
  return apiType === 'responses' ? '/providers/oai-r.png' : '/providers/oai-cc.png'
}

// Resolves a catalog id or uiAlias to the id its artwork would be filed under.
// Returns undefined when neither matches, which is the signal for the caller to
// fall through to the generic OpenAI glyph.
function resolveIconId(id?: string | null): string | undefined {
  const clean = id?.trim()
  if (!clean) return undefined
  return ICON_ALIASES[clean]
    ?? (PROVIDER_CATALOG_MAP.has(clean) ? clean : PROVIDER_CATALOG.find((p) => p.alias === clean)?.id)
}

export function getIconPath(id?: string | null, apiType?: string): string {
  if (!id) return '/providers/oai-cc.png'
  const clean = id.trim()
  if (clean.startsWith('openai-compatible')) {
    return apiType === 'responses' ? '/providers/oai-r.png' : '/providers/oai-cc.png'
  }
  if (clean.startsWith('anthropic-compatible') || clean.includes('anthropic')) {
    return '/providers/anthropic-m.png'
  }
  const fileId = resolveIconId(clean)
  const file = fileId ? ICON_FILES[fileId] : undefined
  if (file) {
    return `/providers/${file}`
  }
  return genericGlyph(apiType)
}

/**
 * Every URL worth trying for a provider's artwork, best first.
 *
 * **Colour beats monochrome, and that rule orders everything below.** A
 * monochrome mark inherits the surrounding text colour, so in a dark theme it
 * renders white and in a light theme black — legible, but no longer the brand.
 * A colour mark is recognisable on either background, so it is always preferred
 * even when the monochrome one lives on a higher-coverage source.
 *
 * The shipped file comes first because it is the only candidate that cannot
 * fail at runtime: it is served from the app's own origin, so it costs no
 * cross-origin request and no CDN dependency.
 *
 * Then, measured against the catalog in `web/src/lib/providers.ts` (150 ids)
 * rather than taken from what each source advertises:
 *
 * 1. LobeHub colour (`{id}-color.svg`): the AI-native set. Not every brand has
 *    a colour variant (`anthropic-color` 404s, `claude-color` exists), so the
 *    plain file follows as its own step rather than being assumed present.
 * 2. models.dev: 30 real matches, mapped by provider id, multicolour.
 * 3. LobeHub monochrome (`{id}.svg`): `currentColor`.
 * 4. Simple Icons: always single-colour, so it is the last resort even though
 *    its CC0 licence is the most permissive.
 *
 * models.dev needs care. `models.dev/logos/{id}.svg` answers **200 with the
 * same generic mark** for an id it does not know — a made-up provider and
 * `gonkabroker` return byte-identical files (119 of 150 ids hit it). So a
 * models.dev candidate is only offered for an id the scan found a real logo
 * for; see `MODELS_DEV_IDS`. Without that list every unknown provider would
 * paint a wrong logo with confidence, which is worse than the generic glyph.
 *
 * SVGL is not wired. Its files come from `svgl.app/library/{file}.svg`, but the
 * filename does not follow the provider id (`openai.svg` exists,
 * `anthropic.svg` and `mistral.svg` do not), so no URL can be derived without
 * the API.
 *
 * Only catalog ids expand to CDN candidates. A custom-endpoint node has no
 * brand to look up, and guessing would turn a deliberate generic glyph into
 * some unrelated provider's mark.
 */
const LOBE_VERSION = '1.101.0'

const MODELS_DEV_IDS: Record<string, true> = {
  'agnes': true, 'anthropic': true, 'azure': true, 'cerebras': true, 'cohere': true,
  'commandcode': true, 'daoxe': true, 'deepseek': true, 'gitlab': true, 'groq': true,
  'huggingface': true, 'minimax': true, 'minimax-cn': true, 'mistral': true,
  'mixlayer': true, 'nan': true, 'nebius': true, 'nvidia': true, 'openai': true,
  'opencode': true, 'opencode-go': true, 'openrouter': true, 'perplexity': true,
  'perplexity-agent': true, 'poolside': true, 'sambanova': true, 'siliconflow': true,
  'tokenrouter': true, 'venice': true, 'xai': true,
}

/**
 * The CDN steps, in order, for a provider with no shipped artwork.
 *
 * Split out so the colour-before-monochrome ordering is assertable: `mono`
 * marks the point where every remaining step is single-colour, and a test
 * checks that no colour source follows one. Building the list inline made the
 * order only observable by inspecting strings, which a test can get wrong
 * without noticing.
 */
export function getRemoteSources(fileId: string): { url: string; mono: boolean }[] {
  const lobe = (file: string) => `https://unpkg.com/@lobehub/icons-static-svg@${LOBE_VERSION}/icons/${file}`
  const sources = [
    { url: lobe(`${fileId}-color.svg`), mono: false },
    ...(MODELS_DEV_IDS[fileId] ? [{ url: `https://models.dev/logos/${fileId}.svg`, mono: false }] : []),
    { url: lobe(`${fileId}.svg`), mono: true },
    { url: `https://cdn.simpleicons.org/${fileId}`, mono: true },
  ]
  return sources
}

export function getIconCandidates(id?: string | null, apiType?: string): string[] {
  const local = getIconPath(id, apiType)
  // A shipped file ends the search: a CDN would only re-render the same brand,
  // over the network, and could disagree with the shipped mark.
  if (local !== genericGlyph(apiType)) return [local]
  const fileId = resolveIconId(id)
  if (!fileId) return [local]
  return [local, ...getRemoteSources(fileId).map((s) => s.url)]
}

// Provider initials badge, used when a provider has no shipped PNG. The
// catalog already carries a brand colour for every entry, so the badge is drawn
// in the provider's own colour rather than a neutral grey — the tile reads as
// intentional instead of as a failed image load.
export function getProviderGlyph(
  id?: string | null
): { name: string; color: string; initials: string } {
  const clean = id?.trim()
  const entry = clean
    ? (PROVIDER_CATALOG_MAP.get(clean) ?? PROVIDER_CATALOG.find((p) => p.alias === clean))
    : undefined
  const name = entry?.name ?? clean ?? 'OpenAI'
  // Take the initials of the leading words, so "B.AI" reads "BA" rather than
  // collapsing to a single letter, and "Dahl Inference" reads "DI".
  const words = name.split(/[\s._/-]+/).filter(Boolean)
  const initials = (words.length > 1 ? words[0][0] + words[1][0] : words[0]?.slice(0, 2) ?? '?').toUpperCase()
  return { name, color: entry?.color ?? '#6B7280', initials }
}

export function getProviderStats(
  connections: ProviderConnection[],
  providerId: string,
  authTypes?: string[]
): ProviderStats {
  const list = connections.filter((c) => {
    if (c.provider !== providerId) return false
    if (authTypes && authTypes.length > 0) {
      return authTypes.includes(c.authType)
    }
    return true
  })

  const total = list.length
  const isConnError = (c: ProviderConnection) =>
    c.testStatus === 'error' || c.testStatus === 'failed' || (c.testStatus !== 'active' && c.testStatus !== 'passed' && !!c.lastError)
  const isConnConnected = (c: ProviderConnection) =>
    c.isActive === 1 && (c.testStatus === 'active' || c.testStatus === 'passed' || (c.testStatus !== 'error' && c.testStatus !== 'failed' && !c.lastError))

  const connected = list.filter(isConnConnected).length
  const errorCount = list.filter(isConnError).length
  const allDisabled = total > 0 && list.every((c) => c.isActive === 0)
  const latestError = list.find(isConnError)?.lastError

  return { total, connected, errorCount, allDisabled, latestError, connections: list }
}


// providerAuthTypes returns the stored authType values a provider's card has to
// count, given the types its section is about.
//
// The card used to be handed one fixed list per section — ['apikey','api_key']
// for the providers the API Key section renders — and a credential stored under
// any other type was therefore counted as none. A browser-login provider is the
// case that made it visible: its add modal derives 'cookie' from the catalog's
// webCookie category, so `deepseek-web` held a live connection while its tile
// read "No connections" and the detail page listed it. The types the catalog
// declares are added to the section's list rather than replacing it, so this can
// only widen what a card counts — no provider that used to match stops matching.
export function providerAuthTypes(
  item: Pick<ProviderCatalogItem, 'authType' | 'authModes'> | undefined,
  sectionTypes: string[]
): string[] {
  // The registry is not consistent about which spelling it declares, and both
  // name the same credential, so the pair always travels together — normalized
  // first, because the sections already pass both.
  const types = sectionTypes.flatMap((type) =>
    type === 'apikey' || type === 'api_key' ? ['apikey', 'api_key'] : [type]
  )
  const widened = [...new Set(types)]
  for (const declared of [item?.authType, ...(item?.authModes ?? [])]) {
    if (declared && !widened.includes(declared)) widened.push(declared)
  }
  return widened
}


export function matchesFilter(stats: ProviderStats, statusFilter: string, noAuth?: boolean): boolean {
  if (statusFilter === 'all') return true
  if (statusFilter === 'connected') return stats.connected > 0 || (!!noAuth && stats.total === 0)
  if (statusFilter === 'error') return stats.errorCount > 0
  if (statusFilter === 'disabled') return stats.allDisabled
  if (statusFilter === 'not_connected') return stats.total === 0 && !noAuth
  return true
}

// matchesSearch matches a provider by display name, id, and alias.
// Matching the name alone made providers unsearchable by their own identifier:
// typing "bai" returned "Baidu Qianfan" (a substring collision) but never "B.AI",
// and "atria-asi" / "tokenharbor" returned nothing at all — the user then
// concluded the provider was missing and built a custom node instead.
export function matchesSearch(
  name: string,
  searchQuery: string,
  ...identifiers: (string | null | undefined)[]
): boolean {
  const q = searchQuery.trim().toLowerCase()
  if (!q) return true
  return [name, ...identifiers].some((v) => v && v.toLowerCase().includes(q))
}

export async function fetchProviderModelsData(
  providerId: string,
  storageAlias: string
): Promise<{ customModels: CustomModelData[]; disabledModelIds: string[] }> {
  try {
    const [customs, disabled] = await Promise.all([
      api.getCustomModels().catch(() => ({})),
      api.getDisabledModels().catch(() => ({})),
    ])
    // Upstream parity: GET /api/models/custom -> { models: [...] }.
    const customModels = parseCustomModelsResponse(customs) as CustomModelData[]
    // Upstream parity: disabled keyed by storage alias OR providerId.
    const disMap = parseDisabledModelsMap(disabled)
    const disArr = disMap[storageAlias] || disMap[providerId] || []
    return {
      customModels,
      disabledModelIds: Array.isArray(disArr) ? disArr : [],
    }
  } catch {
    return { customModels: [], disabledModelIds: [] }
  }
}

export interface SuggestedModel {
  id: string
  name: string
  contextLength?: number
}

// In-memory cache for suggested-model catalogs (upstream parity:
// providerModelsFetcher.js CACHE_TTL_MS).
const suggestedCache = new Map<string, { data: SuggestedModel[]; expiresAt: number }>()
const SUGGESTED_CACHE_TTL_MS = 5 * 60 * 1000

/** Port of fetchSuggestedModels (upstream providerModelsFetcher.js). */
export async function fetchSuggestedModels(fetcher: {
  url: string
  type: string
}): Promise<SuggestedModel[]> {
  if (!fetcher?.url || !fetcher?.type) return []
  const cached = suggestedCache.get(fetcher.url)
  if (cached && Date.now() < cached.expiresAt) return cached.data
  try {
    const params = new URLSearchParams({ url: fetcher.url, type: fetcher.type })
    const res = await fetch(`/api/providers/suggested-models?${params}`, {
      headers: getAuthHeaders(),
    })
    if (!res.ok) return []
    const json = await res.json()
    const data = Array.isArray(json?.data) ? json.data : []
    suggestedCache.set(fetcher.url, { data, expiresAt: Date.now() + SUGGESTED_CACHE_TTL_MS })
    return data
  } catch {
    return []
  }
}

/**
 * Catalog rows first, custom models appended after — upstream keeps the registry
 * list in its own order and treats custom models as additions to it.
 */
export function buildAvailableModels(
  builtInModels: Array<{ id: string; name?: string; kind?: string; type?: string }>,
  providerCustomModels: CustomModelData[]
): ModelItem[] {
  const list: ModelItem[] = []
  const seen = new Set<string>()

  for (const bm of builtInModels) {
    if (!bm.id || seen.has(bm.id)) continue
    seen.add(bm.id)
    list.push({
      id: bm.id,
      name: bm.name,
      isCustom: false,
      caps: getModelCaps(bm.id, bm),
      kind: getModelKind(bm),
    })
  }
  for (const cm of providerCustomModels) {
    if (!cm.id || seen.has(cm.id)) continue
    seen.add(cm.id)
    list.push({
      id: cm.id,
      name: cm.name || cm.id,
      isCustom: true,
      caps: getModelCaps(cm.id, cm),
      kind: getModelKind(cm),
    })
  }
  return list
}
