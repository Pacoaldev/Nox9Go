<script lang="ts">
  // Capacity limits for an INTEGRATED (catalog) provider.
  //
  // The custom provider node has the same editor inside its dialog; a catalog
  // provider has no node row, so this is its home. The fields, the merge semantics
  // and the "0 means unlimited / empty means no expiry" convention are identical, so
  // an operator who learned one editor has learned both.
  //
  // The values can be prefilled from what the provider actually reported at runtime
  // (the observations list below the form): applying a suggestion fills the field but
  // never saves on its own. The operator saves to apply — a transient upstream error
  // must never silently throttle a provider.
  import Input from '../../lib/ui/Input.svelte'
  import Button from '../../lib/ui/Button.svelte'
  import { EMPTY_NODE_LIMITS, nodeLimitsPatchOf, nodeLimitsSummary, type NodeLimitsDraft } from '../../lib/node-limits'
  import { api, type ProviderCapacity, type ProviderCapacityLimits, type ProviderObservation } from '../../api/client'

  interface Props {
    providerId: string
    providerName?: string
  }

  let { providerId, providerName = '' }: Props = $props()

  let capacity = $state<ProviderCapacity | null>(null)
  let draft = $state<NodeLimitsDraft>({ ...EMPTY_NODE_LIMITS })
  let open = $state(false)
  let loading = $state(true)
  let saving = $state(false)
  let error = $state('')
  let saved = $state(false)

  let loadedFor: string | null = null

  // The stored policy as a draft. Same conversion the node editor uses, kept local
  // because the capacity shape is not ProviderNodeLimits (no usage counters here).
  function draftOf(limits: ProviderCapacityLimits): NodeLimitsDraft {
    let expires = ''
    if (limits.contractExpiresAt) {
      const at = new Date(limits.contractExpiresAt)
      expires = Number.isNaN(at.getTime()) ? limits.contractExpiresAt.slice(0, 10) : at.toISOString().slice(0, 10)
    }
    const count = (value: number) => (value > 0 ? String(value) : '')
    return {
      rpm: count(limits.rpm),
      tpm: count(limits.tpm),
      concurrency: count(limits.concurrency),
      dailyRequests: count(limits.dailyRequests),
      tokenQuota: count(limits.tokenQuota),
      queueTimeoutMs: count(limits.queueTimeoutMs),
      contractExpiresAt: expires,
    }
  }

  async function load(id: string) {
    loading = true
    error = ''
    try {
      const res = await api.getProviderCapacity(id)
      capacity = res
      draft = draftOf(res.limits)
      // Always start collapsed: the section is opt-in, even when a policy is stored.
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      loading = false
    }
  }

  // Load once per provider. Tracked on providerId only; the draft is written by the
  // form and reading it back here would reload on every keystroke.
  $effect(() => {
    const id = providerId
    if (!id || loadedFor === id) return
    loadedFor = id
    void load(id)
  })

  async function save() {
    if (saving) return
    saving = true
    error = ''
    saved = false
    try {
      const res = await api.setProviderCapacity(providerId, nodeLimitsPatchOf(draft))
      capacity = res
      draft = draftOf(res.limits)
      saved = true
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      saving = false
    }
  }

  function clearAll() {
    draft = { ...EMPTY_NODE_LIMITS }
  }

  // Fill the per-minute fields from the observed values. A bare window count
  // (requests/tokens) is never mapped here — it is not a per-minute figure.
  function applySuggestions() {
    const s = capacity?.suggestions
    if (!s) return
    if (s.rpm > 0) draft.rpm = String(s.rpm)
    if (s.tpm > 0) draft.tpm = String(s.tpm)
    if (s.concurrency > 0) draft.concurrency = String(s.concurrency)
    if (s.dailyRequests > 0) draft.dailyRequests = String(s.dailyRequests)
    if (s.tokenQuota > 0) draft.tokenQuota = String(s.tokenQuota)
  }

  function applyObservation(obs: ProviderObservation) {
    const v = obs.limitValue ?? 0
    if (v <= 0) return
    switch (obs.limitUnit) {
      case 'rpm':
        draft.rpm = String(v)
        break
      case 'tpm':
        draft.tpm = String(v)
        break
      case 'concurrent':
        draft.concurrency = String(v)
        break
      case 'rpd':
        draft.dailyRequests = String(v)
        break
    }
  }

  const signalLabels: Record<string, string> = {
    rate_limit: 'Rate limit',
    quota_exceeded: 'Quota exhausted',
    capacity: 'At capacity',
    timeout: 'Timeout',
    auth: 'Auth',
    server_error: 'Server error',
  }

  function signalLabel(signal: string): string {
    return signalLabels[signal] ?? signal
  }

  const unitLabels: Record<string, string> = {
    rpm: 'req/min',
    tpm: 'tok/min',
    rpd: 'req/day',
    concurrent: 'in flight',
    requests: 'requests',
    tokens: 'tokens',
  }

  function unitLabel(unit: string): string {
    return unitLabels[unit] ?? unit
  }

  function shortTime(value: string): string {
    const at = new Date(value)
    if (Number.isNaN(at.getTime())) return value
    return at.toLocaleString()
  }

  const noiseVisibilityKey = (id: string) => `capacityObservationsNoiseVisible:${id}`
  let noiseVisible = $state(false)

  function readNoiseVisibility(id: string): boolean {
    if (typeof localStorage === 'undefined') return false
    try {
      return localStorage.getItem(noiseVisibilityKey(id)) === '1'
    } catch {
      return false
    }
  }

  function toggleNoiseVisibility() {
    noiseVisible = !noiseVisible
    if (typeof localStorage === 'undefined') return
    try {
      if (noiseVisible) localStorage.setItem(noiseVisibilityKey(providerId), '1')
      else localStorage.removeItem(noiseVisibilityKey(providerId))
    } catch {
      // Keep the current view usable when browser storage is unavailable.
    }
  }

  let usableObservations = $derived(
    (capacity?.observations ?? []).filter((obs) =>
      (obs.limitUnit === 'rpm' || obs.limitUnit === 'tpm' || obs.limitUnit === 'concurrent' || obs.limitUnit === 'rpd') &&
      (obs.limitValue ?? 0) > 0,
    ),
  )
  let noisyObservations = $derived(
    (capacity?.observations ?? []).filter((obs) => !usableObservations.includes(obs)),
  )
  let suggestionsReady = $derived(
    (capacity?.suggestions.rpm ?? 0) > 0 ||
      (capacity?.suggestions.tpm ?? 0) > 0 ||
      (capacity?.suggestions.concurrency ?? 0) > 0 ||
      (capacity?.suggestions.dailyRequests ?? 0) > 0 ||
      (capacity?.suggestions.tokenQuota ?? 0) > 0,
  )

  $effect(() => {
    noiseVisible = readNoiseVisibility(providerId)
  })
