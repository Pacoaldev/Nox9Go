<script lang="ts">
  import { api } from '../api/client'
  import ProviderIcon from './connections/ProviderIcon.svelte'
  import ChangelogModal from './ChangelogModal.svelte'
  import Menu from '../lib/ui/Menu.svelte'
  import MenuItem from '../lib/ui/MenuItem.svelte'
  import { type ActiveTab } from '../lib/router'
  import { promptInstall, subscribeInstallPrompt } from '../lib/pwa'
  import { type Locale } from '../lib/i18n/config'
  import { getCurrentLocale, setLocale } from '../lib/i18n/runtime'
  import { dashboardStatus } from '../lib/systemStatus.svelte'
  import TokenWidget from './TokenWidget.svelte'
  import AlertsBadge from './AlertsBadge.svelte'

  let {
    activeTab = 'endpoint',
    pageTitle,
    pageDescription,
    selectedProvider = null,
    onBackToProviders,
    onMenuClick,
    onAlertsClick,
    onLogout,
  }: {
    activeTab?: ActiveTab
    pageTitle?: string
    pageDescription?: string
    selectedProvider?: { id: string; name: string; icon: string } | null
    onBackToProviders?: () => void
    onMenuClick?: () => void
    onAlertsClick?: () => void
    onLogout?: () => void
  } = $props()

  const status = $derived(dashboardStatus())

  let uiLocale = $state<Locale>('en')

  // ─── Upstream update alert ────────────────────────────────────────────────
  // The two repositories this fork tracks. They are versioned independently, so
  // the button says which one moved and how far past the baseline this build was
  // ported from (that baseline is baked in with -ldflags, not stored here).
  type UpstreamSource = {
    id: string
    name: string
    repo: string
    baseline: string
    latest: string
    hasNew: boolean
    publishedAt?: string
    releaseUrl?: string
    error?: string
  }

  let upstreamSources = $state<UpstreamSource[]>([])
  let isUpstreamOpen = $state(false)
  // Sources the user has already opened and seen: { id -> latest version seen }.
  // Persisted so a reload does not resurface the same update notice; the button
  // stays hidden until a different, newer version is detected.
  let seenUpstream = $state<Map<string, string>>(loadSeen())
  // Snapshot of the sources whose notes the open modal is showing.
  let upstreamPending = $state<UpstreamSource[]>([])
  // Identity of those notes: the modal refetches when it changes, so a newer
  // release does not show the previous one's notes.
  let upstreamReloadKey = $derived(
    upstreamPending.map((s) => `${s.id}@${s.latest}`).join(',')
  )

  function loadSeen(): Map<string, string> {
    const map = new Map<string, string>()
    if (typeof window === 'undefined') return map
    try {
      const raw = localStorage.getItem('nox9go-upstream-seen')
      if (raw) {
        const parsed = JSON.parse(raw) as { id: string; latest: string }[]
        for (const entry of parsed) map.set(entry.id, entry.latest)
      }
    } catch {
      // ignore corrupt storage
    }
    return map
  }

  function saveSeen(): void {
    if (typeof window === 'undefined') return
    try {
      localStorage.setItem(
        'nox9go-upstream-seen',
        JSON.stringify([...seenUpstream.entries()].map(([id, latest]) => ({ id, latest })))
      )
    } catch {
      // ignore
    }
  }

  function markSeen(sources: UpstreamSource[]): void {
    // The map is re-created on every change so Svelte 5 sees it: mutating it
    // in place is not tracked.
    const next = new Map(seenUpstream)
    for (const s of sources) next.set(s.id, s.latest)
    seenUpstream = next
    saveSeen()
  }
  // One read per ten minutes at most: the gateway caches the answer for half an
  // hour, and a window focus must not become a poll. GitHub's unauthenticated
  // budget is 60 requests an hour, shared with everything else on this IP.
  let lastUpstreamCheck = 0
  const UPSTREAM_CHECK_INTERVAL_MS = 10 * 60 * 1000

  // A source counts as new until the user has actually seen that exact
  // version. Seeing it hides the button again and puts the dashboard back in
  // normal mode, waiting for a different version to show up.
  let upstreamNew = $derived(
    upstreamSources.filter((s) => s.hasNew && seenUpstream.get(s.id) !== s.latest)
  )
  // Both unreachable is worth showing as a state, not as silence.
  let upstreamAllFailed = $derived(
    upstreamSources.length > 0 && upstreamNew.length === 0 && upstreamSources.every((s) => s.error),
  )

  async function checkUpstream(force = false): Promise<void> {
    const now = Date.now()
    if (!force && now - lastUpstreamCheck < UPSTREAM_CHECK_INTERVAL_MS) return
    lastUpstreamCheck = now
    try {
      const res = await api.getUpstreamVersions()
      upstreamSources = res?.sources ?? []
    } catch {
      // A failure here is not worth a toast: the button simply does not appear,
      // and the next focus retries. The page itself is unaffected.
    }
  }

  // The notes of every repository that has something new, each under its own
  // heading, so one press covers the case where both moved.
  // Reads the snapshot taken when the alert was opened, not the live list: the
  // press also marks that version as seen, which empties the live list.
  async function loadUpstreamNotes(): Promise<string> {
    const parts: string[] = []
    for (const source of upstreamPending) {
      const notes = await api.getUpstreamChangelog(source.id, source.latest)
      const link = source.releaseUrl ? ` — [${source.repo}](${source.releaseUrl})` : ` — ${source.repo}`
      parts.push(`## ${source.name} · ${source.latest}${link}\n\n${notes}`)
    }
    return parts.join('\n\n---\n\n')
  }

  // Theme state
  let isDark = $state(true)
  let isDonateOpen = $state(false)
  let isChangelogOpen = $state(false)
  let canInstall = $state(false)

  $effect(() => {
    return subscribeInstallPrompt((available) => {
      canInstall = available
    })
  })
  $effect(() => {
    if (typeof window !== 'undefined') {
      const stored = localStorage.getItem('nox9go-theme') || localStorage.getItem('theme')
      if (stored === 'light') {
        isDark = false
      } else if (stored === 'dark') {
        isDark = true
      } else {
        isDark = document.documentElement.classList.contains('dark')
      }
      applyTheme(isDark)
      uiLocale = getCurrentLocale()
    }
  })

  // The alert is read once when the dashboard mounts and again when the operator
  // comes back to the tab — the moment they would act on it. No timer: the
  // gateway caches the answer for half an hour and a poll would buy nothing.
  $effect(() => {
    void checkUpstream()
    if (typeof document === 'undefined') return
    const onVisible = () => {
      if (!document.hidden) void checkUpstream()
    }
    document.addEventListener('visibilitychange', onVisible)
    return () => document.removeEventListener('visibilitychange', onVisible)
  })

  async function chooseLocale(next: Locale) {
    if (next === uiLocale) return
    await setLocale(next)
    uiLocale = next
  }

  function applyTheme(dark: boolean) {
    document.documentElement.classList.toggle('dark', dark)
    document.documentElement.classList.toggle('light', !dark)
    localStorage.setItem('nox9go-theme', dark ? 'dark' : 'light')
    localStorage.setItem('theme', dark ? 'dark' : 'light')
  }

  function toggleTheme() {
    isDark = !isDark
    applyTheme(isDark)
  }

  async function handleLogout() {
    try {
      await api.logout()
    } catch {}
    if (onLogout) {
      onLogout()
    } else {
      window.location.assign('/login')
    }
  }

  // Dynamic route meta matching upstream layout
  interface RouteMeta {
    title: string
    description: string
    icon: string
  }

  const routeMetaMap: Record<string, RouteMeta> = {
    endpoint: {
      title: 'Endpoint',
      description: 'API endpoint configuration',
      icon: 'api',
    },
    keys: {
      title: 'CLI Tools',
      description: 'Configure CLI tools',
      icon: 'terminal',
    },
    connections: {
      title: 'Providers',
      description: 'Manage your AI provider connections',
      icon: 'dns',
    },
    combos: {
      title: 'Combos',
      description: 'Model combos with fallback',
      icon: 'layers',
    },
    'combo-flow': {
      title: 'Combo flow',
      description: 'Router → combo → members, live',
      icon: 'account_tree',
    },
    analytics: {
      title: 'Usage & Analytics',
      description: 'Monitor your API usage, token consumption, and request logs',
      icon: 'bar_chart',
    },
    quota: {
      title: 'Quota Tracker',
      description: 'Track and manage your API quota limits',
      icon: 'data_usage',
    },
    'token-saver': {
      title: 'Token Saver',
      description: 'Compress prompts and outputs to save tokens',
      icon: 'savings',
    },
    'cli-tools': {
      title: 'CLI Tools',
      description: 'Configure CLI tools',
      icon: 'terminal',
    },
    'media-embedding': {
      title: 'Embedding',
      description: 'Manage your Embedding providers',
      icon: 'data_array',
    },
    'media-image': {
      title: 'Text to Image',
      description: 'Manage your Text to Image providers',
      icon: 'brush',
    },
    'media-tts': {
      title: 'Text To Speech',
      description: 'Manage your Text To Speech providers',
      icon: 'record_voice_over',
    },
    'media-stt': {
      title: 'Speech To Text',
      description: 'Manage your Speech To Text providers',
      icon: 'mic',
    },
    'media-video': {
      title: 'Video',
      description: 'Manage your Video providers',
      icon: 'movie',
    },
    'media-web': {
      title: 'Web Fetch & Search',
      description: 'Configure web search and scrape tools',
      icon: 'travel_explore',
    },
    'proxy-pools': {
      title: 'Proxy Pools',
      description: 'Manage your proxy pool configurations',
      icon: 'lan',
    },
    skills: {
      title: 'Agent Skills',
      description: 'Copy a link and paste to your AI to use NOX9GO — no install needed',
      icon: 'extension',
    },
    'console-log': {
      title: 'Console Log',
      description: 'Live server console output',
      icon: 'terminal',
    },
    terminal: {
      title: 'Console Log',
      description: 'Live server console output',
      icon: 'terminal',
    },
    settings: {
      title: 'Settings',
      description: 'Manage your preferences',
      icon: 'settings',
    },
    security: {
      title: 'Security',
      description: 'Encrypt provider credentials at rest and scan prompts before dispatch',
      icon: 'shield_lock',
    },
    plans: {
      title: 'Plans',
      description: 'Per-key quota plans and policy resolution',
      icon: 'contract',
    },
    login: {
      title: 'Login',
      description: 'Authentication',
      icon: 'lock',
    },
  }

  let currentMeta = $derived(
    routeMetaMap[activeTab] || {
      title: pageTitle || 'Dashboard',
      description: pageDescription || '',
      icon: 'hub',
    }
  )

  let displayTitle = $derived(pageTitle || currentMeta.title)
  let displayDescription = $derived(
    pageDescription !== undefined ? pageDescription : currentMeta.description
  )
