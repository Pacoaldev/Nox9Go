<script lang="ts">
  import { onMount } from 'svelte'
  import { api, ApiError, type TelegramChannelStatus } from '../api/client'

  // The state strings the API reports map to a label, so the banner names the
  // condition keeping the bot silent instead of showing a bare "off". The
  // literals are English: the runtime i18n layer translates the rendered text
  // from es.json, and a hardcoded Spanish string would never be keyed.
  const STATE_LABEL: Record<string, string> = {
    running: 'Channel is active',
    no_token: 'Bot token is missing',
    no_allowlist: 'No authorized users yet',
    disabled: 'Channel is disabled',
  }

  let status = $state<TelegramChannelStatus | null>(null)
  let loading = $state(true)
  let saving = $state(false)
  let error = $state('')
  let notice = $state('')

  // The token field is write-only. It is cleared after a save and never
  // populated from the API, because the API only ever returns it masked.
  let tokenInput = $state('')
  let userIdInput = $state('')

  let allowedIds = $derived(status?.allowedUserIds ?? [])
  let canSaveToken = $derived(tokenInput.trim().length > 0 && !saving)

  const tone = $derived(
    status?.state === 'running'
      ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-300'
      : 'border-amber-500/40 bg-amber-500/10 text-amber-300',
  )

  async function load() {
    loading = true
    error = ''
    try {
      status = await api.getTelegramChannel()
    } catch (e) {
      error = e instanceof ApiError ? e.message : 'Could not read the channel settings.'
    } finally {
      loading = false
    }
  }

  async function saveToken() {
    if (!canSaveToken) return
    saving = true
    error = ''
    notice = ''
    try {
      status = await api.setTelegramToken(tokenInput.trim())
      tokenInput = ''
      notice = 'Token saved.'
    } catch (e) {
      error = e instanceof ApiError ? e.message : 'Could not save the token.'
    } finally {
      saving = false
    }
  }

  async function addUser() {
    const id = Number(userIdInput.trim())
    if (!Number.isInteger(id) || id <= 0) {
      error = 'The user id must be a positive number.'
      return
    }
    if (allowedIds.includes(id)) {
      // Adding a duplicate would look like it worked while changing nothing.
      error = 'That user is already authorized.'
      return
    }
    await saveAllowlist([...allowedIds, id])
    userIdInput = ''
  }

  async function removeUser(id: number) {
    await saveAllowlist(allowedIds.filter((existing) => existing !== id))
  }

  async function saveAllowlist(ids: number[]) {
    saving = true
    error = ''
    notice = ''
    try {
      status = await api.setTelegramAllowlist(ids)
      notice = 'List updated.'
    } catch (e) {
      error = e instanceof ApiError ? e.message : 'Could not save the list.'
    } finally {
      saving = false
    }
  }

  onMount(load)
</script>

