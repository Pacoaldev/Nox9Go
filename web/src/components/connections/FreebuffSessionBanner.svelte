<script lang="ts">
  import { RotateCcw, ArrowLeftRight, AlertTriangle, CircleAlert } from 'lucide-svelte'
  import type {
    FreebuffSessionStatusResponse,
    FreebuffSessionSwitchResponse,
  } from '../../api/client'

  interface ModelOption {
    id: string
    name: string
  }

  export interface ConnectionOption {
    id: string
    name: string
    isActive: boolean
    currentModel?: string
    status?: string
  }

  interface Props {
    session: FreebuffSessionStatusResponse | null
    isLoading: boolean
    expiresInMin: number | null
    onRefresh: () => void
    /** Selectable models. Switching ends the current session and re-admits. */
    models?: ModelOption[]
    onSwitch?: (model: string) => Promise<FreebuffSessionSwitchResponse>
    /** Available Freebuff connections so user can manage session per account. */
    connections?: ConnectionOption[]
    selectedConnectionId?: string
    onSelectConnection?: (id: string) => void
  }

  let {
    session,
    isLoading,
    expiresInMin,
    onRefresh,
    models = [],
    onSwitch,
    connections = [],
    selectedConnectionId,
    onSelectConnection,
  }: Props = $props()

  let selectedModel = $state('')
  let isSwitching = $state(false)
  let switchError = $state('')
  let refundNotice = $state('')

  let status = $derived(session?.status ?? 'none')
  let activeModel = $derived(session?.currentModel ?? '')
  let isActive = $derived(status === 'active' && activeModel !== '')
  // A blocked region still reports an active session, so the warning has to be
  // read off the payload rather than inferred from the status.
  let countryBlocked = $derived(!!session?.countryBlockReason || status === 'country_blocked')

  // Follow the server: a session that changed model elsewhere must not leave a
  // stale pick in the dropdown.
  $effect(() => {
    selectedModel = activeModel
  })

  let switchModel = $derived(selectedModel.trim())
  let canSwitch = $derived(
    !!onSwitch && switchModel !== '' && switchModel !== activeModel && !isSwitching
  )
  let actionLabel = $derived(isActive ? 'Switch model' : 'Start session')

  let tone = $derived.by(() => {
    if (status === 'banned') {
      return {
        card: 'border-red-500/30 bg-red-500/10 text-red-800 dark:text-red-200',
        title: 'text-red-900 dark:text-red-100',
        muted: 'text-red-700/90 dark:text-red-300/90',
        button:
          'border-red-500/40 bg-red-500/15 hover:bg-red-500/25 text-red-800 dark:text-red-100',
      }
    }
    // A flagged region is a warning, not a failure: Freebuff runs most of the
    // world on limited access and the flag does not by itself refuse a turn.
    if (countryBlocked) {
      return {
        card: 'border-amber-500/30 bg-amber-500/10 text-amber-800 dark:text-amber-200',
        title: 'text-amber-900 dark:text-amber-100',
        muted: 'text-amber-700/90 dark:text-amber-300/90',
        button:
          'border-amber-500/40 bg-amber-500/15 hover:bg-amber-500/25 text-amber-800 dark:text-amber-100',
      }
    }
    if (status === 'active') {
      return {
        card: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-800 dark:text-emerald-200',
        title: 'text-emerald-900 dark:text-emerald-100',
        muted: 'text-emerald-700/90 dark:text-emerald-300/90',
        button:
          'border-emerald-500/40 bg-emerald-500/15 hover:bg-emerald-500/25 text-emerald-800 dark:text-emerald-100',
      }
    }
    if (status === 'unauthorized' || status === 'queued') {
      return {
        card: 'border-amber-500/30 bg-amber-500/10 text-amber-800 dark:text-amber-200',
        title: 'text-amber-900 dark:text-amber-100',
        muted: 'text-amber-700/90 dark:text-amber-300/90',
        button:
          'border-amber-500/40 bg-amber-500/15 hover:bg-amber-500/25 text-amber-800 dark:text-amber-100',
      }
    }
    return {
      card: 'border-border bg-surface-2/60 text-text-main',
      title: 'text-text-main',
      muted: 'text-text-muted',
      button: 'border-border bg-surface-3 hover:bg-surface text-text-main',
    }
  })

  let headline = $derived.by(() => {
    if (!session && isLoading) return 'Checking session…'
    switch (status) {
      case 'active':
        return 'Active session'
      case 'queued':
        return 'Waiting in queue'
      case 'unauthorized':
        return 'Session token rejected'
      case 'banned':
        return 'Account banned'
      case 'country_blocked':
        return 'Region flagged'
      default:
        return 'No active session'
    }
  })

  let detail = $derived.by(() => {
    if (!session && isLoading) return 'Asking Freebuff for this account’s session.'
    switch (status) {
      case 'active':
        return 'Freebuff binds this account to one model per session, and a session lasts an hour even when idle. Switching ends the current session and starts a new one on the model you pick.'
      case 'queued':
        return 'Freebuff put this account in the waiting room for the selected model. Refresh to check again.'
      case 'unauthorized':
        return 'Freebuff rejected this account’s token. Re-connect the account, or pick another one from the connections list.'
      case 'banned':
        return 'Freebuff banned this account. Add a different account — re-authenticating this one will not help.'
      case 'country_blocked':
        return 'Freebuff does not serve this region for this account.'
      default:
        return 'Freebuff starts a session on the first request and binds it to that model for an hour. Pick the model now to start one deliberately.'
    }
  })

  let sessionsUsed = $derived.by(() => {
    const limit = session?.rateLimit?.limit
    if (typeof limit !== 'number' || limit <= 0) return null
    const used = session?.rateLimit?.recentCount ?? 0
    const pool = session?.rateLimit?.poolLabel ? `${session.rateLimit.poolLabel} ` : ''
    return `${pool}${used}/${limit} sessions used`
  })

  let freebucksBalance = $derived(
    typeof session?.freebucks?.balance === 'number' ? session.freebucks.balance : null
  )

  async function handleSwitch() {
    if (!onSwitch || !canSwitch) return
    isSwitching = true
    switchError = ''
    refundNotice = ''
    try {
      const res = await onSwitch(switchModel)
      if (res.switched === false) {
        refundNotice = `Already on ${switchModel}.`
      } else if (res.freebucksRefund && res.freebucksRefund > 0) {
        refundNotice = `Ended your previous session — ${res.freebucksRefund} Freebucks refunded. Now on ${res.currentModel}.`
      } else {
        refundNotice = isActive
          ? `Ended your previous session and switched to ${res.currentModel}.`
          : `Session started on ${res.currentModel}.`
      }
    } catch (err) {
      switchError = err instanceof Error ? err.message : String(err)
    } finally {
      isSwitching = false
    }
  }
