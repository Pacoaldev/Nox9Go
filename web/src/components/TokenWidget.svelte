<script lang="ts">
  import { dashboardStatus } from '../lib/systemStatus.svelte'

  const status = $derived(dashboardStatus())

  function formatTokens(count: number): string {
    if (count >= 1_000_000) return `${(count / 1_000_000).toFixed(1)}M`
    if (count >= 1_000) return `${(count / 1_000).toFixed(1)}k`
    return `${count}`
  }
</script>

<div
  class="hidden sm:flex items-center gap-1.5 px-2.5 py-1 rounded-lg border border-border-subtle bg-surface-2 text-text-muted text-xs hover:border-brand-500/30 transition-colors select-none"
  title="Uso de hoy: {status.requestsToday.toLocaleString()} peticiones, {status.totalTokensToday.toLocaleString()} tokens ({status.promptTokensToday.toLocaleString()} prompt / {status.completionTokensToday.toLocaleString()} completado)"
>
  <span class="material-symbols-outlined text-[15px] text-brand-500">data_usage</span>
  <span class="font-medium text-text-main font-mono">{formatTokens(status.totalTokensToday)}</span>
  <span class="text-[11px] text-text-muted">tokens</span>
</div>
