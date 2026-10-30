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

{#if collapsed}
  <div
    class="flex items-center gap-2 select-none justify-center py-1"
    role="status"
    aria-label="Estado de servicios"
  >
    <span
      class="size-3 rounded-full transition-colors {STATE_CLASSES[status.tunnel]}"
      title="Túnel Cloudflare: {STATE_LABELS[status.tunnel]}"
    ></span>
    <span
      class="size-3 rounded-full transition-colors {STATE_CLASSES[status.database]}"
      title="Base de datos SQLite: {STATE_LABELS[status.database]}"
    ></span>
  </div>
{:else}
  <div
    class="flex items-center gap-1.5 select-none py-1"
    role="status"
    aria-label="Estado de servicios"
  >
    <!-- Túnel Cloudflare -->
    <div
      class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full border border-border-subtle bg-surface-2 text-[11px] font-mono text-text-muted"
      title="Túnel Cloudflare: {STATE_LABELS[status.tunnel]}"
    >
      <span class="size-1.5 rounded-full {STATE_CLASSES[status.tunnel]}"></span>
      <span>Tunnel</span>
    </div>

    <!-- Base de datos -->
    <div
      class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full border border-border-subtle bg-surface-2 text-[11px] font-mono text-text-muted"
      title="Base de datos SQLite: {STATE_LABELS[status.database]}"
    >
      <span class="size-1.5 rounded-full {STATE_CLASSES[status.database]}"></span>
      <span>DB</span>
    </div>
  </div>
{/if}
