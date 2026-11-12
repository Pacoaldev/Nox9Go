<script lang="ts">
  // Console Log page. Owns the authenticated SSE transport, the server log
  // level, the buffer window, and the lost-line accounting; ConsoleLogStream
  // owns how rows are read and filtered.
  import { Download, Trash2 } from 'lucide-svelte'
  import ConsoleLogStream from './ConsoleLogStream.svelte'
  import Button from '../lib/ui/Button.svelte'
  import Card from '../lib/ui/Card.svelte'
  import { getAuthHeaders, responseErrorMessage } from '../api/client'
  import { notifications } from '../lib/notifications'
  import { toConsoleEntry, type ConsoleEntry } from '../lib/consoleLog'

  const MAX_LINES = 200
  const LOG_LEVELS = ['debug', 'info', 'warn', 'error'] as const

  type Connection = 'connecting' | 'live' | 'reconnecting'

  let entries = $state<ConsoleEntry[]>([])
  let logLevel = $state<string>('info')
  let levelBusy = $state(false)
  let connection = $state<Connection>('connecting')
  // Lines the viewer provably missed: gaps in the server sequence, and lines the
  // ring buffer trimmed before the snapshot it handed us.
  let dropped = $state(0)
  let older = $state(0)
  let seq = 0
  let lastServerSeq: number | null = null

  const CONNECTION_META: Record<Connection, { label: string; chip: string; dot: string }> = {
    connecting: {
      label: 'Connecting',
      chip: 'border-border text-text-muted',
      dot: 'bg-text-muted animate-pulse',
    },
    live: {
      label: 'Live',
      chip: 'border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400',
      dot: 'bg-emerald-500',
    },
    reconnecting: {
      label: 'Reconnecting',
      chip: 'border-amber-500/40 bg-amber-500/10 text-amber-800 dark:text-amber-400',
      dot: 'bg-amber-500 animate-pulse',
    },
  }

  // A reconnect replays the whole buffer as "init", so the old rows have to go
  // or the same line would appear twice. The snapshot also tells us how many
  // lines the ring buffer trimmed before it: the first server sequence minus 1.
  function adoptBuffer(raw: unknown[]): void {
    seq = 0
    entries = raw.map((item) => toConsoleEntry(item as Record<string, unknown>, seq++))
    dropped = 0
    const seqs = entries.map((e) => e.serverSeq).filter((n): n is number => n !== null)
    older = seqs.length > 0 ? Math.max(0, Math.min(...seqs) - 1) : 0
    lastServerSeq = seqs.length > 0 ? Math.max(...seqs) : null
  }

  function noteServerSeq(entry: ConsoleEntry): void {
    if (entry.serverSeq === null) return
    if (lastServerSeq !== null && entry.serverSeq > lastServerSeq + 1) {
      dropped += entry.serverSeq - lastServerSeq - 1
    }
    lastServerSeq = entry.serverSeq
  }

  async function fetchLogLevel() {
    try {
      const res = await fetch('/api/translator/console-logs/level', {
        headers: getAuthHeaders(),
      })
      if (!res.ok) return
      const data = await res.json()
      if (typeof data.level === 'string') logLevel = data.level
    } catch {
      // keep default; the selector still writes a level
    }
  }

  async function handleLevelChange(e: Event) {
    const next = (e.target as HTMLSelectElement).value
    const prev = logLevel
    logLevel = next
    levelBusy = true
    try {
      const res = await fetch('/api/translator/console-logs/level', {
        method: 'PUT',
        headers: getAuthHeaders(),
        body: JSON.stringify({ level: next }),
      })
      if (!res.ok) throw new Error(await responseErrorMessage(res, `HTTP ${res.status}`))
      const data = await res.json()
      if (!data?.success) throw new Error('Log level update was rejected')
      logLevel = data.level || next
      notifications.success(`Log level set to ${logLevel} (no restart needed)`)
    } catch (err) {
      logLevel = prev
      notifications.error(`Failed to set log level: ${err instanceof Error ? err.message : err}`)
    } finally {
      levelBusy = false
    }
  }

  async function handleClear() {
    try {
      const res = await fetch('/api/translator/console-logs', {
        method: 'DELETE',
        headers: getAuthHeaders(),
      })
      if (!res.ok) throw new Error(await responseErrorMessage(res, `HTTP ${res.status}`))
      // The view clears via the SSE "clear" event, so the server stays the
      // single owner of the buffer.
    } catch (err) {
      notifications.error(`Failed to clear console logs: ${err instanceof Error ? err.message : err}`)
    }
  }

  // The persisted log survives a restart. The binary only writes it when
  // CONSOLE_LOG_FILE is set, so a 404 is an expected, explained answer.
  async function handleDownloadPersisted() {
    try {
      const res = await fetch('/api/translator/console-logs/file', { headers: getAuthHeaders() })
      if (!res.ok) throw new Error(await responseErrorMessage(res, `HTTP ${res.status}`))
      const disposition = res.headers.get('Content-Disposition') ?? ''
      const filename = /filename="?([^"]+)"?/.exec(disposition)?.[1] ?? 'nox9go-console.log'
      const url = URL.createObjectURL(await res.blob())
      const link = document.createElement('a')
      link.href = url
      link.download = filename
      link.click()
      URL.revokeObjectURL(url)
      notifications.success('Downloaded the persisted console log')
    } catch (err) {
      notifications.error(`No persisted log: ${err instanceof Error ? err.message : err}`)
    }
  }

  fetchLogLevel()

  $effect(() => {
    let cancelled = false
    let controller: AbortController | null = null

    // Read through fetch so the stream rides the same authenticated transport
    // and error handling as the rest of the dashboard.
    const connect = async () => {
      try {
        controller = new AbortController()
        const res = await fetch('/api/translator/console-logs/stream', {
          headers: getAuthHeaders(),
          signal: controller.signal,
        })
        if (!res.ok) throw new Error(await responseErrorMessage(res, `HTTP ${res.status}`))

        const reader = res.body?.getReader()
        if (!reader) return

        const decoder = new TextDecoder()
        let buffer = ''
        while (!cancelled) {
          const { done, value } = await reader.read()
          if (done) break

          buffer += decoder.decode(value, { stream: true })
          const frames = buffer.split('\n')
          buffer = frames.pop() || ''

          for (const frame of frames) {
            const trimmed = frame.trim()
            if (!trimmed.startsWith('data: ')) continue

            let msg: Record<string, unknown>
            try {
              msg = JSON.parse(trimmed.slice(6))
            } catch {
              continue
            }

            if (msg.type === 'init') {
              adoptBuffer(Array.isArray(msg.entries) ? msg.entries : [])
              connection = 'live'
            } else if (msg.type === 'line') {
              const entry = toConsoleEntry(msg.entry as Record<string, unknown>, seq++)
              noteServerSeq(entry)
              entries = [...entries, entry].slice(-MAX_LINES)
              connection = 'live'
            } else if (msg.type === 'clear') {
              entries = []
              dropped = 0
              older = 0
            }
          }
        }
      } catch {
        // stream closed or aborted — reconnect below
      }

      if (!cancelled) {
        connection = 'reconnecting'
        setTimeout(connect, 5000)
      }
    }

    connect()

    return () => {
      cancelled = true
      controller?.abort()
    }
  })

  const connMeta = $derived(CONNECTION_META[connection])
