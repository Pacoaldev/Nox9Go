// Parsing rules for the Console Log page.
//
// The server stamps every captured line with the level the logger reported
// (see internal/log.ConsoleEntry), so a row is never assigned a severity by
// matching its text. The text fallbacks here exist only for lines that never
// went through the logger — captured stdout, a panic dump, a library printing
// straight to the process — where no level was ever recorded.

export type LogLevel = 'debug' | 'info' | 'warn' | 'error'

export interface ConsoleEntry {
  /** Arrival time as reported by the server; the display clock when absent. */
  time: string
  level: LogLevel
  line: string
  /** Client sequence number, so rows sharing a timestamp keep their arrival order. */
  seq: number
  /**
   * Server-assigned monotonic line number, when the server sent one. A gap
   * between consecutive serverSeq values proves the viewer missed output.
   * Null for older servers that only send {time,level,line}.
   */
  serverSeq: number | null
}

/** The raw SSE / REST payload, before display parsing. */
export interface RawConsoleEntry {
  time?: unknown
  level?: unknown
  line?: unknown
  seq?: unknown
}

export interface LevelCounts {
  debug: number
  info: number
  warn: number
  error: number
}

// Logger prefixes (DBG/INF/WRN/ERR) plus the long forms other emitters print.
// The trailing lookahead keeps a bare word like "WARN" from reading as a level
// when it is only the start of a message.
const PREFIX_RE = /^(DBG|DEBUG|INF|INFO|WRN|WARN|ERR|ERROR)(?=\s)/

// A leading Go log timestamp, which hides the level prefix behind it.
const LEADING_TIMESTAMP_RE = /^\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?\s*/

