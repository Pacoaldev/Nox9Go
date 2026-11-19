// Providers currently under an active alert (lock or test error), as reported
// by the bell's own detail endpoint. ProviderCard reads this to pulse the
// affected provider's card border, so the operator sees which card the
// notification panel is about without opening it.
//
// The set is synced from AlertsBadge: the status poll already knows *that*
// there are alerts (status.alertConnections / lockedConnections), and the
// detail fetch only runs while alerts are active, so no extra request is
// spent when everything is healthy (AGENTS.md §4.G).
import type { AlertDetail } from '../api/client'

const state = $state<{ providerIds: Set<string> }>({ providerIds: new Set() })

export function providerAlertPulse(): Set<string> {
  return state.providerIds
}

/** Replaces the highlighted set with the providers named by active alerts. */
export function syncProviderAlertPulse(alerts: AlertDetail[]): void {
  const ids = new Set<string>()
  for (const alert of alerts) {
    if (alert.provider) ids.add(alert.provider)
  }
  state.providerIds = ids
}

export function clearProviderAlertPulse(): void {
  state.providerIds = new Set()
}
