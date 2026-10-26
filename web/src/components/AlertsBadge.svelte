<!--
  AlertsBadge — the bell in the top bar, and the panel that explains it.

  The badge used to answer "how many" and nothing else: a red number on a
  glyph, with a tooltip that read "Alertas activas: 2 en bloqueo/límite de
  cuota". The operator still had to go and find which provider, which key and
  which model were parked, which is the one thing the alert was about.

  The panel below the bell answers that instead. Each row names the provider,
  the API key, the model and the message the upstream sent, and it links to the
  provider page where the key lives.

  The panel is `position: fixed` and portalled to `<body>`, like every other
  dropdown here: the top bar's `backdrop-blur-xl` header establishes a
  containing block, and a panel placed inside it renders off-screen
  (see lib/ui/portal.ts and lib/ui/menuPosition.ts).
-->
<script lang="ts">
  import { api, type AlertDetail, type AlertsResponse } from '../api/client'
  import { dashboardStatus } from '../lib/systemStatus.svelte'
  import { PROVIDER_CATALOG_MAP } from '../lib/providers'
  import { portal } from '../lib/ui/portal'
  import { maxPanelWidth, placePanel, placementStyle } from '../lib/ui/menuPosition'

  interface Props {
    onClick?: () => void
  }

  let { onClick }: Props = $props()

  const status = $derived(dashboardStatus())
  const totalAlerts = $derived(status.alertConnections + status.lockedConnections)

  let open = $state(false)
  let loading = $state(false)
  let details = $state<AlertsResponse | null>(null)
  let trigger: HTMLButtonElement | null = $state(null)
  let panel: HTMLDivElement | null = $state(null)
  let panelStyle = $state('')

  // The detail list is fetched on open, not on the poll: the status snapshot
  // already tells the badge how many there are, and a 15s poll that also
  // carried every alert's message would spend the poll budget on data nobody
  // is looking at (AGENTS.md §4.G).
  async function loadDetails(): Promise<void> {
    loading = true
    details = await api.getDashboardAlerts()
    loading = false
  }

  function toggle(): void {
    open = !open
    if (open) void loadDetails()
  }

  function close(): void {
    open = false
  }

  /** The parent navigates to the provider page; the panel closes behind it. */
  function goToProviders(): void {
    close()
    onClick?.()
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape') {
      close()
      trigger?.focus()
      return
    }
    if (event.key === 'Tab') close()
  }

  function place(): void {
    if (!trigger || !panel) return
    panelStyle = placementStyle(
      placePanel({
        align: 'right',
        rect: trigger.getBoundingClientRect(),
        panelWidth: panel.offsetWidth,
      }),
    )
  }

  $effect(() => {
    if (!open) return

    function handleDocClick(e: MouseEvent): void {
      const target = e.target as HTMLElement | null
      if (!trigger?.contains(target) && !panel?.contains(target)) close()
    }
    function reposition(): void {
      place()
    }

    document.addEventListener('click', handleDocClick)
    document.addEventListener('keydown', onKeydown)
    window.addEventListener('resize', reposition)
    window.addEventListener('scroll', reposition, true)

    return () => {
      document.removeEventListener('click', handleDocClick)
      document.removeEventListener('keydown', onKeydown)
      window.removeEventListener('resize', reposition)
      window.removeEventListener('scroll', reposition, true)
    }
  })

  $effect(() => {
    if (open && panel) place()
  })

  function formatTitle(): string {
    if (totalAlerts === 0) return 'Todos los proveedores operativos'
    const parts: string[] = []
    if (status.alertConnections > 0) {
      parts.push(`${status.alertConnections} con error de prueba`)
    }
    if (status.lockedConnections > 0) {
      parts.push(`${status.lockedConnections} en bloqueo/límite de cuota`)
    }
    return `Alertas activas: ${parts.join(', ')}`
  }

  /** The name the operator gave the key, or a readable fallback. */
  function keyName(alert: AlertDetail): string {
    return alert.connectionName || alert.displayName || alert.connectionId
  }

  /** The provider's display name, from the dashboard's own catalog. */
  function providerName(alert: AlertDetail): string {
    return PROVIDER_CATALOG_MAP.get(alert.provider)?.name || alert.provider
  }

  function alertLabel(alert: AlertDetail): string {
    return alert.alertType === 'lock' ? 'Bloqueado' : 'Error'
  }

  function alertIcon(alert: AlertDetail): string {
    return alert.alertType === 'lock' ? 'lock' : 'error'
  }

  /** How long the lock lasts, counted from now. */
  function remainingLabel(alert: AlertDetail): string {
    const stamp = alert.rateLimitedAt || alert.timestamp
    if (!stamp) return ''
    const until = new Date(stamp)
    if (Number.isNaN(until.getTime())) return ''
    const ms = until.getTime() - Date.now()
    if (ms <= 0) return ''
    const minutes = Math.ceil(ms / 60000)
    if (minutes < 60) return `se libera en ${minutes} min`
    const hours = Math.floor(minutes / 60)
    return `se libera en ${hours} h ${minutes % 60} min`
  }
