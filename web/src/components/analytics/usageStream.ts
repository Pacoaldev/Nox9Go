import { getAuthHeaders } from '../../api/client'
import type { ActiveRequestItem, RecentRequestItem } from './types'

/**
 * The usage stream, the SSE channel the dashboard's live panels read.
 *
 * The socket is the scarce resource: the gateway keeps one subscriber per
 * connection, and two readers of the same stream meant two connections for
 * the same numbers — with the reconnect, abort and framing rules written
 * twice. This module owns the connection and hands every payload to the
 * caller, and the caller owns the state it draws.
 */

export interface UsageStreamPayload {
  /** Requests in flight right now. Absent when the server omits the field. */
  activeRequests?: ActiveRequestItem[]
  /** The most recent completed requests, newest first. */
  recentRequests?: RecentRequestItem[]
  /** The provider that last failed, when it failed recently. */
  errorProvider?: string
}

/** How long to wait before reconnecting after the stream drops. */
const RECONNECT_MS = 3000

/**
 * Opens the usage stream and calls `onPayload` for every frame it receives.
 *
 * The returned function closes the connection; call it when the page
 * unmounts, or the reconnect timer keeps the socket alive after the reader
 * is gone.
 */
export function connectUsageStream(onPayload: (payload: UsageStreamPayload) => void): () => void {
  let cancelled = false
  let controller: AbortController | null = null
  let reconnectTimeout: ReturnType<typeof setTimeout> | null = null

  const token = typeof localStorage !== 'undefined' ? localStorage.getItem('nox9go_key') || '' : ''

  const connect = async () => {
    if (cancelled) return
    controller = new AbortController()

    try {
      const streamUrl = token
        ? `/api/usage/stream?key=${encodeURIComponent(token)}`
        : '/api/usage/stream'
      const res = await fetch(streamUrl, {
        headers: getAuthHeaders(),
        signal: controller.signal,
      })
      if (!res.ok) {
        throw new Error(`usage stream failed: ${res.status}`)
      }

      const reader = res.body?.getReader()
      if (!reader) return
      const decoder = new TextDecoder()

      let buffer = ''
      while (!cancelled) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          const trimmed = line.trim()
          if (!trimmed || trimmed.startsWith(':') || !trimmed.startsWith('data: ')) continue
          try {
            onPayload(JSON.parse(trimmed.slice(6)) as UsageStreamPayload)
          } catch (err) {
            console.error('Failed to parse SSE usage stream:', err)
          }
        }
      }
    } catch {
      // The stream drops when the gateway restarts; retry until the page goes
      // away. A cancelled reader throws here too, and that is not a failure.
      if (!cancelled) reconnectTimeout = setTimeout(connect, RECONNECT_MS)
    }
  }

  connect()

  return () => {
    cancelled = true
    if (reconnectTimeout) clearTimeout(reconnectTimeout)
    if (controller) controller.abort()
  }
}
