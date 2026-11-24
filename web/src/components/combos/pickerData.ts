import type { Combo, ProviderConnection, ProviderNode } from '../../api/client'
import { getModelCaps, getModelKind, getModelsByProviderId, PROVIDER_ID_TO_ALIAS } from '../../lib/models'
import { PROVIDER_CATALOG, isChatProvider } from '../../lib/providers'
import { parseSearchQuery, matchesSearch } from '../../lib/modelSearchFilter'

const MEDIA_KINDS: Record<string, true> = {
  image: true,
  tts: true,
  stt: true,
  embedding: true,
  video: true,
}

function isChatModel(m: unknown): boolean {
  const kind = getModelKind(m)
  if (!kind || kind === 'llm' || kind === 'chat') {
    const obj = typeof m === 'object' && m !== null ? (m as { kind?: string; type?: string }) : null
    return !(obj?.kind && MEDIA_KINDS[obj.kind]) && !(obj?.type && MEDIA_KINDS[obj.type])
  }
  return false
}

export interface PickerModel {
  id: string
  name: string
  value: string
  caps: { vision: boolean; audioInput: boolean; reasoning: boolean }
  /** True when the gateway confirmed this model is gone upstream. */
  deprecated?: boolean
}

export interface PickerGroup {
  id: string
  name: string
  color?: string
  models: PickerModel[]
}

export interface PickerExtras {
  modelAliases?: Record<string, string>
  customModels?: Array<{ providerAlias?: string; id: string; name?: string; type?: string }>
  disabledModels?: Record<string, string[]>
  /** Deprecations keyed "<provider>/<model>", from /api/models/deprecations. */
  deprecations?: Record<string, unknown>
}

