import { describe, expect, test } from 'bun:test'
import { proxyBadgeInfo } from './proxyBadge'
import type { ProviderConnection, ProxyPool } from '../../api/client'

function pool(overrides: Partial<ProxyPool> = {}): ProxyPool {
  return {
    id: 'pool-1',
    name: 'SG Proxy',
    type: 'http',
    proxyUrl: 'http://user:secret@proxy.example.com:8080',
    noProxy: 'localhost,127.0.0.1',
    isActive: true,
    ...overrides
  }
}

function conn(psd: Record<string, unknown>): Pick<ProviderConnection, 'providerSpecificData'> {
  return { providerSpecificData: psd } as Pick<ProviderConnection, 'providerSpecificData'>
}

describe('proxyBadgeInfo', () => {
  test('unbound connection shows no badge', () => {
    const info = proxyBadgeInfo(conn({}), [pool()])
    expect(info.hasAnyProxy).toBe(false)
    expect(info.displayText).toBe('Legacy: ')
    expect(info.maskedProxyUrl).toBe('')
    expect(info.noProxyText).toBe('')
  })

  test('active pool is a success badge with a masked endpoint', () => {
    const info = proxyBadgeInfo(conn({ proxyPoolId: 'pool-1' }), [pool()])
    expect(info.hasAnyProxy).toBe(true)
    expect(info.variant).toBe('success')
    expect(info.displayText).toBe('Pool: SG Proxy')
    // Credentials, path and username are stripped.
    expect(info.maskedProxyUrl).toBe('http://proxy.example.com:8080')
    expect(info.maskedProxyUrl).not.toContain('secret')
    expect(info.noProxyText).toBe('localhost,127.0.0.1')
  })

  test('omits the port when the proxy URL has none', () => {
    const info = proxyBadgeInfo(conn({ proxyPoolId: 'pool-1' }), [
      pool({ proxyUrl: 'socks5://proxy.example.com' })
    ])
    expect(info.maskedProxyUrl).toBe('socks5://proxy.example.com')
  })

  test('inactive pool is an error badge', () => {
    const info = proxyBadgeInfo(conn({ proxyPoolId: 'pool-1' }), [pool({ isActive: false })])
    expect(info.variant).toBe('error')
    expect(info.displayText).toBe('Pool: SG Proxy')
  })

  test('missing pool falls back to the id with an (inactive/missing) label', () => {
    const info = proxyBadgeInfo(conn({ proxyPoolId: 'pool-gone' }), [pool()])
    expect(info.variant).toBe('error')
    expect(info.displayText).toBe('Pool: pool-gone (inactive/missing)')
    expect(info.maskedProxyUrl).toBe('')
  })

  test('legacy per-connection proxy is an error badge', () => {
    const info = proxyBadgeInfo(
      conn({
        connectionProxyEnabled: true,
        connectionProxyUrl: 'http://legacy:pass@legacy.example.com:3128',
        connectionNoProxy: '10.0.0.0/8'
      }),
      []
    )
    expect(info.hasAnyProxy).toBe(true)
    expect(info.variant).toBe('error')
    expect(info.displayText).toBe('Legacy: http://legacy:pass@legacy.example.com:3128')
    expect(info.maskedProxyUrl).toBe('http://legacy.example.com:3128')
    expect(info.noProxyText).toBe('10.0.0.0/8')
  })

  test('disabled legacy proxy fields do not produce a badge', () => {
    const info = proxyBadgeInfo(
      conn({ connectionProxyEnabled: false, connectionProxyUrl: 'http://legacy.example.com:3128' }),
      []
    )
    expect(info.hasAnyProxy).toBe(false)
  })

  test('unparsable URLs are shown as-is', () => {
    const info = proxyBadgeInfo(conn({ proxyPoolId: 'p', providerSpecificData: {} }), [
      pool({ id: 'p', proxyUrl: 'not a url' })
    ])
    expect(info.maskedProxyUrl).toBe('not a url')
  })
})
