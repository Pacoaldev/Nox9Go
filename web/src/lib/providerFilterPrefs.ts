// The Providers grid status filter (All / Connected / Error / Disabled / Not
// Connected) is a view preference, but an operator sets it once and expects it to
// survive a reload — reopening the app should not quietly widen the grid back to
// every provider. localStorage only, no server round trip, same shape as
// lib/displayPrefs.ts and lib/compactNumber.ts.
const PROVIDER_STATUS_FILTER_KEY = 'providerStatusFilter'

export type ProviderStatusFilter = 'all' | 'connected' | 'error' | 'disabled' | 'not_connected'

const ALLOWED: Record<ProviderStatusFilter, true> = {
  all: true,
  connected: true,
  error: true,
  disabled: true,
  not_connected: true,
}

function isProviderStatusFilter(value: string | null): value is ProviderStatusFilter {
  return value !== null && Object.prototype.hasOwnProperty.call(ALLOWED, value)
}

export function loadProviderStatusFilter(): ProviderStatusFilter {
  if (typeof localStorage === 'undefined') return 'all'
  const stored = localStorage.getItem(PROVIDER_STATUS_FILTER_KEY)
  return isProviderStatusFilter(stored) ? stored : 'all'
}

export function saveProviderStatusFilter(value: ProviderStatusFilter): void {
  if (typeof localStorage === 'undefined') return
  if (!isProviderStatusFilter(value)) return
  localStorage.setItem(PROVIDER_STATUS_FILTER_KEY, value)
}