</script>

{#if !loading || capacity}
  <div class="bg-surface border border-border-subtle rounded-[14px] shadow-[var(--shadow-soft)] p-6">
    <button
      type="button"
      class="flex w-full items-start justify-between gap-3 text-left cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-500/40 rounded-md"
      aria-expanded={open}
      aria-controls="provider-capacity-fields"
      onclick={() => (open = !open)}
    >
      <span class="flex flex-col gap-0.5">
        <span class="text-sm font-medium text-text-main">Capacity limits</span>
        <span class="text-xs text-text-muted">
          {#if open}Optional. Applied to every request routed through {providerName || providerId}.{:else}{nodeLimitsSummary(draft)}{/if}
        </span>
      </span>
      <span class="material-symbols-outlined text-[20px] text-text-muted shrink-0" aria-hidden="true">
        {open ? 'expand_less' : 'expand_more'}
      </span>
    </button>

    {#if open}
      <div id="provider-capacity-fields" class="mt-4 flex flex-col gap-4">
        <div class="grid gap-4 sm:grid-cols-2">
          <Input id="provider-capacity-rpm" label="Requests / minute" type="number" min="0" bind:value={draft.rpm} placeholder="0" />
          <Input id="provider-capacity-tpm" label="Prompt tokens / minute" type="number" min="0" bind:value={draft.tpm} placeholder="0" />
          <Input id="provider-capacity-concurrency" label="Concurrent requests" type="number" min="0" bind:value={draft.concurrency} placeholder="0" />
          <Input
            id="provider-capacity-daily"
            label="Requests / day"
            type="number"
            min="0"
            bind:value={draft.dailyRequests}
            placeholder="0"
            hint="A fixed daily budget. It resets at 00:00 UTC, not one minute after the first request."
          />
          <Input
            id="provider-capacity-queue"
            label="Queue wait (ms)"
            type="number"
            min="0"
            bind:value={draft.queueTimeoutMs}
            placeholder="0"
            hint="How long a request waits for a slot before the provider counts as full."
          />
          <Input
            id="provider-capacity-quota"
            label="Lifetime token quota"
            type="number"
            min="0"
            bind:value={draft.tokenQuota}
            placeholder="0"
            hint="Prompt + completion tokens for this provider. Once spent, requests to it answer 403."
          />
          <Input
            id="provider-capacity-expiry"
            label="Contract ends (optional)"
            type="date"
            bind:value={draft.contractExpiresAt}
            hint="Last day the subscription is valid. Empty means no expiry."
          />
        </div>

        <p class="text-xs text-text-muted">
          0 means unlimited. A request that cannot get a slot is served by another candidate; 429 only appears once no
          candidate is left.
        </p>

        <div class="flex flex-wrap gap-2">
          <Button variant="primary" size="sm" loading={saving} onclick={save}>Save limits</Button>
          <Button variant="ghost" size="sm" onclick={clearAll} disabled={saving}>Clear</Button>
          {#if suggestionsReady}
            <Button variant="secondary" size="sm" onclick={applySuggestions} disabled={saving}>Apply observed values</Button>
          {/if}
        </div>

        {#if saved}
          <p class="text-xs text-emerald-600 dark:text-emerald-400">Limits saved.</p>
        {/if}
        {#if error}
          <p class="text-xs text-red-500" role="alert">{error}</p>
        {/if}

        <div class="flex flex-col gap-2 border-t border-border pt-3">
          <div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
            <span class="text-xs font-medium text-text-main">Observed from this provider</span>
            <span class="text-xs text-text-subtle">
              {capacity?.observations.length ?? 0} fact{(capacity?.observations.length ?? 0) === 1 ? '' : 's'}
            </span>
          </div>
          {#if capacity && capacity.observations.length > 0}
            <p class="text-xs text-text-muted">
              What {providerName || providerId} itself reported at runtime. Applying a value fills the form above; nothing
              is saved until you press Save limits.
            </p>
            {#if noisyObservations.length > 0}
              <button
                type="button"
                class="self-start text-xs text-text-muted underline decoration-dotted underline-offset-2 hover:text-text-main focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-500/40 rounded-sm"
                aria-expanded={noiseVisible}
                onclick={toggleNoiseVisibility}
              >
                {noiseVisible ? 'Hide' : 'Show'} {noisyObservations.length} observations without usable limits
              </button>
            {/if}
            <div class="flex flex-col divide-y divide-border-subtle">
              {#each (noiseVisible ? capacity.observations : usableObservations) as obs (obs.signal + '|' + (obs.model ?? '') + '|' + (obs.limitUnit ?? '') + '|' + (obs.limitValue ?? 0))}
                <div class="flex flex-wrap items-center justify-between gap-2 py-2">
                  <div class="min-w-0">
                    <div class="flex flex-wrap items-center gap-2">
                      <span class="text-xs font-medium text-text-main">{signalLabel(obs.signal)}</span>
                      {#if obs.statusCode}
                        <span class="text-[11px] text-text-subtle font-mono">HTTP {obs.statusCode}</span>
                      {/if}
                      {#if obs.model}
                        <span class="text-[11px] text-text-subtle font-mono">{obs.model}</span>
                      {/if}
                      <span class="text-[11px] text-text-subtle">{obs.sampleCount}x · {shortTime(obs.lastSeen)}</span>
                    </div>
                    {#if obs.evidence}
                      <p class="mt-0.5 break-words text-[11px] text-text-muted">{obs.evidence}</p>
                    {/if}
                  </div>
                  <div class="flex shrink-0 items-center gap-2">
                    {#if obs.limitValue && obs.limitUnit}
                      <span class="text-xs font-mono text-text-main">{obs.limitValue} {unitLabel(obs.limitUnit)}</span>
                    {/if}
                    {#if obs.limitUnit === 'rpm' || obs.limitUnit === 'tpm' || obs.limitUnit === 'concurrent' || obs.limitUnit === 'rpd'}
                      <Button variant="ghost" size="sm" onclick={() => applyObservation(obs)}>Use</Button>
                    {/if}
                  </div>
                </div>
              {/each}
            </div>
          {:else}
            <p class="text-xs text-text-muted">
              Nothing observed yet. As {providerName || providerId} reports rate limits or refusals, they will appear here
              and can prefill the fields above.
            </p>
          {/if}
        </div>
      </div>
    {/if}
  </div>
{/if}
