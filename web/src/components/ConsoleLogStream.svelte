<script lang="ts">
  // Renders the console buffer: filter by level and subsystem tag, search with
  // literal or regex matching (case toggle), pivot on a request id, show context
  // around matches, and read the rows. The server stamps each row's level
  // (internal/log.ConsoleEntry), so colour follows the emitted severity — never
  // a guess made from the message text.
  import { ArrowDown, Copy, Download, DownloadCloud, Regex, Search, X } from 'lucide-svelte'
  import Button from '../lib/ui/Button.svelte'
  import { notifications } from '../lib/notifications'
  import {
    collectTags,
    compileSearch,
    countByLevel,
    downloadFilename,
    entryTag,
    extractReqId,
    formatClock,
    formatRelative,
    highlightParts,
    splitTag,
    toPlainText,
    type ConsoleEntry,
    type LogLevel,
  } from '../lib/consoleLog'

  interface Props {
    entries: ConsoleEntry[]
    /** Lines the viewer knows it missed (gaps in the server sequence). */
    dropped?: number
    /** Earliest buffered line number; > 1 means the ring trimmed older lines. */
    older?: number
    /** Optional export-all hook; falls back to exporting the whole buffer. */
    onExportAll?: () => void
  }

  let { entries, dropped = 0, older = 0, onExportAll }: Props = $props()

  // Paired light/dark values: each pair is checked against the panel surface
  // for 4.5:1 contrast, so a level stays legible in both themes.
  const LEVEL_META: Record<LogLevel, { label: string; text: string; chip: string; dot: string }> = {
    error: {
      label: 'ERR',
      text: 'text-red-700 dark:text-red-400',
      chip: 'border-red-500/40 bg-red-500/10 text-red-700 dark:text-red-400',
      dot: 'bg-red-500',
    },
    warn: {
      label: 'WRN',
      text: 'text-amber-800 dark:text-amber-400',
      chip: 'border-amber-500/40 bg-amber-500/10 text-amber-800 dark:text-amber-400',
      dot: 'bg-amber-500',
    },
    info: {
      label: 'INF',
      text: 'text-emerald-700 dark:text-emerald-400',
      chip: 'border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400',
      dot: 'bg-emerald-500',
    },
    debug: {
      label: 'DBG',
      text: 'text-sky-800 dark:text-sky-300',
      chip: 'border-sky-500/40 bg-sky-500/10 text-sky-800 dark:text-sky-300',
      dot: 'bg-sky-500',
    },
  }

  const LEVEL_ORDER: LogLevel[] = ['error', 'warn', 'info', 'debug']
  const CONTEXT_LINES = 3
  const MAX_TAG_CHIPS = 8

  type TimeMode = 'clock' | 'relative' | 'off'

  let query = $state('')
  let caseSensitive = $state(false)
  let useRegex = $state(false)
  let wrap = $state(false)
  let following = $state(true)
  let timeMode = $state<TimeMode>('clock')
  let contextOn = $state(false)
  let mutedLevels = $state<Set<LogLevel>>(new Set())
  let activeTag = $state('')
  let viewport = $state<HTMLDivElement | null>(null)

  const counts = $derived(countByLevel(entries))
  const matcher = $derived(compileSearch({ query, caseSensitive, regex: useRegex }))
  const tags = $derived(collectTags(entries).slice(0, MAX_TAG_CHIPS))
  const filtersActive = $derived(mutedLevels.size > 0 || activeTag !== '' || query.trim() !== '')

  // Row selection is computed over the full buffer as indices, so "context"
  // can pull neighbours that the level/tag filters would otherwise hide.
  const view = $derived.by(() => {
    const matched: number[] = []
    for (let i = 0; i < entries.length; i++) {
      const e = entries[i]
      if (mutedLevels.has(e.level)) continue
      if (activeTag && entryTag(e.line) !== activeTag) continue
      if (!matcher.test(e.line)) continue
      matched.push(i)
    }

    if (!contextOn || !filtersActive) {
      return {
        rows: matched.map((i) => entries[i]),
        contextSeqs: new Set<number>(),
      }
    }

    const matchedSet = new Set(matched)
    const shown = new Set<number>()
    for (const i of matched) {
      for (let j = Math.max(0, i - CONTEXT_LINES); j <= Math.min(entries.length - 1, i + CONTEXT_LINES); j++) {
        shown.add(j)
      }
    }
    const indices = [...shown].sort((a, b) => a - b)
    const contextSeqs = new Set<number>()
    for (const i of indices) {
      if (!matchedSet.has(i)) contextSeqs.add(entries[i].seq)
    }
    return { rows: indices.map((i) => entries[i]), contextSeqs }
  })

  const visible = $derived(view.rows)
  const baseMs = $derived(visible.length > 0 ? Date.parse(visible[0].time) : 0)

  function toggleLevel(level: LogLevel) {
    const next = new Set(mutedLevels)
    if (next.has(level)) next.delete(level)
    else next.add(level)
    mutedLevels = next
  }

  function toggleTag(tag: string) {
    activeTag = activeTag === tag ? '' : tag
  }

  function pivotRequestId(id: string) {
    // A request id is matched literally, so regex/case do not interfere.
    useRegex = false
    caseSensitive = false
    query = id
  }

  function clearFilters() {
    query = ''
    useRegex = false
    caseSensitive = false
    activeTag = ''
    mutedLevels = new Set()
  }

  function scrollToLatest() {
    if (viewport) viewport.scrollTop = viewport.scrollHeight
  }

  // Scrolling up is how an operator pins the line they are reading; yanking
  // the view back down mid-inspection loses it.
  function handleScroll() {
    if (!viewport) return
    following = viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight < 24
  }

  async function copyVisible() {
    try {
      await navigator.clipboard.writeText(toPlainText(visible))
      notifications.success(`Copied ${visible.length} log ${visible.length === 1 ? 'line' : 'lines'}`)
    } catch {
      notifications.error('Clipboard is unavailable in this browser')
    }
  }

  function saveBlob(text: string, label: string) {
    const blob = new Blob([text], { type: 'text/plain;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = downloadFilename()
    link.click()
    URL.revokeObjectURL(url)
    notifications.success(`Downloaded ${label}`)
  }

  function downloadVisible() {
    const count = visible.length
    saveBlob(toPlainText(visible), `${count} log ${count === 1 ? 'line' : 'lines'}`)
  }

  // Export all ignores the current filter, so a saved log is complete even
  // when the screen is narrowed to one subsystem.
  function downloadAll() {
    if (onExportAll) {
      onExportAll()
      return
    }
    const count = entries.length
    saveBlob(toPlainText(entries), `all ${count} buffered ${count === 1 ? 'line' : 'lines'}`)
  }

  function handleSearchKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape' && query) {
      query = ''
      e.preventDefault()
    }
  }

  // ----- Deep link: the filter lives in the URL so a view can be shared or
  // survive a reload. pathToTab strips the query string, so this never
  // interferes with tab routing.
  const DEEP_LINK_KEYS = ['q', 'case', 're', 'tag', 'time', 'wrap', 'mute', 'ctx']

  function applyDeepLink() {
    if (typeof window === 'undefined') return
    const params = new URLSearchParams(window.location.search)
    if (!DEEP_LINK_KEYS.some((k) => params.has(k))) return
    query = params.get('q') ?? ''
    caseSensitive = params.get('case') === '1'
    useRegex = params.get('re') === '1'
    activeTag = params.get('tag') ?? ''
    wrap = params.get('wrap') === '1'
    contextOn = params.get('ctx') === '1'
    const time = params.get('time')
    if (time === 'clock' || time === 'relative' || time === 'off') timeMode = time
    const mute = params.get('mute')
    if (mute) {
      mutedLevels = new Set(mute.split(',').filter(Boolean) as LogLevel[])
    }
  }
  applyDeepLink()

  let deepLinkTimer: ReturnType<typeof setTimeout> | undefined
  $effect(() => {
    // Track every value the URL carries.
    const params = new URLSearchParams()
    if (query) params.set('q', query)
    if (caseSensitive) params.set('case', '1')
    if (useRegex) params.set('re', '1')
    if (activeTag) params.set('tag', activeTag)
    if (timeMode !== 'clock') params.set('time', timeMode)
    if (wrap) params.set('wrap', '1')
    if (contextOn) params.set('ctx', '1')
    if (mutedLevels.size > 0) params.set('mute', [...mutedLevels].join(','))

    if (typeof window === 'undefined') return
    clearTimeout(deepLinkTimer)
    deepLinkTimer = setTimeout(() => {
      const search = params.toString()
      const next = window.location.pathname + (search ? `?${search}` : '')
      if (window.location.pathname + window.location.search !== next) {
        window.history.replaceState(window.history.state, '', next)
      }
    }, 300)
    return () => clearTimeout(deepLinkTimer)
  })

  $effect(() => {
    void entries.length
    if (following && viewport) viewport.scrollTop = viewport.scrollHeight
  })