</script>

<div class="mt-4 p-4 rounded-xl border {tone.card} text-xs flex items-start gap-3 leading-relaxed shadow-xs">      <span class="text-base shrink-0 leading-none">
    {#if countryBlocked || status === 'banned'}
      <AlertTriangle class="w-4 h-4" />
    {:else if isActive}
      🔒
    {:else}
      <CircleAlert class="w-4 h-4" />
    {/if}
  </span>

  <div class="flex-1 min-w-0">
    <div class="flex items-center justify-between gap-2 flex-wrap">
      <p class="font-semibold text-sm {tone.title}">
        {headline}
        {#if isActive}
          : <span class="font-mono bg-emerald-500/20 px-1.5 py-0.5 rounded text-xs">{activeModel}</span>
          {#if expiresInMin !== null}
            <span class="font-normal text-xs {tone.muted} ml-1">
              ({expiresInMin > 0 ? `Expires in ${expiresInMin} min` : 'Expires soon'})
            </span>
          {/if}
        {/if}
        {#if session?.connectionName}
          <span class="font-normal text-xs {tone.muted} ml-1.5">
            · Account: <span class="font-mono font-medium px-1.5 py-0.5 rounded bg-black/5 dark:bg-white/10">{session.connectionName}</span>
          </span>
        {/if}
      </p>
      <button
        type="button"
        title="Refresh session status"
        disabled={isLoading}
        onclick={onRefresh}
        class="p-1 rounded-md {tone.muted} hover:bg-black/5 dark:hover:bg-white/10 transition-colors cursor-pointer"
      >
        <RotateCcw class="w-3.5 h-3.5 {isLoading ? 'animate-spin' : ''}" />
      </button>
    </div>

    <p class="mt-1 {tone.muted}">{detail}</p>

    {#if countryBlocked}
      <p class="mt-1 font-medium">
        Region flagged{session?.countryCode ? ` (${session.countryCode})` : ''}: {session?.countryBlockReason ??
          'country_not_allowed'}. Only a short list of countries (US, CA, GB, and parts of the EU/APAC) gets Freebuff's
        full-access tier — elsewhere the account runs on limited access and some requests can be refused.
      </p>
    {/if}

    {#if session?.accessTier || sessionsUsed || freebucksBalance !== null}
      <div class="mt-1.5 flex items-center gap-2 flex-wrap {tone.muted}">
        {#if session?.accessTier}
          <span class="px-1.5 py-0.5 rounded border border-black/10 dark:border-white/20 uppercase text-[10px] font-semibold">
            {session.accessTier}
          </span>
        {/if}
        {#if sessionsUsed}
          <span>{sessionsUsed}</span>
        {/if}
        {#if freebucksBalance !== null}
          <span>· {freebucksBalance} Freebucks</span>
        {/if}
        {#if session?.countryCode && !countryBlocked}
          <span>· {session.countryCode}</span>
        {/if}
      </div>
    {/if}
    {#if connections && connections.length > 1}
      <div class="mt-2.5 flex items-center gap-2 flex-wrap text-xs">
        <span class="font-medium {tone.muted}">Manage Account:</span>
        <div class="flex items-center gap-1.5 flex-wrap">
          {#each connections as conn (conn.id)}
            <button
              type="button"
              onclick={() => onSelectConnection?.(conn.id)}
              class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs font-medium border transition-colors cursor-pointer {selectedConnectionId === conn.id ? 'bg-primary text-white border-primary shadow-xs' : 'border-border bg-surface-2 hover:bg-surface-3 text-text-main'}"
            >
              <span>{conn.name}</span>
              {#if conn.currentModel}
                <span class="font-mono text-[10px] {selectedConnectionId === conn.id ? 'text-white/90 bg-black/20' : 'text-emerald-600 dark:text-emerald-400 bg-emerald-500/10'} px-1 py-0.5 rounded">
                  {conn.currentModel.split('/').pop()}
                </span>
              {:else if conn.status === 'banned'}
                <span class="text-[10px] text-red-500 font-semibold">(banned)</span>
              {/if}
            </button>
          {/each}
        </div>
      </div>
    {/if}


    {#if onSwitch && models.length > 0}
      <div class="mt-2.5 flex items-center gap-2 flex-wrap">
        <select
          bind:value={selectedModel}
          disabled={isSwitching}
          class="min-w-0 max-w-full px-2 py-1 text-xs rounded-md border {tone.button} bg-background text-text-main focus:outline-none font-mono cursor-pointer disabled:opacity-50"
        >
          {#each models as model (model.id)}
            <option value={model.id}>{model.id}</option>
          {/each}
          {#if activeModel && !models.some((m) => m.id === activeModel)}
            <option value={activeModel}>{activeModel}</option>
          {/if}
        </select>
        <button
          type="button"
          disabled={!canSwitch}
          onclick={handleSwitch}
          class="inline-flex items-center gap-1.5 px-2.5 py-1 text-xs font-semibold rounded-md border {tone.button} transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <ArrowLeftRight class="w-3.5 h-3.5 {isSwitching ? 'animate-pulse' : ''}" />
          {isSwitching ? 'Working…' : actionLabel}
        </button>
      </div>
    {/if}

    {#if switchError}
      <p class="mt-1.5 text-red-600 dark:text-red-400">
        Failed: {switchError}{isActive ? ' — the current session is still active.' : ''}
      </p>
    {/if}
    {#if refundNotice}
      <p class="mt-1.5 font-medium {tone.title}">{refundNotice}</p>
    {/if}
  </div>
</div>