</script>

<button
  bind:this={trigger}
  type="button"
  onclick={toggle}
  onkeydown={onKeydown}
  aria-haspopup="dialog"
  aria-expanded={open}
  class="relative p-1.5 rounded-lg text-text-muted hover:text-text-main hover:bg-surface-2 transition-colors cursor-pointer select-none"
  aria-label={totalAlerts > 0 ? `${totalAlerts} alertas de proveedores` : 'Sin alertas de proveedores'}
  title={formatTitle()}
>
  <span
    class="material-symbols-outlined text-[20px] {totalAlerts > 0 ? 'text-amber-500 animate-pulse' : ''}"
  >
    {totalAlerts > 0 ? 'notifications_active' : 'notifications'}
  </span>

  {#if totalAlerts > 0}
    <span
      class="absolute top-0.5 right-0.5 min-w-[15px] h-[15px] px-1 rounded-full bg-red-500 text-white text-[9px] font-bold flex items-center justify-center leading-none shadow-[0_0_6px_rgba(239,68,68,0.5)]"
    >
      {totalAlerts > 99 ? '99+' : totalAlerts}
    </span>
  {/if}
</button>

{#if open}
  <div
    use:portal
    bind:this={panel}
    role="dialog"
    aria-label="Detalle de alertas de proveedores"
    tabindex="-1"
    style="{panelStyle};max-width:{maxPanelWidth()}"
    onkeydown={onKeydown}
    class="fixed z-50 w-[22rem] max-w-[calc(100vw-1rem)] rounded-xl border border-border bg-surface shadow-[var(--shadow-elev)] overflow-hidden"
  >
    <div class="flex items-center justify-between gap-2 px-3.5 py-2.5 border-b border-border-subtle">
      <span class="text-xs font-semibold text-text-main uppercase tracking-wide">
        Alertas de proveedores
      </span>
      {#if details && details.counts.total > 0}
        <span class="text-[10px] font-mono text-text-muted">
          {details.counts.locked} bloqueadas · {details.counts.errored} con error
        </span>
      {/if}
    </div>

    <div class="max-h-[60vh] overflow-y-auto">
      {#if loading && !details}
        <div class="px-3.5 py-6 text-center text-xs text-text-muted">Cargando alertas…</div>
      {:else if !details || details.alerts.length === 0}
        <div class="px-3.5 py-6 text-center text-xs text-text-muted">
          {#if totalAlerts > 0}
            No hay detalle disponible para las alertas activas.
          {:else}
            Todos los proveedores operativos.
          {/if}
        </div>
      {:else}
        {#each details.alerts as alert (alert.connectionId + alert.model + alert.alertType + alert.timestamp)}
          {@const remaining = remainingLabel(alert)}
          <div class="px-3.5 py-2.5 border-b border-border-subtle last:border-b-0">
            <div class="flex items-start gap-2">
              <span
                class="material-symbols-outlined text-[16px] mt-0.5 flex-shrink-0 {alert.alertType === 'lock'
                  ? 'text-amber-500'
                  : 'text-red-500'}"
              >
                {alertIcon(alert)}
              </span>
              <div class="min-w-0 flex-1">
                <div class="flex items-center gap-1.5 flex-wrap">
                  <span class="text-xs font-semibold text-text-main">
                    {providerName(alert)}
                  </span>
                  <span class="text-[10px] text-text-subtle">·</span>
                  <span class="text-[11px] text-text-muted">API KEY:</span>
                  <span class="text-[11px] font-mono text-text-main truncate">
                    {keyName(alert)}
                  </span>
                </div>

                <div class="text-[11px] text-text-muted mt-0.5 break-words">
                  {alertLabel(alert)}: {alert.message}
                </div>

                <div class="flex items-center gap-2 mt-1 flex-wrap text-[10px] text-text-subtle font-mono">
                  {#if alert.model}
                    <span class="px-1.5 py-0.5 rounded bg-surface-2">model: {alert.model}</span>
                  {/if}
                  {#if alert.status}
                    <span class="px-1.5 py-0.5 rounded bg-surface-2">HTTP {alert.status}</span>
                  {/if}
                  {#if remaining}
                    <span class="px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-600 dark:text-amber-400">
                      {remaining}
                    </span>
                  {/if}
                  {#if alert.apiKeyMasked}
                    <span>{alert.apiKeyMasked}</span>
                  {/if}
                </div>
              </div>
            </div>
          </div>
        {/each}
      {/if}
    </div>

    {#if onClick}
      <div class="px-3 py-2 border-t border-border-subtle bg-surface-2">
        <button
          type="button"
          onclick={goToProviders}
          class="w-full text-[11px] font-medium text-primary hover:text-primary-hover transition-colors cursor-pointer flex items-center justify-center gap-1"
        >
          <span class="material-symbols-outlined text-[14px]">dns</span>
          Ver proveedores
        </button>
      </div>
    {/if}
  </div>
{/if}
