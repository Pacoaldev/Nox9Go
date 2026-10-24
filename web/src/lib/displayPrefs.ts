// Usage detail panels (Activity Heatmap, Node Health) start collapsed; the
// Settings > Display switch flips that default. Each panel can still be opened
// on its own for the current view, so this decides the starting state only.
// Mirrors lib/compactNumber.ts: a localStorage preference, no server round trip.
const DETAILS_EXPANDED_KEY = 'detailsExpandedByDefault'

export function isDetailsExpandedByDefault(): boolean {
  if (typeof localStorage === 'undefined') return false
  return localStorage.getItem(DETAILS_EXPANDED_KEY) === '1'
}

export function setDetailsExpandedByDefault(on: boolean): void {
  if (typeof localStorage === 'undefined') return
  localStorage.setItem(DETAILS_EXPANDED_KEY, on ? '1' : '0')
}

// The heavy Settings blocks — the currency rate grid, the per-panel Usage list
// with its arrangement controls, and the sidebar feature-visibility row — stay
// folded behind the Display switch until an operator asks for them. Nothing is
// turned off by folding them: the currency still formats every cost and the
// arrangement still drives the Usage page, so this is a disclosure preference
// rather than a feature flag, and it sits with the other display defaults.
const ADVANCED_SETTINGS_KEY = 'advancedSettingsVisible'

export function isAdvancedSettingsVisible(): boolean {
  if (typeof localStorage === 'undefined') return false
  return localStorage.getItem(ADVANCED_SETTINGS_KEY) === '1'
}

export function setAdvancedSettingsVisible(on: boolean): void {
  if (typeof localStorage === 'undefined') return
  localStorage.setItem(ADVANCED_SETTINGS_KEY, on ? '1' : '0')
}