export function resolveModelPickerGroups(
  connections: ProviderConnection[] = [],
  providerNodes: ProviderNode[] = [],
  extras: PickerExtras = {}
): PickerGroup[] {
  // Upstream parity divergence (ModelSelectModal.js groupedModels): upstream
  // holds ALL connections (no isActive filter) so inactive providers stay
  // selectable. NOX9GO asked for the opposite: a provider the operator
  // deactivated or removed must not keep offering its models in the picker.
  const connectedProviderIds = new Set<string>()
  const activeConnectionsByProvider = new Map<string, ProviderConnection>()
  const providersWithConnections = new Set<string>()
  for (const c of connections) {
    if (!c.provider) continue
    providersWithConnections.add(c.provider)
    const alias = PROVIDER_ID_TO_ALIAS[c.provider]
    if (alias) providersWithConnections.add(alias)
    if (c.isActive !== 0) {
      connectedProviderIds.add(c.provider)
      if (alias) connectedProviderIds.add(alias)
      if (!activeConnectionsByProvider.has(c.provider)) {
        activeConnectionsByProvider.set(c.provider, c)
      }
      if (alias && !activeConnectionsByProvider.has(alias)) {
        activeConnectionsByProvider.set(alias, c)
      }
    }
  }
  const hasAnyActiveConnection = connectedProviderIds.size > 0

  const groups: PickerGroup[] = []
  const seenGroupIds = new Set<string>()

  // 1. Catalog providers (active connections only when configured; noAuth fallback only when 0 active connections exist and provider was not explicitly disabled)
  for (const catItem of PROVIDER_CATALOG) {
    if (!isChatProvider(catItem) || catItem.hidden) {
      continue
    }
    const isConnected =
      connectedProviderIds.has(catItem.id) || Boolean(catItem.alias && connectedProviderIds.has(catItem.alias))
    const hasRows =
      providersWithConnections.has(catItem.id) || Boolean(catItem.alias && providersWithConnections.has(catItem.alias))
    const isExplicitlyDisabled = hasRows && !isConnected
    if (isExplicitlyDisabled) {
      continue
    }
    // Only the explicit noAuth flag (upstream registry semantics): category
    // "free" providers like kiro/gemini-cli still require a connection.
    // When at least one active connection exists, only connected providers
    // appear in the picker (matching /v1/models ModeListAll behavior).
    const isNoAuth = catItem.noAuth === true
    if (!isConnected && (hasAnyActiveConnection || !isNoAuth)) {
      continue
    }
    const alias = catItem.alias || PROVIDER_ID_TO_ALIAS[catItem.id] || catItem.id
    const activeConn = activeConnectionsByProvider.get(catItem.id) || activeConnectionsByProvider.get(alias)
    const psd = (activeConn?.providerSpecificData || {}) as { enabledModels?: unknown }
    const enabledModelsList = Array.isArray(psd.enabledModels)
      ? psd.enabledModels
          .filter((x): x is string => typeof x === 'string' && x.trim().length > 0)
          .map((x) => {
            const s = x.trim()
            if (s.startsWith(`${alias}/`)) return s.slice(alias.length + 1)
            if (s.startsWith(`${catItem.id}/`)) return s.slice(catItem.id.length + 1)
            return s
          })
      : null
    const enabledModelsSet = enabledModelsList && enabledModelsList.length > 0 ? new Set(enabledModelsList) : null

    const rawModels = getModelsByProviderId(catItem.id)
    const hardcodedIds = new Set((rawModels || []).map((m) => m.id))
    const hasHardcoded = (rawModels || []).length > 0

    // Custom models registered via /api/models/custom for this provider
    // (matched by either storage alias or canonical provider id).
    const customs = (extras.customModels || []).filter(
      (m) =>
        (m.providerAlias === alias || m.providerAlias === catItem.id) &&
        m.id &&
        !hardcodedIds.has(m.id)
    )
    const validCustoms = customs.filter((m) => {
      if (!m.type || m.type === 'llm' || m.type === 'chat') return true
      return !MEDIA_KINDS[m.type]
    })
    const visibleCustoms = validCustoms

    // Upstream parity: aliases stored as {aliasName: "alias/modelId"}.
    const aliasEntries = Object.entries(extras.modelAliases || {}).filter(
      ([aliasName, fullModel]) =>
        typeof fullModel === 'string' &&
        (fullModel.startsWith(`${alias}/`) || fullModel.startsWith(`${catItem.id}/`)) &&
        (hasHardcoded
          ? aliasName === fullModel.replace(`${alias}/`, '').replace(`${catItem.id}/`, '')
          : true) &&
        !hardcodedIds.has(fullModel.replace(`${alias}/`, '').replace(`${catItem.id}/`, ''))
    )

    if ((!rawModels || rawModels.length === 0) && customs.length === 0 && aliasEntries.length === 0) {
      continue
    }

    const seenModelIds = new Set<string>()
    const models: PickerModel[] = []
    // A model the gateway saw refused with a 410 is marked, not hidden: the
    // operator may still want it in a combo while they replace it, and a
    // picker that silently dropped it would leave them editing JSON to find
    // out why it disappeared.
    const isDeprecated = (modelId: string) =>
      Boolean(extras.deprecations?.[`${catItem.id.toLowerCase()}/${modelId.toLowerCase()}`])


    for (const m of rawModels || []) {
      if (!m.id || seenModelIds.has(m.id) || !isChatModel(m)) continue
      if (enabledModelsSet && !enabledModelsSet.has(m.id)) continue
      seenModelIds.add(m.id)
      models.push({
        id: m.id,
        name: m.name || m.id,
        value: `${alias}/${m.id}`,
        caps: getModelCaps(m.id, m),
        deprecated: isDeprecated(m.id),
      })
    }

    for (const m of visibleCustoms) {
      if (seenModelIds.has(m.id)) continue
      seenModelIds.add(m.id)
      models.push({
        id: m.id,
        name: m.name || m.id,
        value: `${alias}/${m.id}`,
        caps: getModelCaps(m.id, m),
        deprecated: isDeprecated(m.id),
      })
    }

    for (const [aliasName, fullModel] of aliasEntries) {
      const modelId = (fullModel as string).replace(`${alias}/`, '').replace(`${catItem.id}/`, '')
      if (!modelId || seenModelIds.has(modelId)) continue
      seenModelIds.add(modelId)
      models.push({
        id: modelId,
        name: aliasName,
        value: `${alias}/${modelId}`,
        caps: getModelCaps(modelId),
        deprecated: isDeprecated(modelId),
      })
    }

    // Upstream parity: filter out disabled models per provider
    // (disabled keyed by storage alias OR providerId, with or without prefix).
    const rawDisabled = [
      ...((extras.disabledModels || {})[alias] || []),
      ...((extras.disabledModels || {})[catItem.id] || []),
    ]
    const disabled = new Set<string>()
    for (const d of rawDisabled) {
      if (typeof d !== 'string') continue
      const trimmed = d.trim()
      if (!trimmed) continue
      disabled.add(trimmed)
      if (trimmed.startsWith(`${alias}/`)) disabled.add(trimmed.slice(alias.length + 1))
      if (trimmed.startsWith(`${catItem.id}/`)) disabled.add(trimmed.slice(catItem.id.length + 1))
    }
    const visible = disabled.size > 0 ? models.filter((m) => !disabled.has(m.id) && !disabled.has(m.value)) : models

    if (visible.length > 0) {
      seenGroupIds.add(catItem.id)
      groups.push({
        id: catItem.id,
        name: catItem.name || catItem.id,
        color: catItem.color,
        models: visible,
      })
    }
  }

  // 2. Custom (openai/anthropic-compatible) nodes — LLM-only. Upstream parity
  // (ModelSelectModal.js isCustomProvider): aliases filtered by raw providerId,
  // values use the display prefix, plus custom models registered for the node id;
  // placeholder when connected but empty.
  for (const node of providerNodes) {
    if (!node.id || seenGroupIds.has(node.id)) continue
    // Divergence from upstream parity (ModelSelectModal.js 6a1573eb): upstream
    // kept a compatible node selectable without any connection when it had
    // custom models. NOX9GO behavior (operator request): only nodes with an
    // ACTIVE connection row are listed, so removed/deactivated nodes disappear
    // from the picker along with their orphaned custom models.
    const conn = connections.find((c) => c.provider === node.id && c.isActive !== 0)
    if (!conn) continue
    const psd = (conn.providerSpecificData || {}) as Record<string, unknown>
    const nodePrefix = (psd.prefix as string) || node.prefix || node.id
    const displayName = node.name || conn?.name || node.id
    const isCompatibleNode = node.type === 'openai-compatible' || node.type === 'anthropic-compatible'

    const registeredCustom = (extras.customModels || [])
      .filter((m) => (m.providerAlias === node.id || m.providerAlias === nodePrefix) && m.id)
      .map((m) => ({
        id: m.id,
        name: m.name || m.id,
        value: `${nodePrefix}/${m.id}`,
        caps: getModelCaps(m.id, m),
        deprecated: Boolean(
          extras.deprecations?.[`${node.id.toLowerCase()}/${m.id.toLowerCase()}`]
        ),
      }))

    // providerNodes lists every node; ones with a connection row are usable as
    // picker values (matches upstream activeProviders, which holds all
    // connections regardless of isActive). Upstream parity (ModelSelectModal.js
    // 6a1573eb): a compatible node that already has user-added custom models is
    // selectable WITHOUT a connection — local / self-hosted endpoints don't
    // always need a stored key, so gating on connections hid models the user had
    // added. Such nodes are LLM-only, which is why they are unioned here, in the
    // LLM-only half of the picker, instead of behind a kind filter. With custom
    // models present there is always something to show, so no placeholder group.
    if (!conn && (!isCompatibleNode || registeredCustom.length === 0)) continue

    const nodeModels = Object.entries(extras.modelAliases || {})
      .filter(([, fullModel]) => typeof fullModel === 'string' && fullModel.startsWith(`${node.id}/`))
      .map(([aliasName, fullModel]) => {
        const modelId = (fullModel as string).replace(`${node.id}/`, '')
        return {
          id: modelId,
          name: aliasName,
          value: `${nodePrefix}/${modelId}`,
          caps: getModelCaps(modelId),
          deprecated: Boolean(
            extras.deprecations?.[`${node.id.toLowerCase()}/${modelId.toLowerCase()}`]
          ),
        }
      })
    const seen = new Set(nodeModels.map((m) => m.value))
    const rawNodeDisabled = [
      ...((extras.disabledModels || {})[node.id] || []),
      ...((extras.disabledModels || {})[nodePrefix] || []),
    ]
    const nodeDisabled = new Set<string>()
    for (const d of rawNodeDisabled) {
      if (typeof d !== 'string') continue
      const trimmed = d.trim()
      if (!trimmed) continue
      nodeDisabled.add(trimmed)
      if (trimmed.startsWith(`${node.id}/`)) nodeDisabled.add(trimmed.slice(node.id.length + 1))
      if (trimmed.startsWith(`${nodePrefix}/`)) nodeDisabled.add(trimmed.slice(nodePrefix.length + 1))
    }
    const mergedModels = [...nodeModels, ...registeredCustom.filter((m) => !seen.has(m.value))].filter(
      (m) => !nodeDisabled.has(m.id) && !nodeDisabled.has(m.value)
    )

    const modelsToShow =
      mergedModels.length > 0
        ? mergedModels
        : [
            {
              id: `__placeholder__${node.id}`,
              name: `${nodePrefix}/model-id`,
              value: `${nodePrefix}/model-id`,
              caps: { vision: false, audioInput: false, reasoning: false },
            },
          ]

    seenGroupIds.add(node.id)
    groups.push({
      id: node.id,
      name: displayName,
      models: modelsToShow,
    })
  }

  return groups
}

