// System status store: one poll feeds every surface showing health, totals or
// alerts, keeping the poll budget on the 1 vCPU target (§4.G).
import { api } from '../api/client'

export type ServiceState = 'ok' | 'error' | 'idle' | 'unknown'

export interface DashboardStatusSnapshot {
  gateway: ServiceState
  database: ServiceState
  tunnel: ServiceState
  latencyMs: number | null
  requestsToday: number
  promptTokensToday: number
  completionTokensToday: number
  totalTokensToday: number
  alertConnections: number
  lockedConnections: number
}

const POLL_INTERVAL_MS = 15000

let current = $state<DashboardStatusSnapshot>({
  gateway: 'unknown',
  database: 'unknown',
  tunnel: 'unknown',
  latencyMs: null,
  requestsToday: 0,
  promptTokensToday: 0,
  completionTokensToday: 0,
  totalTokensToday: 0,
  alertConnections: 0,
  lockedConnections: 0,
})

let timer: ReturnType<typeof setInterval> | null = null
let inFlight = false

export function dashboardStatus(): DashboardStatusSnapshot {
  return current
}

export async function refreshDashboardStatus(): Promise<void> {
  if (inFlight) return
  if (typeof document !== 'undefined' && document.hidden) return
  inFlight = true
  const started = performance.now()
  try {
    const res = await api.getDashboardStatus()
    const elapsed = Math.round(performance.now() - started)

    const gwOk = res.gateway?.ok === true
    const dbOk = res.database?.ok === true
    const tEnabled = res.tunnel?.enabled === true
    const tRunning = res.tunnel?.running === true
    const tunnelState: ServiceState = tEnabled ? (tRunning ? 'ok' : 'error') : 'idle'

    const prompt = Number(res.today?.promptTokens ?? 0)
    const completion = Number(res.today?.completionTokens ?? 0)

    current = {
      gateway: gwOk ? 'ok' : 'error',
      database: dbOk ? 'ok' : 'error',
      tunnel: tunnelState,
      latencyMs: gwOk ? elapsed : null,
      requestsToday: Number(res.today?.requests ?? 0),
      promptTokensToday: prompt,
      completionTokensToday: completion,
      totalTokensToday: prompt + completion,
      alertConnections: Number(res.alerts?.connections ?? 0),
      lockedConnections: Number(res.alerts?.locked ?? 0),
    }
  } catch {
    current = {
      ...current,
      gateway: 'error',
      database: 'error',
      latencyMs: null,
    }
  } finally {
    inFlight = false
  }
}

export function startDashboardStatusPolling(): void {
  if (timer) return
  void refreshDashboardStatus()
  timer = setInterval(() => {
    void refreshDashboardStatus()
  }, POLL_INTERVAL_MS)
}

export function stopDashboardStatusPolling(): void {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
}