</script>

<Card padding="none" class="flex flex-col">
  <div class="flex flex-wrap items-center gap-2 border-b border-border-subtle px-4 py-2.5">
    <span
      class="inline-flex h-6 items-center gap-1.5 rounded-full border px-2 text-[11px] font-medium {connMeta.chip}"
      title={connection === 'live' ? 'Streaming live' : connection === 'reconnecting' ? 'Reconnecting to the stream' : 'Opening the stream'}
    >
      <span class="h-1.5 w-1.5 rounded-full {connMeta.dot}"></span>
      {connMeta.label}
    </span>

    <label class="text-xs text-text-muted" for="console-log-level">Level</label>
    <select
      id="console-log-level"
      class="rounded-[8px] border border-border bg-surface px-2 py-1 text-xs text-text-main disabled:opacity-50"
      bind:value={logLevel}
      disabled={levelBusy}
      onchange={handleLevelChange}
    >
      {#each LOG_LEVELS as level (level)}
        <option value={level}>{level}</option>
      {/each}
    </select>
    <span class="text-[11px] text-text-muted">
      Applies immediately — lines below this level are not buffered.
    </span>

    <div class="ml-auto flex items-center gap-2">
      <Button size="sm" variant="outline" title="Download the log file persisted to disk (survives restarts)" onclick={handleDownloadPersisted}>
        <Download class="h-3.5 w-3.5" />
        Persisted
      </Button>
      <Button size="sm" variant="outline" title="Clear the buffer" onclick={handleClear}>
        <Trash2 class="h-3.5 w-3.5" />
        Clear
      </Button>
    </div>
  </div>

  <ConsoleLogStream {entries} {dropped} {older} />
</Card>