export function resolveFilteredCombos(
  combos: Combo[] = [],
  currentComboName: string | undefined,
  searchQuery: string,
  target: string
): Combo[] {
  if (target === 'vision' || target === 'audio') {
    return []
  }

  const parsed = parseSearchQuery(searchQuery)
  if (parsed.isEmpty) {
    return combos.filter((c) => {
      // Upstream parity (page.js fetchData): webSearch/webFetch combos live
      // under media-providers/web. search-combo is excluded even when its kind
      // field is missing (older rows predate the kind column).
      if (c.kind && c.kind !== 'llm') return false
      if (c.name === 'search-combo' || c.name.startsWith('search-combo-')) return false
      if (currentComboName && c.name === currentComboName) return false
      return true
    })
  }
  return combos.filter((c) => {
    // Upstream parity (page.js fetchData): webSearch/webFetch combos live
    // under media-providers/web. search-combo is excluded even when its kind
    // field is missing (older rows predate the kind column).
    if (c.kind && c.kind !== 'llm') return false
    if (c.name === 'search-combo' || c.name.startsWith('search-combo-')) return false
    if (currentComboName && c.name === currentComboName) return false
    return matchesSearch(c.name, parsed)
  })
}

// resolveFilteredGroups applies the search text and the modality target. The
// already-added models are deliberately not an input: nothing here reads them, but
// passing them from the caller made the signature part of the reactive graph, so
// clicking one pill re-filtered and re-sorted every group and reconciled the
// whole pill list again (#61).
export function resolveFilteredGroups(
  groups: PickerGroup[] = [],
  searchQuery: string,
  target: string
): PickerGroup[] {
  const parsed = parseSearchQuery(searchQuery)

  return groups
    .map((group) => {
      let models = group.models

      // Filter by input-modality capability (vision / audio) matching upstream ModelSelectModal capFilter
      if (target === 'vision') {
        models = models.filter((m) => m.caps.vision)
      } else if (target === 'audio') {
        models = models.filter((m) => m.caps.audioInput)
      }

      if (models.length === 0) return null

      if (!parsed.isEmpty) {
        const groupMatches = matchesSearch(group.name, parsed)
        models = models.filter((m) => {
          if (matchesSearch(m, parsed)) return true
          if (groupMatches) {
            if (parsed.negativeTerms.length > 0) {
              return matchesSearch(m, { ...parsed, positiveTerms: [] })
            }
            return true
          }
          return false
        })
      }

      if (models.length === 0) return null

      // Sort models alphabetically; stable display avoids sudden layout shift and freezing on click
      const sorted = [...models].sort((a, b) => a.name.localeCompare(b.name))

      return {
        ...group,
        models: sorted,
      }
    })
    .filter((g): g is PickerGroup => g !== null)
}
