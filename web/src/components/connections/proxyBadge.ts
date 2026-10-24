import type { ProviderConnection, ProxyPool } from '../../api/client'

export interface ProxyBadgeInfo {
  /** True when the connection is bound to a pool or a legacy per-connection proxy. */
  hasAnyProxy: boolean
  /** 'success' when bound to an active pool, 'error' for inactive/missing pools and legacy proxies. */
  variant: 'success' | 'error'
  /** "Pool: <name>" / "Pool: <id> (inactive/missing)" / "Legacy: <url>". */
  displayText: string
  /** Endpoint with credentials and path stripped, e.g. "http://proxy.example.com:8080". */
  maskedProxyUrl: string
  noProxyText: string
}

/**
 * Port of upstream ConnectionRow's proxy badge:
 *   const boundProxyPoolId = connection.providerSpecificData?.proxyPoolId
 *   boundProxyPool = pools.find(p => p.id === boundProxyPoolId)
 *   proxyBadgeVariant = success when the pool is active, error when bound to a
 *   missing/inactive pool or a legacy proxy, default otherwise.
 * The endpoint is masked via `new URL()` (protocol + hostname + port only) so
 * proxy credentials never reach the dashboard.
 */
export function proxyBadgeInfo(
  conn: Pick<ProviderConnection, 'providerSpecificData'>,
  pools: ProxyPool[] = []
): ProxyBadgeInfo {
  const psd = (conn.providerSpecificData as Record<string, unknown> | undefined) || {}
  const poolId = typeof psd.proxyPoolId === 'string' && psd.proxyPoolId ? psd.proxyPoolId : null
  const pool = poolId ? pools.find((p) => p.id === poolId) || null : null

  const legacyUrl = typeof psd.connectionProxyUrl === 'string' ? psd.connectionProxyUrl : ''
  const hasLegacyProxy = psd.connectionProxyEnabled === true && !!legacyUrl
  const hasAnyProxy = !!poolId || hasLegacyProxy

  const rawProxyUrl = pool?.proxyUrl || legacyUrl
  let maskedProxyUrl = ''
  if (rawProxyUrl) {
    try {
      const parsed = new URL(rawProxyUrl)
      maskedProxyUrl = `${parsed.protocol}//${parsed.hostname}${parsed.port ? `:${parsed.port}` : ''}`
    } catch {
      maskedProxyUrl = rawProxyUrl
    }
  }

  return {
    hasAnyProxy,
    variant: pool?.isActive === true ? 'success' : 'error',
    displayText: pool
      ? `Pool: ${pool.name}`
      : poolId
        ? `Pool: ${poolId} (inactive/missing)`
        : `Legacy: ${legacyUrl}`,
    maskedProxyUrl,
    noProxyText:
      pool?.noProxy || (typeof psd.connectionNoProxy === 'string' ? psd.connectionNoProxy : '')
  }
}
