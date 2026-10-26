<script lang="ts">
  import { api } from '../api/client'
  import { marked } from 'marked'

  let {
    isOpen = false,
    onClose = () => {},
    title = 'Change Log',
    repoUrl = 'https://github.com/Pacoaldev/Nox9Go',
    load,
    reloadKey = '',
  }: {
    isOpen?: boolean
    onClose?: () => void
    /** Header title. Defaults to this fork's own change log. */
    title?: string
    /** Base GitHub URL for the two outbound links; hidden when empty. */
    repoUrl?: string
    /**
     * Where the markdown comes from. Defaults to this fork's own change log, so
     * the existing caller is unchanged; the upstream-alert caller passes a loader
     * that concatenates the notes of every repository that has something new.
     */
    load?: () => Promise<string>
    /**
     * Identity of the content being shown. When it changes, the cached markdown
     * is discarded and the notes are fetched again, so a newer version does not
     * show the previous release's notes.
     */
    reloadKey?: string
  } = $props()

  let html = $state('')
  let loading = $state(false)
  let error = $state('')

  marked.setOptions({ gfm: true, breaks: true })

  async function fetchChangelog() {
    loading = true
    error = ''
    try {
      const md = load ? await load() : await api.getChangelog()
      html = await marked.parse(md)
    } catch (err: any) {
      error = err?.message || 'Failed to load change log'
    } finally {
      loading = false
    }
  }

  $effect(() => {
    if (isOpen && !html && !loading) {
      fetchChangelog()
    }
  })

  // A new content identity invalidates what was loaded: the modal is reused
  // across opens, and the previous release's notes are not the current ones.
  // A plain (non-reactive) tracker so the effect does not depend on what it
  // writes.
  let loadedKey = ''
  $effect(() => {
    if (!isOpen) return
    if (reloadKey && loadedKey !== reloadKey) {
      loadedKey = reloadKey
      html = ''
      error = ''
    }
  })

  // Close on Escape key
  $effect(() => {
    if (!isOpen) return
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  })

  // Lock body scroll while modal is open
  $effect(() => {
    if (!isOpen) return
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = prev
    }
  })
</script>

{#if isOpen}
  <div class="fixed inset-0 z-50 flex items-center justify-center p-4">
    <!-- Backdrop -->
    <div
      class="absolute inset-0 bg-black/50 backdrop-blur-sm"
      onclick={onClose}
      onkeydown={(e) => e.key === 'Escape' && onClose()}
      role="button"
      tabindex="-1"
      aria-label="Close background"
    ></div>

    <!-- Modal Card -->
    <div
      class="relative w-full max-w-3xl bg-surface border border-border-subtle rounded-2xl shadow-2xl flex flex-col max-h-[85vh] z-10 animate-in fade-in zoom-in-95 duration-150 overflow-hidden"
    >
      <!-- Header -->
      <div class="flex items-center justify-between px-6 py-4 border-b border-border-subtle shrink-0">
        <div class="flex items-center gap-2.5">
          <span class="material-symbols-outlined text-primary text-[22px]">history</span>
          <h2 class="text-lg font-semibold text-text-main tracking-tight">{title}</h2>
        </div>
        <div class="flex items-center gap-2">
          {#if repoUrl}
            <a
              href="{repoUrl}/releases"
              target="_blank"
              rel="noopener noreferrer"
              class="hidden sm:inline-flex items-center gap-1.5 px-3 py-1 rounded-lg text-xs font-medium text-text-muted hover:text-text-main hover:bg-surface-2 border border-border-subtle transition-colors"
            >
              <span class="material-symbols-outlined text-[16px]">open_in_new</span>
              GitHub Releases
            </a>
          {/if}
          <button
            type="button"
          onclick={onClose}
          class="p-1.5 rounded-lg text-text-muted hover:text-text-main hover:bg-surface-2 transition-colors cursor-pointer"
          aria-label="Close"
          >
            <span class="material-symbols-outlined text-[20px]">close</span>
          </button>
        </div>
      </div>

      <!-- Body -->
      <div class="p-6 overflow-y-auto flex-1 custom-scrollbar">
        {#if loading}
          <div class="flex flex-col items-center justify-center py-16 text-text-muted gap-3">
            <span class="material-symbols-outlined animate-spin text-[32px] text-primary">
              progress_activity
            </span>
            <p class="text-sm font-medium">Loading change log...</p>
          </div>
        {:else if error}
          <div class="flex flex-col items-center justify-center py-12 text-center gap-4">
            <div class="w-12 h-12 rounded-full bg-red-500/10 text-red-500 flex items-center justify-center">
              <span class="material-symbols-outlined text-[24px]">error</span>
            </div>
            <div class="max-w-md">
              <p class="text-sm font-semibold text-text-main">Failed to load change log</p>
              <p class="text-xs text-text-muted mt-1">{error}</p>
            </div>
            <div class="flex items-center gap-3">
              <button
                type="button"
                onclick={fetchChangelog}
                class="px-4 py-2 text-xs font-semibold rounded-lg bg-primary hover:bg-primary-hover text-white transition-colors cursor-pointer"
              >
                Retry
              </button>
              {#if repoUrl}
                <a
                  href="{repoUrl}/blob/main/CHANGELOG.md"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="px-4 py-2 text-xs font-medium rounded-lg border border-border-subtle text-text-main hover:bg-surface-2 transition-colors"
                >
                  View on GitHub
                </a>
              {/if}
            </div>
          </div>
        {:else if html}
          <div class="changelog-body text-text-main">
            <!-- eslint-disable-next-line svelte/no-at-html-tags -->
            {@html html}
          </div>
        {/if}
      </div>

      <!-- Footer -->
      <div class="flex items-center justify-between px-6 py-3.5 border-t border-border-subtle bg-surface shrink-0">
        {#if repoUrl}
          <a
            href="{repoUrl}/blob/main/CHANGELOG.md"
            target="_blank"
            rel="noopener noreferrer"
            class="text-xs text-text-muted hover:text-primary transition-colors flex items-center gap-1"
          >
            <span>View on GitHub</span>
            <span class="material-symbols-outlined text-[14px]">arrow_outward</span>
          </a>
        {:else}
          <span></span>
        {/if}
        <button
          type="button"
          onclick={onClose}
          class="px-4 py-1.5 text-xs font-medium rounded-lg bg-surface-2 hover:bg-surface-3 text-text-main transition-colors cursor-pointer border border-border-subtle"
        >
          Close
        </button>
      </div>
    </div>
  </div>
{/if}
