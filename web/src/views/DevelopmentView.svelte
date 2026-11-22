<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import { api, ApiError, type DevStatusResponse } from '../api/client'

  // The development panel is a read-out, not a control surface: it shows where a
  // feature stands and never changes the repository. Everything visible here is
  // also reachable from the Telegram bot with /estado, because both render the
  // same report built by internal/devstatus.

  // The refresh interval is slow on purpose. This panel is watched while work is
  // happening, and a faster poll would spend the small box's CPU to redraw a
  // page whose content changes on the order of minutes.
  const REFRESH_MS = 30_000

  let status = $state<DevStatusResponse | null>(null)
  let loading = $state(true)
  let error = $state('')
  let selected = $state('')

  let features = $derived(status?.features ?? [])
  let feature = $derived(status?.feature ?? null)
  let progress = $derived(
    feature && feature.total > 0 ? Math.round((feature.done / feature.total) * 100) : 0,
  )

  let timer: ReturnType<typeof setInterval> | null = null

  async function load() {
    try {
      status = await api.getDevStatus(selected || undefined)
      error = ''
    } catch (e) {
      error = e instanceof ApiError ? e.message : 'Could not read the development report.'
    } finally {
      loading = false
    }
  }

  async function pick(name: string) {
    selected = name === selected ? '' : name
    await load()
  }

  onMount(() => {
    void load()
    timer = setInterval(() => void load(), REFRESH_MS)
    return () => {
      if (timer) clearInterval(timer)
    }
  })

  onDestroy(() => {
    if (timer) clearInterval(timer)
  })
</script>

<div class="space-y-4">
  <div class="flex flex-wrap items-center justify-between gap-3">
    <div>
      <h2 class="text-lg font-semibold text-text-main">Development</h2>
      <p class="text-sm text-text-muted">
        Where the current feature stands. Also available from the Telegram bot with /estado.
      </p>
    </div>
    <button
      class="rounded-[10px] border border-border bg-surface-2 px-3 py-1.5 text-sm text-text-main hover:bg-surface-3 disabled:opacity-50"
      onclick={() => void load()}
      disabled={loading}
    >
      {loading ? 'Refreshing…' : 'Refresh'}
    </button>
  </div>

  {#if error}
    <div class="rounded-[14px] border border-red-500/40 bg-red-500/10 px-4 py-3 text-sm text-red-300">
      {error}
    </div>
  {/if}

  {#if status && !status.configured}
    <div class="rounded-[14px] border border-border bg-surface-2 px-4 py-6 text-sm text-text-muted">
      <p class="text-text-main">No checkout configured.</p>
      <p class="mt-2">
        Set <code class="rounded bg-surface-3 px-1.5 py-0.5 text-text-main">DEV_REPO_ROOT</code>
        to a repository path and restart the gateway to see the development report here.
      </p>
    </div>
  {:else if status && status.configured}
    {#if status.feature}
      <div class="rounded-[14px] border border-border bg-surface-2 p-4">
        <div class="flex items-baseline justify-between gap-3">
          <p class="font-medium text-text-main">{status.feature.title || status.feature.name}</p>
          <p class="text-sm text-text-muted">
            {status.feature.done}/{status.feature.total} tasks
          </p>
        </div>
        <div class="mt-3 h-2 w-full overflow-hidden rounded-full bg-surface-3">
          <div class="h-full rounded-full bg-primary transition-[width] duration-300" style={`width: ${progress}%`}></div>
        </div>
      </div>
    {/if}

    <div class="grid gap-3 sm:grid-cols-3">
      <div class="rounded-[14px] border border-border bg-surface-2 p-4">
        <p class="text-xs uppercase tracking-wide text-text-subtle">Branch</p>
        <p class="mt-1 truncate font-mono text-sm text-text-main">{status.branch || '—'}</p>
      </div>
      <div class="rounded-[14px] border border-border bg-surface-2 p-4">
        <p class="text-xs uppercase tracking-wide text-text-subtle">Last commit</p>
        <p class="mt-1 truncate font-mono text-sm text-text-main" title={status.headSubject}>
          {status.head || '—'} {status.headSubject || ''}
        </p>
      </div>
      <div class="rounded-[14px] border border-border bg-surface-2 p-4">
        <p class="text-xs uppercase tracking-wide text-text-subtle">Uncommitted</p>
        <p class="mt-1 text-sm text-text-main">{status.changes ?? 0} files</p>
      </div>
    </div>

    {#if features.length > 0}
      <div class="flex flex-wrap gap-2">
        {#each features as name (name)}
          <button
            class="rounded-full border px-3 py-1 text-xs transition-colors {selected === name
              ? 'border-primary bg-primary/15 text-primary'
              : 'border-border bg-surface-2 text-text-muted hover:text-text-main'}"
            onclick={() => void pick(name)}
          >
            {name}
          </button>
        {/each}
      </div>
    {/if}

    {#if status.rendered}
      <pre
        class="overflow-x-auto rounded-[14px] border border-border bg-surface-2 p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap text-text-main">{status.rendered}</pre>
    {/if}
  {/if}
</div>
