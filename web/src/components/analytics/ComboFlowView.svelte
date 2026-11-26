<script lang="ts">
  import { api, type ProviderConnection } from '../../api/client'
  import ComboFlowCard from './ComboFlowCard.svelte'
  import { connectUsageStream } from './usageStream'
  import { liveComboNames } from './comboMatch'
  import type { ActiveRequestItem, RecentRequestItem } from './types'
  import Card from '../../lib/ui/Card.svelte'

  let comboDefs = $state<{ name: string; members: string[]; strategy?: string }[]>([])
  let activeRequests = $state<ActiveRequestItem[]>([])
  let recentRequests = $state<RecentRequestItem[]>([])
  let errorProvider = $state('')
  /** Connections carry rateLimitedUntil / modelLock_* so the card can mark a member locked only when every key is parked. */
  let connections = $state<ProviderConnection[]>([])

  $effect(() => {
    let cancelled = false
    api
      .getCombos()
      .then((rows: any[]) => {
        if (cancelled) return
        comboDefs = (Array.isArray(rows) ? rows : [])
          .map((c: any) => {
            let members: string[] = []
            try {
              const raw = typeof c?.models === 'string' ? JSON.parse(c.models) : c?.models
              if (Array.isArray(raw)) members = raw.map((m: any) => String(m))
            } catch {
              members = []
            }
            return { name: String(c?.name ?? ''), members, strategy: String(c?.strategy ?? '') }
          })
          .filter((c: { name: string }) => c.name !== '')
      })
      .catch(() => {
        if (!cancelled) comboDefs = []
      })
    return () => {
      cancelled = true
    }
  })

  $effect(() => {
    return connectUsageStream((payload) => {
      if (Array.isArray(payload.activeRequests)) activeRequests = payload.activeRequests
      if (Array.isArray(payload.recentRequests)) recentRequests = payload.recentRequests
      if (payload.errorProvider) errorProvider = payload.errorProvider
    })
  })

  // Lock stamps change without a usage event, so poll connections while this
  // page is open. 10s keeps the 1 vCPU budget calm and still tracks a 5-min lockdown.
  /**
   * The combos that get a panel, one each. A combo appears while it has a
   * request in flight and stays while its result is inside the recent window.
   * Ordered by the combo list, so a panel never changes column when a sibling
   * combo starts or finishes.
   */
  let liveCombos = $derived.by(() => {
    const order = new Map(comboDefs.map((c, i) => [c.name.toLowerCase().trim(), { name: c.name, i }]))
    return liveComboNames(activeRequests, recentRequests)
      .map((name) => order.get(name.toLowerCase()))
      .filter((entry): entry is { name: string; i: number } => Boolean(entry))
      .sort((a, b) => a.i - b.i)
      .map((entry) => entry.name)
  })

  $effect(() => {
    let cancelled = false
    async function loadConnections() {
      try {
        const rows = await api.getConnections()
        if (!cancelled) connections = Array.isArray(rows) ? rows : []
      } catch {
        if (!cancelled) connections = []
      }
    }
    void loadConnections()
    const timer = setInterval(loadConnections, 10_000)
    return () => {
      cancelled = true
      clearInterval(timer)
    }
  })
</script>

<div class="flex flex-col gap-4">
  {#snippet panelHeader()}
    <div
      class="flex flex-col gap-3 border-b border-border bg-surface-2 px-5 py-3 sm:flex-row sm:items-center sm:justify-between"
    >
      <div class="flex min-w-0 items-center gap-2">
        <span class="material-symbols-outlined text-[18px] text-text-muted" aria-hidden="true"
          >account_tree</span
        >
        <div class="min-w-0">
          <h2 class="font-headline text-sm font-bold text-text-main">Combo flow</h2>
          <p class="font-body text-xs text-text-muted">Router → combo → members, live</p>
        </div>
      </div>
    </div>
  {/snippet}

  {#if liveCombos.length === 0}
    <Card padding="none" class="overflow-hidden border border-border">
      {@render panelHeader()}
      <div class="p-5">
        <p class="font-body text-sm text-text-muted">
          Waiting for a combo request. The live map appears while a combo is in use.
        </p>
      </div>
    </Card>
  {:else}
    <!--
      One whole panel per combo in use. A single combo keeps the full width it
      had before, so the grid reserves a second column only when a second combo
      is actually live. From md up the two panels split the row equally and the
      grid stretches both to the same height; `gap-5` keeps them off each other
      and the page padding keeps them off the frame.
    -->
    <div
      class="grid grid-cols-1 gap-5 {liveCombos.length > 1 ? 'md:grid-cols-2' : ''}"
      data-testid="combo-flow-panels"
    >
      {#each liveCombos as name (name)}
        <Card padding="none" class="flex min-w-0 flex-col overflow-hidden border border-border">
          {@render panelHeader()}
          <div class="flex min-h-0 min-w-0 flex-1 flex-col p-5">
            <ComboFlowCard
              comboName={name}
              combos={comboDefs}
              {activeRequests}
              {recentRequests}
              {errorProvider}
              {connections}
            />
          </div>
        </Card>
      {/each}
    </div>
  {/if}
</div>
