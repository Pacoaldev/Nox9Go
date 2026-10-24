// Port of parseProxyLine from upstream
// src/app/(dashboard)/dashboard/proxy-pools/page.js.
// Supported batch-import line formats:
//   - protocol://user:pass@host:port (any URL with a scheme)
//   - host:port:user:pass
export interface ParsedProxy {
  proxyUrl: string
  name: string
}

export function parseProxyLine(line: string): ParsedProxy | null {
  const trimmed = line.trim()
  if (!trimmed) return null

  if (trimmed.includes('://')) {
    const parsed = new URL(trimmed)
    const hostLabel = parsed.port ? `${parsed.hostname}:${parsed.port}` : parsed.hostname
    return {
      proxyUrl: parsed.toString(),
      name: `Imported ${hostLabel}`,
    }
  }

  const parts = trimmed.split(':')
  if (parts.length === 4) {
    const [host, port, username, password] = parts
    if (!host || !port || !username || !password) {
      throw new Error('Invalid host:port:user:pass format')
    }
    const proxyUrl = `http://${encodeURIComponent(username)}:${encodeURIComponent(password)}@${host}:${port}`
    const parsed = new URL(proxyUrl)
    return {
      proxyUrl: parsed.toString(),
      name: `Imported ${host}:${port}`,
    }
  }

  throw new Error('Unsupported format')
}
