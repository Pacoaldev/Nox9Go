import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { parseProxyLine } from './proxy-import'

describe('parseProxyLine (upstream proxy-pools parity)', () => {
  it('parses a full proxy URL', () => {
    const res = parseProxyLine('http://user:pass@127.0.0.1:7897')
    assert.ok(res)
    assert.strictEqual(res.proxyUrl, 'http://user:pass@127.0.0.1:7897/')
    assert.strictEqual(res.name, 'Imported 127.0.0.1:7897')
  })

  it('parses a URL without credentials and port', () => {
    const res = parseProxyLine('socks5://127.0.0.1:1080')
    assert.ok(res)
    assert.strictEqual(res.name, 'Imported 127.0.0.1:1080')
  })

  it('parses host:port:user:pass format', () => {
    const res = parseProxyLine('127.0.0.1:7897:user:pass')
    assert.ok(res)
    assert.strictEqual(res.proxyUrl, 'http://user:pass@127.0.0.1:7897/')
    assert.strictEqual(res.name, 'Imported 127.0.0.1:7897')
  })

  it('returns null for blank lines', () => {
    assert.strictEqual(parseProxyLine('   '), null)
  })

  it('throws on unsupported formats', () => {
    assert.throws(() => parseProxyLine('just-a-hostname'), /Unsupported format/)
    assert.throws(() => parseProxyLine('a:b:c'), /Unsupported format/)
  })

  it('throws on invalid host:port:user:pass segments', () => {
    assert.throws(() => parseProxyLine(':::'), /Invalid host:port:user:pass format/)
  })
})