</script>

<div class="flex flex-col">
  <div class="flex flex-col gap-2 border-b border-border-subtle px-4 py-2.5">
    <!-- Row 1: level chips (they double as filters) and the search box. -->
    <div class="flex flex-wrap items-center gap-2">
      <div class="flex flex-wrap items-center gap-1.5">
        {#each LEVEL_ORDER as level (level)}
          {@const meta = LEVEL_META[level]}
          {@const muted = mutedLevels.has(level)}
          <button
            type="button"
            aria-pressed={!muted}
            title={muted ? `Show ${meta.label} lines` : `Hide ${meta.label} lines`}
            onclick={() => toggleLevel(level)}
            class="inline-flex h-6 items-center gap-1.5 rounded-full border px-2 text-[11px] font-semibold transition-colors duration-150 {meta.chip} {muted
              ? 'opacity-40'
              : ''}"
          >
            <span class="h-1.5 w-1.5 rounded-full {meta.dot}"></span>
            {meta.label}
            <span class="tabular-nums font-normal opacity-80">{counts[level]}</span>
          </button>
        {/each}
      </div>

      <div class="relative ml-auto w-full sm:w-64">
        <Search class="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-text-muted" />
        <input
          type="search"
          bind:value={query}
          onkeydown={handleSearchKeydown}
          placeholder={useRegex ? 'Search pattern…' : 'Search lines…'}
          aria-label="Search console log lines"
          aria-invalid={matcher.invalid}
          class="w-full rounded-[8px] border bg-surface py-1.5 pl-8 pr-7 text-xs text-text-main placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-brand-500/30 {matcher.invalid
            ? 'border-red-500'
            : 'border-border focus:border-brand-500'}"
        />
        {#if query}
          <button
            type="button"
            aria-label="Clear search"
            onclick={() => (query = '')}
            class="absolute right-2 top-1/2 -translate-y-1/2 cursor-pointer text-text-muted hover:text-text-main"
          >
            <X class="h-3.5 w-3.5" />
          </button>
        {/if}
      </div>

      <button
        type="button"
        aria-pressed={caseSensitive}
        title={caseSensitive ? 'Matching case exactly' : 'Matching any case'}
        onclick={() => (caseSensitive = !caseSensitive)}
        class="h-7 w-7 cursor-pointer rounded-[8px] border text-[11px] font-semibold text-text-main transition-colors duration-150 hover:bg-surface-2 {caseSensitive
          ? 'border-brand-500/40 bg-brand-500/10 text-brand-600 dark:text-brand-400'
          : 'border-border'}"
      >
        Aa
      </button>
      <button
        type="button"
        aria-pressed={useRegex}
        title={useRegex ? 'Treating the query as a regular expression' : 'Treating the query as literal text'}
        onclick={() => (useRegex = !useRegex)}
        class="inline-flex h-7 cursor-pointer items-center gap-1 rounded-[8px] border px-2 text-[11px] text-text-main transition-colors duration-150 hover:bg-surface-2 {useRegex
          ? 'border-brand-500/40 bg-brand-500/10 text-brand-600 dark:text-brand-400'
          : 'border-border'}"
      >
        <Regex class="h-3.5 w-3.5" />
        Regex
      </button>
    </div>

    {#if matcher.invalid}
      <p class="text-[11px] text-red-600 dark:text-red-400">Invalid regular expression — no lines are filtered.</p>
    {/if}

    <!-- Row 2: subsystem tag chips and view controls. -->
    <div class="flex flex-wrap items-center gap-1.5">
      {#if tags.length > 0}
        <span class="mr-0.5 text-[11px] text-text-muted">Subsystem</span>
        {#each tags as tag (tag.tag)}
          <button
            type="button"
            aria-pressed={activeTag === tag.tag}
            title={activeTag === tag.tag ? `Stop filtering by ${tag.tag}` : `Show only [${tag.tag}]`}
            onclick={() => toggleTag(tag.tag)}
            class="inline-flex h-6 items-center gap-1 rounded-full border px-2 font-mono text-[11px] transition-colors duration-150 {activeTag ===
            tag.tag
              ? 'border-brand-500/40 bg-brand-500/10 text-brand-600 dark:text-brand-400'
              : 'border-border text-text-muted hover:bg-surface-2'}"
          >
            {tag.tag}
            <span class="tabular-nums opacity-70">{tag.count}</span>
          </button>
        {/each}
        <span class="mx-1 h-4 w-px bg-border-subtle"></span>
      {/if}

      <button
        type="button"
        aria-pressed={wrap}
        title={wrap ? 'Disable line wrap' : 'Wrap long lines'}
        onclick={() => (wrap = !wrap)}
        class="h-7 cursor-pointer rounded-[8px] border border-border px-2.5 text-xs text-text-main transition-colors duration-150 hover:bg-surface-2 {wrap
          ? 'border-brand-500/40 bg-brand-500/10 text-brand-600 dark:text-brand-400'
          : ''}"
      >
        Wrap
      </button>

      <div class="inline-flex overflow-hidden rounded-[8px] border border-border" role="group" aria-label="Timestamp mode">
        {#each [['clock', 'Clock'], ['relative', 'Relative'], ['off', 'Hide']] as [mode, label] (mode)}
          <button
            type="button"
            aria-pressed={timeMode === mode}
            title={`Show times as ${label.toLowerCase()}`}
            onclick={() => (timeMode = mode as TimeMode)}
            class="h-7 cursor-pointer px-2 text-[11px] text-text-main transition-colors duration-150 {timeMode ===
            mode
              ? 'bg-brand-500/10 text-brand-600 dark:text-brand-400'
              : 'hover:bg-surface-2'}"
          >
            {label}
          </button>
        {/each}
      </div>

      <button
        type="button"
        aria-pressed={contextOn}
        title="Also show the lines around each match (dimmed)"
        onclick={() => (contextOn = !contextOn)}
        class="h-7 cursor-pointer rounded-[8px] border px-2.5 text-xs text-text-main transition-colors duration-150 hover:bg-surface-2 {contextOn
          ? 'border-brand-500/40 bg-brand-500/10 text-brand-600 dark:text-brand-400'
          : 'border-border'}"
      >
        Context ±{CONTEXT_LINES}
      </button>

      <div class="ml-auto flex items-center gap-1.5">
        <Button size="sm" variant="outline" title="Copy visible lines" disabled={visible.length === 0} onclick={copyVisible}>
          <Copy class="h-3.5 w-3.5" />
          Copy
        </Button>
        <Button size="sm" variant="outline" title="Download visible lines" disabled={visible.length === 0} onclick={downloadVisible}>
          <Download class="h-3.5 w-3.5" />
          Visible
        </Button>
        <Button
          size="sm"
          variant="outline"
          title="Download every buffered line, ignoring the filter"
          disabled={entries.length === 0}
          onclick={downloadAll}
        >
          <DownloadCloud class="h-3.5 w-3.5" />
          All
        </Button>
      </div>
    </div>
  </div>

  <div
    bind:this={viewport}
    onscroll={handleScroll}
    class="custom-scrollbar relative h-[calc(100vh-300px)] min-h-[320px] overflow-y-auto bg-surface-2/50 font-mono text-xs"
  >
    {#if entries.length === 0}
      <div class="flex h-full flex-col items-center justify-center gap-1.5 px-6 text-center">
        <p class="text-sm text-text-main">No console output yet</p>
        <p class="max-w-md text-xs text-text-muted">
          Nothing has been logged since the server started or the last clear. Send a request, or set the level to
          <span class="font-mono">debug</span>, and rows appear here live.
        </p>
      </div>
    {:else if visible.length === 0}
      <div class="flex h-full flex-col items-center justify-center gap-1.5 px-6 text-center">
        <p class="text-sm text-text-main">Nothing matches the current filter</p>
        <p class="max-w-md text-xs text-text-muted">
          {#if matcher.invalid}
            The regular expression is not valid.
          {:else if mutedLevels.size > 0 || activeTag}
            {[...mutedLevels].map((level) => LEVEL_META[level].label).join(', ')}
            {mutedLevels.size > 0 ? 'rows and ' : ''}{activeTag ? `[${activeTag}] rows ` : ''}are hidden by the filters.
          {:else}
            No line contains “{query.trim()}”.
          {/if}
        </p>
        <Button size="sm" variant="outline" onclick={clearFilters}>Show all {entries.length} lines</Button>
      </div>
    {:else}
      <div class="divide-y divide-border-subtle/60">
        {#each visible as entry (entry.seq)}
          {@const meta = LEVEL_META[entry.level]}
          {@const parts = splitTag(entry.line)}
          {@const message = parts?.message ?? entry.line}
          {@const isContext = view.contextSeqs.has(entry.seq)}
          {@const reqId = extractReqId(entry.line)}
          <div class="flex items-baseline gap-3 px-3 py-1 hover:bg-surface-3/40 {isContext ? 'opacity-50' : ''}">
            {#if timeMode !== 'off'}
              <time class="shrink-0 tabular-nums text-[11px] text-text-muted">
                {timeMode === 'relative' ? formatRelative(entry.time, baseMs) : formatClock(entry.time)}
              </time>
            {/if}
            <span class="w-8 shrink-0 text-[11px] font-bold {meta.text}">{meta.label}</span>
            <span class="min-w-0 flex-1 {wrap ? 'break-words' : 'whitespace-pre'} {meta.text}">
              {#if parts}<span class="opacity-70">{parts.tag}</span>{' '}{:else}{/if}{#each highlightParts(message, matcher) as part, i (i)}{#if part.hit}<mark
                    class="rounded-[2px] bg-amber-300/70 text-inherit dark:bg-amber-500/40">{part.text}</mark
                  >{:else}{part.text}{/if}{/each}
              {#if reqId}
                <button
                  type="button"
                  title={`Show every line of ${reqId}`}
                  onclick={() => pivotRequestId(reqId)}
                  class="ml-1.5 cursor-pointer rounded-[4px] border border-border px-1 py-0.5 font-mono text-[10px] text-text-muted hover:bg-surface-3 hover:text-text-main"
                >
                  {reqId}
                </button>
              {/if}
            </span>
          </div>
        {/each}
      </div>
    {/if}

    {#if !following && entries.length > 0}
      <button
        type="button"
        onclick={() => {
          following = true
          scrollToLatest()
        }}
        class="absolute bottom-3 left-1/2 inline-flex -translate-x-1/2 items-center gap-1.5 rounded-full border border-border bg-surface px-3 py-1.5 text-xs font-medium text-text-main shadow-[var(--shadow-elev)] transition-colors duration-150 hover:bg-surface-2"
      >
        <ArrowDown class="h-3.5 w-3.5" />
        Jump to latest
      </button>
    {/if}
  </div>

  <div class="flex flex-wrap items-center justify-between gap-2 border-t border-border-subtle px-4 py-2 text-[11px] text-text-muted">
    <span class="tabular-nums">
      Showing {visible.length} of {entries.length} buffered {entries.length === 1 ? 'line' : 'lines'}
      {#if contextOn && filtersActive && visible.length > 0}
        <span class="opacity-70">(incl. context)</span>
      {/if}
    </span>
    <span class="flex flex-wrap items-center gap-3">
      {#if older > 0}
        <span class="tabular-nums text-amber-700 dark:text-amber-400" title="The ring buffer keeps only the newest lines">
          {older} earlier {older === 1 ? 'line' : 'lines'} trimmed
        </span>
      {/if}
      {#if dropped > 0}
        <span class="tabular-nums text-amber-700 dark:text-amber-400" title="Gaps in the server line numbering">
          {dropped} {dropped === 1 ? 'line' : 'lines'} lost in transit
        </span>
      {/if}
      <span class="tabular-nums">
        {counts.error} error · {counts.warn} warn · {counts.info} info · {counts.debug} debug
      </span>
    </span>
  </div>
</div>
