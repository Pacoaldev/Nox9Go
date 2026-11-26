/**
 * Badge rules for the connections list (web/src/lib/connection-cooldown.ts).
 *
 * The rule this file exists for: a model the UPSTREAM declares exhausted must
 * never badge the connection. An Antigravity account that also serves the
 * Antigravity IDE reports `Claude Opus 5.5 (High)` with zero remaining for the
 * account, with no gateway request behind it, and the badge used to read
 * `QUOTA OUT · Quota exhausted for Claude Opus 5.5 (High)` on a connection that
 * was routing every other model fine.
 *
 * The gateway's own locks and recorded 429s keep badging; that is the fallback
 * direction this file pins second.
 */
import { describe, expect, it } from 'bun:test'
import {
  buildUpstreamQuotaNote,
  formatLockTimeLeft,
  formatResetIn,
  getGatewayCooldownInfo,
  listUpstreamExhaustedQuotas,
  type QuotaLike,
} from './connection-cooldown'

const NOW = Date.parse('2026-11-26T11:06:00.000Z')

function hoursFromNow(h: number): string {
  return new Date(NOW + h * 60 * 60 * 1000).toISOString()
}

describe('getGatewayCooldownInfo', () => {
  it('does not badge a connection because the upstream declares a model exhausted', () => {
    const quotas: Record<string, QuotaLike> = {
      'claude-opus-5.5-high': {
        total: 100,
        remaining: 0,
        remainingPercentage: 0,
        resetAt: hoursFromNow(10),
        displayName: 'Claude Opus 5.5 (High)',
      },
    }
    // The exhausted model travels on the connection object, exactly as the
    // component's live-quota scan used to read it.
    const conn = { id: 'conn-1', __usageQuotas: quotas }

    expect(getGatewayCooldownInfo(conn, '', NOW)).toBeNull()
  })

  it('badges a model-scoped lock the gateway wrote', () => {
    const conn = { id: 'conn-1', data: { 'modelLock_gemini-3-pro': hoursFromNow(3) } }

    const info = getGatewayCooldownInfo(conn, '', NOW)

    expect(info?.isLock).toBe(true)
    expect(info?.isExhausted).toBe(false)
    expect(info?.label).toBe('⏱ 3h 0m')
  })

  it('badges an account rateLimitedUntil lock and reads it off providerSpecificData', () => {
    const conn = { providerSpecificData: { rateLimitedUntil: hoursFromNow(0.5) } }

    const info = getGatewayCooldownInfo(conn, '', NOW)

    expect(info?.isLock).toBe(true)
    expect(info?.label).toBe('⏱ 30m 0s')
  })

  it('ignores a lock whose window already closed', () => {
    const conn = { 'modelLock_gemini-3-pro': hoursFromNow(-1) }

    expect(getGatewayCooldownInfo(conn, '', NOW)).toBeNull()
  })

  it('badges a recorded 429 on the connection', () => {
    const info = getGatewayCooldownInfo({ errorCode: 429 }, '', NOW)

    expect(info?.isExhausted).toBe(true)
    expect(info?.label).toBe('Quota Exhausted (429)')
  })

  it('does not badge a connection with no lock and no recorded error', () => {
    expect(getGatewayCooldownInfo({ id: 'conn-1' }, '', NOW)).toBeNull()
  })
})

describe('listUpstreamExhaustedQuotas', () => {
  it('reports the account pool with nothing left, oldest reset first', () => {
    const quotas: Record<string, QuotaLike> = {
      a: { total: 100, remainingPercentage: 0, resetAt: hoursFromNow(5), displayName: 'A' },
      b: { total: 100, remaining: 0, resetAt: hoursFromNow(1), displayName: 'B' },
    }

    expect(listUpstreamExhaustedQuotas(quotas, NOW).map((e) => e.displayName)).toEqual(['B', 'A'])
  })

  it('does not treat a pool with no allocation at all as exhausted', () => {
    const quotas: Record<string, QuotaLike> = {
      qoder: { total: 0, remaining: 0, resetAt: '9999-12-31T00:00:00Z' },
    }

    expect(listUpstreamExhaustedQuotas(quotas, NOW)).toEqual([])
  })
})

describe('buildUpstreamQuotaNote', () => {
  it('names the model and the time to reset instead of badging the connection', () => {
    const quotas: Record<string, QuotaLike> = {
      'claude-opus-5.5-high': {
        total: 100,
        remaining: 0,
        resetAt: hoursFromNow(10),
        displayName: 'Claude Opus 5.5 (High)',
      },
    }

    const note = buildUpstreamQuotaNote(quotas, NOW)

    expect(note?.text).toBe('Upstream quota exhausted: Claude Opus 5.5 (High) — resets in 10h 0m')
    expect(note?.title.startsWith('Declared exhausted by the upstream for this account')).toBe(true)
  })

  it('returns nothing when the account has quota left', () => {
    expect(buildUpstreamQuotaNote({ m: { total: 10, remaining: 7 } }, NOW)).toBeNull()
  })
})

describe('time formatting', () => {
  it('formats lock windows and reset windows', () => {
    expect(formatLockTimeLeft(45_000)).toBe('45s')
    expect(formatLockTimeLeft(90_000)).toBe('1m 30s')
    expect(formatResetIn(48 * 3600_000)).toBe('2d 0h')
    expect(formatResetIn(90_000)).toBe('1m 30s')
  })
})
