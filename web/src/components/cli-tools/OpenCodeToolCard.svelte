<script lang="ts">
  /**
   * OpenCode configuration card.
   *
   * Replaces the card's old "export OPENCODE_BASE_URL" snippet, which was not
   * something OpenCode reads. The client is configured by writing
   * ~/.config/opencode/opencode.json(c), and that is what Apply does here —
   * through the same adapters the CLI uses, against the file that already
   * exists.
   */
  import { untrack } from 'svelte'
  import { AlertTriangle, Check, Loader2, RotateCcw, Save, Terminal } from 'lucide-svelte'
  import { api, type APIKey } from '../../api/client'

  interface Props {
    installed?: boolean
    configured?: boolean
    apiKeys?: APIKey[]
    /** Called after a successful apply/reset so the badge can refresh. */
    onChanged?: () => void
  }

  let { installed = false, configured = false, apiKeys = [], onChanged }: Props = $props()

  let endpoint = $state('')
  let selectedApiKey = $state('')
  let model = $state('')
  let configPath = $state('')
  let applying = $state(false)
  let resetting = $state(false)
  let message = $state<{ type: 'success' | 'error'; text: string } | null>(null)

  // SSR fallback uses the Go default port; the live origin wins on mount.
  let localOrigin = $state(
    typeof window !== 'undefined' ? window.location.origin : 'http://127.0.0.1:20130'
  )

  $effect(() => {
    if (typeof window === 'undefined') return
    localOrigin = window.location.origin
    untrack(() => {
      if (!endpoint) endpoint = window.location.origin
      if (apiKeys.length > 0 && !selectedApiKey) selectedApiKey = apiKeys[0].key
      void loadSettings()
    })
  })

  async function loadSettings() {
    try {
      const res = await api.getCliToolSettings('opencode')
      configPath = res.files[0]?.path ?? ''
    } catch {
      // The files list is informational; a failure must not block the form.
      configPath = ''
    }
  }

  async function handleApply() {
    applying = true
    message = null
    try {
      const res = await api.applyCliToolSettings('opencode', {
        baseUrl: endpoint,
        apiKey: selectedApiKey.trim() || undefined,
        model: model.trim() || undefined,
      })
      message = {
        type: 'success',
        text: res.changed
          ? `Wrote ${res.backups?.length ? `config (backup: ${res.backups.join(', ')}) ` : 'config '}for OpenCode.`
          : 'OpenCode was already configured exactly like this; nothing was rewritten.',
      }
      await loadSettings()
      onChanged?.()
    } catch (err) {
      message = { type: 'error', text: err instanceof Error ? err.message : 'Failed to apply settings' }
    } finally {
      applying = false
    }
  }

  async function handleReset() {
    resetting = true
    message = null
    try {
      const res = await api.resetCliToolSettings('opencode')
      message = {
        type: 'success',
        text: res.changed ? 'Removed the NOX9GO provider from OpenCode.' : 'Nothing to remove.',
      }
      onChanged?.()
    } catch (err) {
      message = { type: 'error', text: err instanceof Error ? err.message : 'Failed to reset settings' }
    } finally {
      resetting = false
    }
  }
</script>

<div class="flex flex-col gap-3">
  {#if !installed}
    <div class="flex items-start gap-2 rounded-lg bg-yellow-500/10 border border-yellow-500/30 px-3 py-2">
      <AlertTriangle class="w-4 h-4 text-warning shrink-0 mt-0.5" />
      <div class="text-xs text-text-muted">
        <p class="font-semibold text-warning">OpenCode CLI not detected on this machine</p>
        <p class="mt-0.5 font-mono">npm install -g opencode-ai</p>
      </div>
    </div>
  {/if}

  <div class="flex flex-col gap-2">
    <div class="flex flex-col sm:flex-row sm:items-center gap-1.5 sm:gap-2">
      <span class="text-xs font-semibold text-text-main sm:w-24 sm:text-right">Endpoint</span>
      <input
        type="text"
        bind:value={endpoint}
        placeholder="{localOrigin}/v1"
        class="flex-1 min-w-0 px-2 py-1.5 rounded bg-surface border border-border text-xs text-text-main focus:outline-none focus:ring-1 focus:ring-brand-500/50"
      />
    </div>

    <div class="flex flex-col sm:flex-row sm:items-center gap-1.5 sm:gap-2">
      <span class="text-xs font-semibold text-text-main sm:w-24 sm:text-right">API Key</span>
      <select
        bind:value={selectedApiKey}
        class="flex-1 min-w-0 px-2 py-1.5 rounded bg-surface border border-border text-xs text-text-main focus:outline-none focus:ring-1 focus:ring-brand-500/50"
      >
        {#if apiKeys.length === 0}
          <option value="">No key created</option>
        {/if}
        {#each apiKeys as key (key.id)}
          <option value={key.key}>{key.name || key.keyDisplay || key.key}</option>
        {/each}
      </select>
    </div>

    <div class="flex flex-col sm:flex-row sm:items-center gap-1.5 sm:gap-2">
      <span class="text-xs font-semibold text-text-main sm:w-24 sm:text-right">Model</span>
      <input
        type="text"
        bind:value={model}
        placeholder="provider/model-id"
        class="flex-1 min-w-0 px-2 py-1.5 rounded bg-surface border border-border text-xs text-text-main focus:outline-none focus:ring-1 focus:ring-brand-500/50"
      />
    </div>

    {#if configPath}
      <div class="flex items-center gap-2 text-xs text-text-muted">
        <Terminal class="w-3.5 h-3.5 shrink-0" />
        <code class="px-1.5 py-0.5 rounded bg-surface-2 font-mono text-[11px] truncate">{configPath}</code>
        {#if configured}
          <span class="shrink-0 text-[10px] font-semibold px-1.5 py-0.5 rounded bg-success/10 text-success">
            linked
          </span>
        {/if}
      </div>
    {/if}
  </div>

  {#if message}
    <div
      class="flex items-start gap-2 px-2 py-1.5 rounded text-xs {message.type === 'success'
        ? 'bg-success/10 text-success'
        : 'bg-danger/10 text-danger'}"
    >
      {#if message.type === 'success'}
        <Check class="w-3.5 h-3.5 shrink-0" />
      {:else}
        <AlertTriangle class="w-3.5 h-3.5 shrink-0" />
      {/if}
      <span class="break-words">{message.text}</span>
    </div>
  {/if}

  <div class="flex flex-wrap items-center gap-2">
    <button
      type="button"
      onclick={handleApply}
      disabled={applying || !endpoint.trim()}
      class="flex items-center gap-1.5 px-3 py-1.5 rounded-[10px] bg-brand-500 hover:bg-brand-600 text-white font-semibold text-xs disabled:opacity-50 cursor-pointer"
    >
      {#if applying}
        <Loader2 class="w-3.5 h-3.5 animate-spin" />
      {:else}
        <Save class="w-3.5 h-3.5" />
      {/if}
      Apply
    </button>
    <button
      type="button"
      onclick={handleReset}
      disabled={resetting || !configured}
      title="Remove the NOX9GO provider, leaving any other provider untouched."
      class="flex items-center gap-1.5 px-3 py-1.5 rounded-[10px] border border-border text-text-main font-semibold text-xs disabled:opacity-50 cursor-pointer"
    >
      {#if resetting}
        <Loader2 class="w-3.5 h-3.5 animate-spin" />
      {:else}
        <RotateCcw class="w-3.5 h-3.5" />
      {/if}
      Reset
    </button>
  </div>
</div>