</script>

<header
  class="h-16 bg-vibrancy backdrop-blur-xl border-b border-border-subtle px-4 lg:px-8 flex items-center justify-between gap-4 flex-shrink-0 z-20 transition-colors"
>
  <!-- Left: Mobile menu toggle + Dynamic Title & Description -->
  <div class="flex items-center gap-3 min-w-0 flex-1">
    {#if onMenuClick}
      <button
        type="button"
        onclick={onMenuClick}
        class="lg:hidden p-1.5 rounded-lg text-text-muted hover:text-text-main hover:bg-surface-2 transition-colors cursor-pointer"
        aria-label="Toggle menu"
      >
        <span class="material-symbols-outlined text-[22px]">menu</span>
      </button>
    {/if}

    {#if activeTab === 'connections' && selectedProvider}
      <div class="flex items-center gap-2 min-w-0">
        <div class="flex items-center gap-2">
          <button
            type="button"
            onclick={onBackToProviders}
            class="text-text-muted hover:text-primary transition-colors cursor-pointer text-sm font-normal"
          >
            Providers
          </button>
        </div>
        <div class="flex items-center gap-2 min-w-0">
          <span class="material-symbols-outlined text-text-muted text-base">chevron_right</span>
          <div class="flex items-center gap-2 min-w-0">
            <ProviderIcon id={selectedProvider.id} size="sm" />
            <h1 class="text-base lg:text-2xl font-semibold text-text-main tracking-tight truncate">
              {selectedProvider.name}
            </h1>
          </div>
        </div>
      </div>
    {:else}
      <div class="flex items-center gap-2.5 min-w-0">
        <span class="material-symbols-outlined text-primary text-xl lg:text-2xl flex-shrink-0">
          {currentMeta.icon}
        </span>
        <div class="min-w-0">
          <h1 class="text-base lg:text-lg font-semibold text-text-main truncate leading-tight tracking-tight">
            {displayTitle}
          </h1>
          {#if displayDescription}
            <p class="hidden sm:block text-xs text-text-muted truncate leading-tight mt-0.5">
              {displayDescription}
            </p>
          {/if}
        </div>
      </div>
    {/if}
  </div>

  <!-- Right: one menu (issue #224). Language is back with a real handler:
       only English + Español (España), via the runtime DOM translator. -->
  <div class="flex items-center gap-2 shrink-0">
    <!-- Latency chip (Idea 1) -->
    {#if status.latencyMs !== null}
      <div
        class="hidden md:inline-flex items-center gap-1 px-2 py-0.5 rounded-full border border-border-subtle bg-surface-2 text-[11px] font-mono text-text-muted"
        title="Latencia al gateway local"
      >
        <span class="size-1.5 rounded-full bg-emerald-500"></span>
        <span>{status.latencyMs} ms</span>
      </div>
    {/if}

    <!-- Upstream / fork update alert: between the latency meter and "Uso de hoy",
         so it reads as one more live signal about this install. -->
    {#if upstreamNew.length > 0}
      <button
        type="button"
        onclick={() => {
          upstreamPending = [...upstreamNew]
          markSeen(upstreamNew)
          isUpstreamOpen = true
        }}
        class="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full border border-primary/40 bg-primary/10 text-[11px] font-medium text-primary hover:bg-primary/20 transition-colors cursor-pointer"
        title={upstreamNew
          .map((s) => `${s.name}: ${s.baseline || '?'} → ${s.latest}`)
          .join('\n')}
      >
        <span class="material-symbols-outlined text-[14px]">new_releases</span>
        <span class="hidden sm:inline">{upstreamNew.length === 1
          ? upstreamNew[0].name
          : `${upstreamNew.length} updates`}</span>
        <span class="font-mono">{upstreamNew.length === 1 ? upstreamNew[0].latest : ''}</span>
      </button>
    {:else if upstreamAllFailed}
      <div
        class="hidden md:inline-flex items-center gap-1 px-2 py-0.5 rounded-full border border-border-subtle bg-surface-2 text-[11px] text-text-subtle"
        title={upstreamSources.map((s) => `${s.name}: ${s.error}`).join('\n')}
      >
        <span class="material-symbols-outlined text-[14px]">cloud_off</span>
        <span>upstream unreachable</span>
      </div>
    {/if}

    <!-- Token savings / usage today (Idea 6) -->
    <TokenWidget />

    <!-- Alerts bell (Idea 7) -->
    <AlertsBadge onClick={onAlertsClick} />

    <Menu label="Account and display options" triggerIcon="account_circle" minWidth="15rem">
      <MenuItem label="Donate" icon="volunteer_activism" onSelect={() => (isDonateOpen = true)} />

      {#if canInstall}
        <MenuItem label="Install App" icon="install_desktop" onSelect={promptInstall} />
      {/if}

      <div class="my-1 border-t border-border-subtle" role="separator"></div>

      <MenuItem
        label={isDark ? 'Switch to light mode' : 'Switch to dark mode'}
        icon={isDark ? 'light_mode' : 'dark_mode'}
        onSelect={toggleTheme}
      />
      <!-- Static labels so the DOM translator can match es.json keys exactly. -->
      <MenuItem
        label={uiLocale === 'en' ? 'Language: English ✓' : 'Language: English'}
        icon="translate"
        onSelect={() => chooseLocale('en')}
      />
      <MenuItem
        label={uiLocale === 'es' ? 'Language: Español (España) ✓' : 'Language: Español (España)'}
        icon="translate"
        onSelect={() => chooseLocale('es')}
      />
      <MenuItem label="Change Log" icon="history" onSelect={() => (isChangelogOpen = true)} />

      <div class="my-1 border-t border-border-subtle" role="separator"></div>

      <MenuItem label="Logout" icon="logout" danger onSelect={handleLogout} />
    </Menu>
  </div>
</header>

<!-- Escape closes the modal from anywhere on the page. The handler used to sit
     on the backdrop, which never receives focus, so it never fired; a
     <svelte:window> binding is the only place it actually runs. -->
<svelte:window onkeydown={(e) => e.key === 'Escape' && (isDonateOpen = false)} />

<!-- Donate Modal -->
{#if isDonateOpen}
  <div class="fixed inset-0 z-50 flex items-center justify-center p-4">
    <div
      class="absolute inset-0 bg-black/40 backdrop-blur-sm"
      onclick={() => (isDonateOpen = false)}
      role="button"
      tabindex="-1"
      aria-label="Close background"
    ></div>

    <div
      class="relative w-full max-w-lg bg-surface border border-border-subtle rounded-2xl shadow-2xl p-6 flex flex-col gap-5 z-10 animate-in fade-in zoom-in-95"
    >
      <div class="flex items-center justify-between pb-3 border-b border-border-subtle">
        <h2 class="text-lg font-semibold text-text-main flex items-center gap-2">
          <span class="material-symbols-outlined text-pink-500">volunteer_activism</span>
          Support NOX9GO
        </h2>
        <button
          type="button"
          onclick={() => (isDonateOpen = false)}
          class="p-1 rounded-lg text-text-muted hover:text-text-main hover:bg-surface-2 transition-colors cursor-pointer"
          aria-label="Close"
        >
          <span class="material-symbols-outlined text-[20px]">close</span>
        </button>
      </div>

      <p class="text-sm text-text-muted leading-relaxed">
        NOX9GO is a fast, lightweight and open-source high-throughput AI gateway in Go. If NOX9GO saves you time and tokens, consider supporting the project!
      </p>

      <!-- Three destinations, stacked rather than tiled: a 2-column grid leaves
           the third card alone on its own row, and a full-width row per link
           reads as a list, which is what these are. -->
      <div class="flex flex-col gap-2.5">
        <a
          href="https://github.com/Pacoaldev/Nox9Go"
          target="_blank"
          rel="noopener noreferrer"
          class="flex items-center gap-3 p-3.5 rounded-xl border border-border-subtle bg-surface-2 hover:border-brand-500/40 transition-colors group"
        >
          <div class="size-10 rounded-full flex items-center justify-center bg-brand-500/10 text-brand-500">
            <span class="material-symbols-outlined text-[22px]">star</span>
          </div>
          <div class="min-w-0">
            <div class="text-sm font-semibold text-text-main group-hover:text-brand-500 transition-colors">
              GitHub Repository
            </div>
            <div class="text-xs text-text-muted">Star & contribute on GitHub</div>
          </div>
        </a>

        <a
          href="https://github.com/Pacoaldev/Nox9Go/releases"
          target="_blank"
          rel="noopener noreferrer"
          class="flex items-center gap-3 p-3.5 rounded-xl border border-border-subtle bg-surface-2 hover:border-pink-500/40 transition-colors group"
        >
          <div class="size-10 rounded-full flex items-center justify-center bg-pink-500/10 text-pink-500">
            <span class="material-symbols-outlined text-[22px]">rocket_launch</span>
          </div>
          <div class="min-w-0">
            <div class="text-sm font-semibold text-text-main group-hover:text-pink-500 transition-colors">
              Releases & Updates
            </div>
            <div class="text-xs text-text-muted">Latest releases & changelog</div>
          </div>
        </a>

        <a
          href="https://ko-fi.com/H3A029MD9M"
          target="_blank"
          rel="noopener noreferrer"
          class="flex items-center gap-3 p-3.5 rounded-xl border border-border-subtle bg-surface-2 hover:border-[#ff5e5b]/50 transition-colors group"
        >
          <div class="size-10 rounded-full flex items-center justify-center bg-[#ff5e5b]/10 text-[#ff5e5b]">
            <span class="material-symbols-outlined text-[22px]">coffee</span>
          </div>
          <div class="min-w-0">
            <div class="text-sm font-semibold text-text-main group-hover:text-[#ff5e5b] transition-colors">
              Buy me a coffee
            </div>
            <div class="text-xs text-text-muted">Support NOX9GO on Ko-fi</div>
          </div>
        </a>
      </div>

      <div class="flex justify-end pt-2">
        <button
          type="button"
          onclick={() => (isDonateOpen = false)}
          class="px-4 py-2 text-sm rounded-lg bg-surface-2 hover:bg-surface-3 text-text-main font-medium transition-colors cursor-pointer"
        >
          Close
        </button>
      </div>
    </div>
  </div>
{/if}

<!-- Change Log Modal -->
<ChangelogModal isOpen={isChangelogOpen} onClose={() => (isChangelogOpen = false)} />

<!-- Same modal, different source: the notes of whichever tracked repository has
     moved past this build's baseline. No repoUrl, because there are two repos
     and the assembled markdown names each one. The key changes with the
     versions on offer, so a newer release refetches its notes. -->
<ChangelogModal
  isOpen={isUpstreamOpen}
  onClose={() => (isUpstreamOpen = false)}
  title="Upstream changes"
  repoUrl=""
  load={loadUpstreamNotes}
  reloadKey={upstreamReloadKey}
/>
