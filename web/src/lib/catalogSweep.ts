// Pure helpers for the provider catalogue sweep. The sweep only ever reports
// what the upstream catalogue lists; a model missing from that catalogue is
// NOT evidence of deprecation, so nothing here marks a model deprecated.
import type { CatalogSweepEntry } from '../api/client'

export interface SweepSummary {
  state: 'never' | 'skipped' | 'error' | 'ok'
  unlistedCount: number
  listed: number
}

/**
 * Reduces one provider's sweep entry to the state the dashboard renders.
 *
 * An entry without a check timestamp was never swept, and a failed read of the
 * catalogue is not the same thing as an empty catalogue.
 */
export function summarizeSweep(entry: CatalogSweepEntry | undefined): SweepSummary {
  if (!entry || !entry.checkedAt) {
    return { state: 'never', unlistedCount: 0, listed: 0 }
  }
  const listed = entry.listed ?? 0
  if (entry.error) {
    return { state: 'error', unlistedCount: 0, listed }
  }
  if (entry.skipped) {
    return { state: 'skipped', unlistedCount: 0, listed }
  }
  return { state: 'ok', unlistedCount: entry.unlisted?.length ?? 0, listed }
}

/**
 * Renders an RFC3339 timestamp as a local date-time. Empty or unparseable
 * input renders as nothing, because the caller shows the line only when
 * there is something to show.
 */
export function formatCheckedAt(checkedAt: string | undefined): string {
  if (!checkedAt) return ''
  const parsed = new Date(checkedAt)
  if (Number.isNaN(parsed.getTime())) return ''
  return parsed.toLocaleString()
}
