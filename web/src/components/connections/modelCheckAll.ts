export type ModelTestStatus = 'ok' | 'error' | 'blocked' | 'testing'

export interface ModelTestVerdict {
  status: 'ok' | 'error' | 'blocked'
  error: string | null
}

export function parseModelTestVerdict(res: {
  ok: boolean
  status?: number
  error?: string
  blocked?: boolean
  resetAt?: string
}): ModelTestVerdict {
  if (res.ok) {
    return { status: 'ok', error: null }
  }
  const err = res.error || 'Model test failed'

  // The backend owns this verdict. It knows whether the account was parked
  // (temporary, resolves on its own) or whether the model was actually asked
  // and refused — a distinction no amount of string matching in the SPA can
  // make reliably, since the gateway's own wording differs from a provider's
  // and a model-scoped quota refusal never clears by waiting.
  if (res.blocked) {
    const reset = formatResetAt(res.resetAt)
    return { status: 'blocked', error: reset ? `Blocked (cooldown until ${reset})` : err }
  }

  return { status: 'error', error: err }
}

// The gateway speaks RFC3339; operators read local time. Every other reset in
// this view (quota rows at :860) is rendered the same way, and a raw
// "2026-10-09T03:00:00Z" on a dashboard row is not something a human can act on.
function formatResetAt(resetAt?: string): string {
  if (!resetAt) return ''
  const parsed = new Date(resetAt)
  return Number.isNaN(parsed.getTime()) ? resetAt : parsed.toLocaleString()
}

export interface CheckAllSummary {
  passed: number
  failed: number
  blocked: number
  total: number
  severity: 'ok' | 'error' | 'blocked' | null
  message: string | null
}

export function formatCheckAllSummary(statuses: Record<string, ModelTestStatus>): CheckAllSummary {
  const vals = Object.values(statuses).filter((s) => s !== 'testing')
  const passed = vals.filter((s) => s === 'ok').length
  const failed = vals.filter((s) => s === 'error').length
  const blocked = vals.filter((s) => s === 'blocked').length
  const total = vals.length

  if (total === 0 || (failed === 0 && blocked === 0)) {
    return { passed, failed, blocked, total, severity: total > 0 ? 'ok' : null, message: null }
  }

  let severity: 'error' | 'blocked' = 'error'
  let msg = ''

  if (failed === 0 && blocked > 0) {
    severity = 'blocked'
    msg = `${passed} passed · ${blocked} blocked — see per-row status.`
  } else if (failed > 0 && blocked === 0) {
    severity = 'error'
    msg = `${passed} passed · ${failed} failed — see per-row status.`
  } else {
    severity = 'error'
    msg = `${passed} passed · ${failed} failed · ${blocked} blocked — see per-row status.`
  }

  return { passed, failed, blocked, total, severity, message: msg }
}

export function getBlockedModelIds(statuses: Record<string, ModelTestStatus>): string[] {
  return Object.entries(statuses)
    .filter(([, s]) => s === 'blocked')
    .map(([id]) => id)
}

export function getFailedModelIds(statuses: Record<string, ModelTestStatus>): string[] {
  return Object.entries(statuses)
    .filter(([, s]) => s === 'error')
    .map(([id]) => id)
}

// Cap matches the gateway's probeRetryAfterCap: a longer upstream window is
// still shown on the row, but Check All must not stall for minutes between
// models while the operator watches a progress bar.
const PROBE_BACKOFF_CAP_MS = 10_000
const PROBE_BACKOFF_DEFAULT_MS = 1_000

/**
 * How long Check All should wait before the next probe after this verdict.
 * Honours the gateway's `retryAfter` first; falls back to "Retry after N"
 * prose in the error body (llm7-style); defaults to 1s on a bare 429.
 */
export function probeBackoffMs(res: {
  status?: number
  retryAfter?: number
  error?: string
}): number {
  if (res.status !== 429) return 0
  if (typeof res.retryAfter === 'number' && res.retryAfter > 0) {
    return Math.min(res.retryAfter * 1000, PROBE_BACKOFF_CAP_MS)
  }
  const match = /retry after\s+(\d+)/i.exec(res.error ?? '')
  if (match) {
    const secs = Number(match[1])
    if (Number.isFinite(secs) && secs > 0) {
      return Math.min(secs * 1000, PROBE_BACKOFF_CAP_MS)
    }
  }
  return PROBE_BACKOFF_DEFAULT_MS
}