// The ESC byte is the point here, not an oversight — these are the CSI
// sequences a terminal emits and they must be removed, not tolerated.
// oxlint-disable-next-line no-control-regex
const ANSI_RE = /\u001b\[[0-9;]*m/g

const pad = (value: number, size = 2) => String(value).padStart(size, '0')

function coerceLevel(value: unknown): LogLevel | null {
  if (typeof value !== 'string') return null
  const lowered = value.toLowerCase()
  if (lowered === 'debug' || lowered === 'info' || lowered === 'warn' || lowered === 'error') {
    return lowered
  }
  return null
}

/** ANSI escapes survive only on lines the logger never formatted itself. */
export function stripAnsi(line: string): string {
  return line.includes('\u001b[') ? line.replace(ANSI_RE, '') : line
}

function levelFromText(line: string): LogLevel | null {
  const prefix = PREFIX_RE.exec(line.replace(LEADING_TIMESTAMP_RE, ''))?.[1]?.toLowerCase()
  if (!prefix) return null
  if (prefix.startsWith('err')) return 'error'
  if (prefix.startsWith('wrn') || prefix.startsWith('warn')) return 'warn'
  if (prefix.startsWith('dbg')) return 'debug'
  return 'info'
}

/**
 * A JSON log line (LOG_FORMAT=json) is a complete record on its own: the body
 * carries the authoritative timestamp and level. Returns null for text that
 * merely starts with a brace, so a truncated print is not silently swallowed.
 */
function parseJsonLine(line: string): { time?: string; level?: LogLevel } | null {
  const trimmed = line.trimStart()
  if (!trimmed.startsWith('{') || !trimmed.endsWith('}')) return null

  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch {
    return null
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) return null

  const record = parsed as Record<string, unknown>
  const time = typeof record.time === 'string' ? record.time : undefined
  const level = coerceLevel(record.level)
  return { time, level: level ?? undefined }
}

/**
 * Turns one raw payload into a display row.
 *
 * Level precedence: the level the server stamped, then the JSON body's own
 * level, then a text prefix, and finally "info". Untagged stdout is ordinary
 * output — painting it red would cry wolf on every plain print, which is the
 * habit this page is being fixed for.
 */
export function toConsoleEntry(
  raw: RawConsoleEntry,
  seq: number,
  receivedAt: number = Date.now()
): ConsoleEntry {
  const line = stripAnsi(typeof raw.line === 'string' ? raw.line : '')
  const json = parseJsonLine(line)
  const serverTime = typeof raw.time === 'string' ? Date.parse(raw.time) : Number.NaN

  return {
    time: json?.time ?? (Number.isNaN(serverTime) ? new Date(receivedAt) : new Date(serverTime)).toISOString(),
    level: coerceLevel(raw.level) ?? json?.level ?? levelFromText(line) ?? 'info',
    line,
    seq,
    serverSeq: coerceSeq(raw.seq),
  }
}

function coerceSeq(value: unknown): number | null {
  if (typeof value === 'number' && Number.isFinite(value) && value >= 0) return value
  return null
}

export function countByLevel(entries: ConsoleEntry[]): LevelCounts {
  const counts: LevelCounts = { debug: 0, info: 0, warn: 0, error: 0 }
  for (const entry of entries) counts[entry.level]++
  return counts
}

export interface SearchOptions {
  query: string
  /** Match case exactly instead of folding to lower-case. */
  caseSensitive?: boolean
  /** Treat the query as a regular expression instead of a literal substring. */
  regex?: boolean
}

export interface Matcher {
  /** True when the matcher can test a line. */
  test: (line: string) => boolean
  /** The compiled regex for highlighting, or null for a literal/empty query. */
  regex: RegExp | null
  /** Set when regex mode was requested but the pattern is invalid. */
  invalid: boolean
}

/**
 * Compiles a search into a reusable matcher. A literal query is escaped so
 * characters like `.` or `[` are searched as text; a regex query is used as
 * written. An invalid regex is reported (invalid: true) instead of thrown, so
 * the field can show the error while the list keeps the previous results.
 */
export function compileSearch(options: SearchOptions | string): Matcher {
  const opts: SearchOptions = typeof options === 'string' ? { query: options } : options
  const query = opts.query.trim()
  if (!query) return { test: () => true, regex: null, invalid: false }

  const caseFlags = opts.caseSensitive ? '' : 'i'
  const source = opts.regex ? query : query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

  let globalRegex: RegExp
  try {
    // The highlighter needs /g; the tester must not, because RegExp.test is
    // stateful on a global regex and would skip every other match.
    globalRegex = new RegExp(source, caseFlags + 'g')
  } catch {
    return { test: () => true, regex: null, invalid: true }
  }
  const plainRegex = new RegExp(source, caseFlags)
  return { test: (line: string) => plainRegex.test(line), regex: globalRegex, invalid: false }
}

/**
 * Filters entries by a search. Accepts a bare string for the historical
 * case-insensitive substring behaviour, or SearchOptions for case/regex.
 */
export function filterEntries(entries: ConsoleEntry[], search: SearchOptions | string): ConsoleEntry[] {
  const matcher = compileSearch(search)
  if (!matcher.regex) return entries
  return entries.filter((entry) => matcher.test(entry.line))
}

export interface HighlightPart {
  text: string
  hit: boolean
}

/**
 * Splits a line into alternating plain/matched segments for rendering with
 * <mark>. Uses the matcher's global regex, so a highlighted row shows exactly
 * why it matched instead of forcing the operator to re-scan a long line.
 */
export function highlightParts(line: string, matcher: Matcher): HighlightPart[] {
  const regex = matcher.regex
  if (!regex) return [{ text: line, hit: false }]

  regex.lastIndex = 0
  const parts: HighlightPart[] = []
  let last = 0
  let match: RegExpExecArray | null
  while ((match = regex.exec(line)) !== null) {
    if (match[0] === '') {
      regex.lastIndex++
      continue
    }
    if (match.index > last) parts.push({ text: line.slice(last, match.index), hit: false })
    parts.push({ text: match[0], hit: true })
    last = match.index + match[0].length
    if (parts.length > 500) break // pathological pattern guard
  }
  if (last < line.length) parts.push({ text: line.slice(last), hit: false })
  return parts.length > 0 ? parts : [{ text: line, hit: false }]
}

export interface TagCount {
  tag: string
  count: number
}

/**
 * Collects the subsystem tags ([usage], [auth], ...) present in the buffer,
 * most frequent first, so the page can offer them as filter chips. Derived
 * from the rendered line, which is the only place the tag survives.
 */
export function collectTags(entries: ConsoleEntry[]): TagCount[] {
  const counts = new Map<string, number>()
  for (const entry of entries) {
    const parsed = splitTag(entry.line)
    if (!parsed) continue
    counts.set(parsed.tag, (counts.get(parsed.tag) ?? 0) + 1)
  }
  return [...counts.entries()]
    .map(([tag, count]) => ({ tag, count }))
    .sort((a, b) => b.count - a.count || a.tag.localeCompare(b.tag))
}

// req_id appears either as `req_id=req_ab12` in text format or `"req_id":"req_ab12"`
// in JSON format (quote, colon, quote). Capturing it lets a click pivot to every
// line of one request.
const REQ_ID_RE = /\breq_id"?\s*[=:]\s*"?(req_[0-9a-zA-Z]+)/

export function extractReqId(line: string): string | null {
  return REQ_ID_RE.exec(line)?.[1] ?? null
}

/** Reads a [tag] out of a rendered line, or '' when it has none. */
export function entryTag(line: string): string {
  return splitTag(line)?.tag ?? ''
}

/**
 * Formats a row's time relative to a base instant (the first visible row), for
 * reading a burst of requests as a timeline instead of absolute clocks.
 */
export function formatRelative(iso: string, baseMs: number): string {
  const parsed = Date.parse(iso)
  if (Number.isNaN(parsed)) return '--'
  const deltaMs = parsed - baseMs
  const sign = deltaMs < 0 ? '-' : '+'
  const abs = Math.abs(deltaMs)
  if (abs < 1000) return `${sign}${abs}ms`
  if (abs < 60_000) return `${sign}${(abs / 1000).toFixed(abs < 10_000 ? 2 : 1)}s`
  const minutes = Math.floor(abs / 60_000)
  const seconds = Math.floor((abs % 60_000) / 1000)
  return `${sign}${minutes}m${String(seconds).padStart(2, '0')}s`
}

/** HH:MM:SS.mmm — a burst of requests needs sub-second resolution to be readable. */
export function formatClock(iso: string): string {
  const parsed = Date.parse(iso)
  if (Number.isNaN(parsed)) return '--:--:--.---'
  const date = new Date(parsed)
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}`
}

/**
 * Splits the leading "[tag]" group so the row can show subsystem and message
 * apart. The logger prints "INF [usage] logged", so the timestamp and level
 * prefix in front of the bracket are skipped rather than demanded absent.
 */
export function splitTag(line: string): { tag: string; message: string } | null {
  const withoutPrefix = line.replace(LEADING_TIMESTAMP_RE, '').replace(PREFIX_RE, '').trimStart()
  const match = /^\[([^\]\s]+)\]\s?([\s\S]*)$/.exec(withoutPrefix)
  return match ? { tag: match[1], message: match[2] } : null
}


/** Export text, one row per line, timestamped like the on-screen gutter. */
export function toPlainText(entries: ConsoleEntry[]): string {
  return entries
    .map((entry) => `${formatClock(entry.time)} ${entry.level.toUpperCase()} ${entry.line}`)
    .join('\n')
}

export function downloadFilename(when = new Date()): string {
  return `nox9go-console-${when.getFullYear()}${pad(when.getMonth() + 1)}${pad(when.getDate())}-${pad(when.getHours())}${pad(when.getMinutes())}${pad(when.getSeconds())}.log`
}