<div class="mx-auto max-w-3xl space-y-6 p-6">
  <header class="space-y-1">
    <h1 class="text-xl font-semibold text-text-main">Channels</h1>
    <p class="text-sm text-text-muted">
      Connect this gateway to Telegram. An authorized message is forwarded to the
      configured model, through the same routing and quotas as an API client.
    </p>
  </header>

  {#if error}
    <div class="rounded-[10px] border border-red-500/40 bg-red-500/10 px-4 py-3 text-sm text-red-300">
      {error}
    </div>
  {/if}

  {#if notice}
    <div class="rounded-[10px] border border-border bg-surface-2 px-4 py-3 text-sm text-text-muted">
      {notice}
    </div>
  {/if}

  {#if loading}
    <p class="text-sm text-text-muted">Loading…</p>
  {:else if status}
    <div class="rounded-[10px] border px-4 py-3 text-sm {tone}">
      <p class="font-medium">{STATE_LABEL[status.state] ?? status.state}</p>
      {#if status.state === 'no_token'}
        <p class="mt-1 opacity-80">Paste the token @BotFather gave you to start.</p>
      {:else if status.state === 'no_allowlist'}
        <p class="mt-1 opacity-80">
          The bot answers nobody until you add your user id below.
        </p>
      {:else if status.state === 'running' && status.botUsername}
        <p class="mt-1 opacity-80">@{status.botUsername}</p>
      {/if}
    </div>

    <section class="space-y-3 rounded-[14px] border border-border bg-surface p-5">
      <div>
        <h2 class="text-sm font-semibold text-text-main">Bot token</h2>
        <p class="text-xs text-text-muted">
          Stored encrypted and never readable again. If you lose it, issue a new one in
          @BotFather.
        </p>
      </div>
      {#if status.tokenPresent}
        <p class="text-xs text-text-subtle font-mono">{status.tokenMasked}</p>
      {/if}
      <div class="flex gap-2">
        <input
          type="password"
          bind:value={tokenInput}
          autocomplete="off"
          placeholder="123456:ABC-DEF…"
          class="min-w-0 flex-1 rounded-[10px] border border-border bg-surface-2 px-3 py-2 text-sm text-text-main placeholder:text-text-subtle focus:border-primary focus:outline-none"
        />
        <button
          type="button"
          onclick={saveToken}
          disabled={!canSaveToken}
          class="rounded-[10px] bg-primary px-4 py-2 text-sm font-medium text-white hover:bg-primary-hover disabled:cursor-not-allowed disabled:opacity-50"
        >
          {saving ? 'Saving…' : 'Save'}
        </button>
      </div>
    </section>

    <section class="space-y-3 rounded-[14px] border border-border bg-surface p-5">
      <div>
        <h2 class="text-sm font-semibold text-text-main">Authorized users</h2>
        <p class="text-xs text-text-muted">
          Only these user ids can talk to the bot. Everyone else gets silence: they spend
          no tokens.
        </p>
      </div>

      {#if allowedIds.length === 0}
        <p class="text-xs text-amber-300">
          The list is empty, so the bot answers nobody.
        </p>
      {:else}
        <ul class="space-y-1">
          {#each allowedIds as id (id)}
            <li class="flex items-center justify-between rounded-[10px] bg-surface-2 px-3 py-2">
              <span class="font-mono text-sm text-text-main">{id}</span>
              <button
                type="button"
                onclick={() => removeUser(id)}
                disabled={saving}
                class="text-xs text-text-muted hover:text-red-400 disabled:opacity-50"
              >
                Remove
              </button>
            </li>
          {/each}
        </ul>
      {/if}

      <div class="flex gap-2">
        <input
          type="text"
          bind:value={userIdInput}
          inputmode="numeric"
          placeholder="Your user id"
          class="min-w-0 flex-1 rounded-[10px] border border-border bg-surface-2 px-3 py-2 text-sm text-text-main placeholder:text-text-subtle focus:border-primary focus:outline-none"
        />
        <button
          type="button"
          onclick={addUser}
          disabled={saving}
          class="rounded-[10px] border border-border px-4 py-2 text-sm font-medium text-text-main hover:bg-surface-2 disabled:opacity-50"
        >
          Add
        </button>
      </div>
      <p class="text-xs text-text-subtle">
        @userinfobot in Telegram gives you your user id. It is a number, not your username.
      </p>
    </section>

    <section class="space-y-2 rounded-[14px] border border-border bg-surface p-5">
      <h2 class="text-sm font-semibold text-text-main">Model</h2>
      {#if status.model}
        <p class="font-mono text-sm text-text-main">{status.model}</p>
      {:else}
        <p class="text-xs text-amber-300">
          No model configured. Choose one from Telegram with /model.
        </p>
      {/if}
      <p class="text-xs text-text-subtle">
        Changed from the bot with /model, which lists the models this gateway can use.
      </p>
    </section>

    {#if status.lastError}
      <section class="rounded-[14px] border border-red-500/40 bg-red-500/10 p-4">
        <h2 class="text-sm font-semibold text-red-300">Last error</h2>
        <p class="mt-1 font-mono text-xs text-red-300">{status.lastError}</p>
      </section>
    {/if}
  {/if}
</div>
