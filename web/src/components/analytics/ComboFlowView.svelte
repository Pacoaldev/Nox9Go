<script lang="ts">
  import { api } from '../../api/client'
  import ComboFlowCard from './ComboFlowCard.svelte'
  import { connectUsageStream } from './usageStream'
  import type { ActiveRequestItem, RecentRequestItem } from './types'
  import Card from '../../lib/ui/Card.svelte'

  let comboDefs = $state<{ name: string; members: string[]; strategy?: string }[]>([])
  let activeRequests = $state<ActiveRequestItem[]>([])
  let recentRequests = $state<RecentRequestItem[]>([])
  let errorProvider = $state('')

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
</script>

<div class="flex flex-col gap-4">
  <Card padding="none" class="overflow-hidden border border-border">
    <div class="flex flex-col gap-3 border-b border-border bg-surface-2 px-5 py-3 sm:flex-row sm:items-center sm:justify-between">
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
    <div class="p-5">
      <ComboFlowCard
        combos={comboDefs}
        {activeRequests}
        {recentRequests}
        {errorProvider}
      />
      {#if !activeRequests.some((r) => r.comboName) && !recentRequests.some((r) => r.comboName)}
        <p class="font-body text-sm text-text-muted">
          Waiting for a combo request. The live map appears while a combo is in use.
        </p>
      {/if}
    </div>
  </Card>
</div>
