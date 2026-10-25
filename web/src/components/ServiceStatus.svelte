<script lang="ts">
  import { dashboardStatus, type ServiceState } from '../lib/systemStatus.svelte'

  interface Props {
    collapsed?: boolean
  }

  let { collapsed = false }: Props = $props()

  const status = $derived(dashboardStatus())

  const STATE_CLASSES: Record<ServiceState, string> = {
    ok: 'bg-[#27C93F] shadow-[0_0_8px_rgba(39,201,63,0.3)]',
    error: 'bg-[#FF5F56] shadow-[0_0_8px_rgba(255,95,86,0.3)]',
    idle: 'bg-[#FFBD2E]/50',
    unknown: 'bg-surface-3',
  }

  const STATE_LABELS: Record<ServiceState, string> = {
    ok: 'En línea',
    error: 'Fallo',
    idle: 'Inactivo',
    unknown: 'Comprobando...',
  }
</script>

<div
  class="flex items-center gap-2 select-none {collapsed ? 'justify-center py-1' : ''}"
  role="status"
  aria-label="Estado de servicios"
>
  <!-- Dot 1: Gateway -->
  <span
    class="size-3 rounded-full transition-colors {STATE_CLASSES[status.gateway]}"
    title="Gateway API: {STATE_LABELS[status.gateway]}{status.latencyMs !== null ? ` (${status.latencyMs} ms)` : ''}"
  ></span>

  <!-- Dot 2: SQLite DB -->
  <span
    class="size-3 rounded-full transition-colors {STATE_CLASSES[status.database]}"
    title="Base de datos SQLite: {STATE_LABELS[status.database]}"
  ></span>

  <!-- Dot 3: Cloudflare Tunnel -->
  <span
    class="size-3 rounded-full transition-colors {STATE_CLASSES[status.tunnel]}"
    title="Túnel Cloudflare: {STATE_LABELS[status.tunnel]}"
  ></span>
</div>
