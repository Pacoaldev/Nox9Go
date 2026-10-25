<script lang="ts">
  import { dashboardStatus } from '../lib/systemStatus.svelte'

  interface Props {
    onClick?: () => void
  }

  let { onClick }: Props = $props()

  const status = $derived(dashboardStatus())
  const totalAlerts = $derived(status.alertConnections + status.lockedConnections)

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
</script>

<button
  type="button"
  onclick={onClick}
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
