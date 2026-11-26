/**
 * Cooldown / quota badge rules for the connections list of ProviderDetailView.
 *
 * The connection badge answers ONE question: is the gateway currently holding
 * this connection out of rotation? Two sources qualify:
 *   1. a lock the gateway itself wrote after an upstream error
 *      (`modelLock_<model>` or `rateLimitedUntil`), and
 *   2. a 429 / quota error already recorded on the connection.
 *
 * A model the UPSTREAM declares exhausted is not a connection lockout. An
 * Antigravity account that also serves the Antigravity IDE reports its whole
 * entitlement with zero remaining — including models the operator never routed
 * through the gateway — and that data is about the account at Google, not about
 * what the gateway sent. Badging the connection for it told the operator the
 * connection was dead while every other model kept answering.
 *
 * That upstream data still reaches the UI, as neutral text, through
 * `buildUpstreamQuotaNote`.
 */

export interface CooldownInfo {
  label: string
  title: string
  isExhausted: boolean
  isLock?: boolean
}

/** Fields the badge reads off a connection, merged from `conn`, `conn.data` and `providerSpecificData`. */
export interface ConnectionLockSource {
  data?: Record<string, unknown>
  providerSpecificData?: Record<string, unknown>
  [key: string]: unknown
}

export interface QuotaLike {
  total?: number | string
  remaining?: number | string
  remainingPercentage?: number | string
  resetAt?: string
  displayName?: string
}

export interface UpstreamQuotaExhaustion {
  model: string
  displayName: string
  resetAt: number
}

/** Time left on a gateway lock, as the badge label. */
export function formatLockTimeLeft(ms: number): string {
  const diff = Math.max(0, Math.floor(ms / 1000))
  if (diff < 60) return `${diff}s`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ${diff % 60}s`
  return `${Math.floor(diff / 3600)}h ${Math.floor((diff % 3600) / 60)}m`
}

/** Time until an upstream quota window resets. */
export function formatResetIn(ms: number): string {
  const diff = Math.max(0, ms)
  const hours = Math.floor(diff / (1000 * 60 * 60))
  const mins = Math.floor((diff % (1000 * 60 * 60)) / (1000 * 60))
  const secs = Math.floor((diff % (1000 * 60)) / 1000)
  if (hours >= 24) return `${Math.floor(hours / 24)}d ${hours % 24}h`
  if (hours > 0) return `${hours}h ${mins}m`
  return `${mins}m ${secs}s`
}

/**
 * Models the upstream reports with nothing left in this window.
 *
 * A pool with `total <= 0` is NOT exhausted: it means the account has no
 * allocation at all (Qoder credits report total 0, and its `expiresAt` is a
 * 9999-12-31 sentinel), so counting it produced "Quota Exhausted (0%): Resets in
 * 2912172d 1h" on a perfectly healthy connection. Only a pool that was actually
 * granted something can run out of it.
 */
export function listUpstreamExhaustedQuotas(
  quotas: Record<string, QuotaLike> | undefined,
  currentTime: number
): UpstreamQuotaExhaustion[] {
  if (!quotas) return []
  const exhausted: UpstreamQuotaExhaustion[] = []
  for (const [model, q] of Object.entries(quotas)) {
    const total = Number(q.total)
    if (!Number.isFinite(total) || total <= 0) continue
    const spent =
      (q.remainingPercentage !== undefined && Number(q.remainingPercentage) <= 0) ||
      (q.remaining !== undefined && Number(q.remaining) <= 0)
    if (!spent) continue
    const resetAt = q.resetAt ? new Date(q.resetAt).getTime() : 0
    exhausted.push({
      model,
      displayName: q.displayName || model,
      resetAt: Number.isFinite(resetAt) && resetAt > currentTime ? resetAt : 0,
    })
  }
  return exhausted.sort(
    (a, b) => (a.resetAt || Number.POSITIVE_INFINITY) - (b.resetAt || Number.POSITIVE_INFINITY) || a.displayName.localeCompare(b.displayName)
  )
}

/** Neutral, informational note for the connection detail. Never a badge, never a lockout. */
export function buildUpstreamQuotaNote(
  quotas: Record<string, QuotaLike> | undefined,
  currentTime: number
): { text: string; title: string } | null {
  const exhausted = listUpstreamExhaustedQuotas(quotas, currentTime)
  if (exhausted.length === 0) return null
  const names = exhausted.map((e) => e.displayName).join(', ')
  const next = exhausted.find((e) => e.resetAt > 0)
  const text = next
    ? `Upstream quota exhausted: ${names} — resets in ${formatResetIn(next.resetAt - currentTime)}`
    : `Upstream quota exhausted: ${names}`
  const detail = exhausted
    .map((e) => (e.resetAt > 0 ? `${e.displayName} until ${new Date(e.resetAt).toLocaleString()}` : e.displayName))
    .join(', ')
  return {
    text,
    title: `Declared exhausted by the upstream for this account (not a gateway lockout): ${detail}`,
  }
}

/**
 * Badge for a connection row, or null when nothing is holding it out of rotation.
 *
 * `lastErrorText` is the connection's already-normalized `lastError`.
 */
export function getGatewayCooldownInfo(
  conn: ConnectionLockSource,
  lastErrorText: string,
  currentTime: number
): CooldownInfo | null {
  // 1. modelLock_* and rateLimitedUntil across conn, conn.data and
  //    providerSpecificData (matching upstream's ⏱ {timeLeft}).
  const dataObj = conn.providerSpecificData
  const rawData = conn.data
  const allProps: Record<string, unknown> = {
    ...(typeof rawData === 'object' && rawData ? rawData : {}),
    ...(typeof dataObj === 'object' && dataObj ? dataObj : {}),
    ...conn,
  }
  const locks = Object.entries(allProps).filter(
    ([k, v]) =>
      (k.startsWith('modelLock_') || k === 'rateLimitedUntil') &&
      typeof v === 'string' &&
      new Date(v).getTime() > currentTime
  )
  if (locks.length > 0) {
    let maxLock = 0
    for (const [, v] of locks) {
      const t = new Date(v as string).getTime()
      if (t > maxLock) maxLock = t
    }
    return {
      label: `⏱ ${formatLockTimeLeft(maxLock - currentTime)}`,
      title: `Model rate limit lock active until ${new Date(maxLock).toLocaleTimeString()}`,
      isExhausted: false,
      isLock: true,
    }
  }

  // 2. A 429 / quota error already recorded on this connection.
  const errCode = conn.errorCode
  const lastErr = lastErrorText || ''
  if (
    errCode === 429 ||
    lastErr.includes('429') ||
    lastErr.toLowerCase().includes('quota') ||
    lastErr.toLowerCase().includes('exhausted')
  ) {
    const match = lastErr.match(/Resets in ([^.]+)/i)
    if (match) {
      return {
        label: `Quota Exhausted (429): Resets in ${match[1].trim()}`,
        title: lastErr,
        isExhausted: true,
      }
    }
    return {
      label: 'Quota Exhausted (429)',
      title: lastErr || 'HTTP 429 Rate Limit / Quota Exhausted',
      isExhausted: true,
    }
  }

  return null
}
