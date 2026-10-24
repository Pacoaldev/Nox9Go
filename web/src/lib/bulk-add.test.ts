// Guards the bulk-add naming bug: generated "Key N" names must never collide
// with an existing connection name (connections are upserted by name, so a
// collision overwrites instead of inserting). Ported from upstream
// tests/unit/bulk-add-names.test.js.
import assert from 'node:assert'
import { describe, it } from 'node:test'
import { planBulkAdd } from './bulk-add'

describe('planBulkAdd: auto-named gap-fill', () => {
  it('uses Key 1..N by paste index when nothing exists', () => {
    const out = planBulkAdd(['sk-a', 'sk-b', 'sk-c'], [])
    assert.deepStrictEqual(out.map((o) => o.name), ['Key 1', 'Key 2', 'Key 3'])
    assert.ok(out.every((o) => o.skipped === false))
  })

  it('gap-fills around existing names — never reuses an existing name', () => {
    const out = planBulkAdd(['sk-a', 'sk-b', 'sk-c', 'sk-d'], ['Key 3', 'Key 5'])
    assert.deepStrictEqual(out.map((o) => o.name), ['Key 1', 'Key 2', 'Key 4', 'Key 6'])
  })

  it('continues past the highest existing index when low slots are taken', () => {
    const out = planBulkAdd(['sk-a', 'sk-b'], ['Key 1', 'Key 2'])
    assert.deepStrictEqual(out.map((o) => o.name), ['Key 3', 'Key 4'])
  })

  it('skips blank/whitespace-only lines but keeps indexing contiguous', () => {
    const out = planBulkAdd(['sk-a', '   ', '', 'sk-b'], [])
    assert.deepStrictEqual(out.map((o) => o.name), ['Key 1', 'Key 2'])
    assert.deepStrictEqual(out.map((o) => o.apiKey), ['sk-a', 'sk-b'])
  })

  it('within-batch names are unique even for the same free slot', () => {
    const out = planBulkAdd(['sk-a', 'sk-b', 'sk-c'], ['Key 1'])
    const names = out.map((o) => o.name)
    assert.strictEqual(new Set(names).size, names.length)
    assert.deepStrictEqual(names, ['Key 2', 'Key 3', 'Key 4'])
  })
})

describe('planBulkAdd: custom name|apiKey', () => {
  it('uses the literal base name with a gap-filled index', () => {
    const out = planBulkAdd(['Prod|sk-1', 'Prod|sk-2'], [])
    assert.deepStrictEqual(out.map((o) => o.name), ['Prod 1', 'Prod 2'])
    assert.deepStrictEqual(out.map((o) => o.apiKey), ['sk-1', 'sk-2'])
  })

  it('custom name avoids an existing same-base name', () => {
    const out = planBulkAdd(['Prod|sk-new'], ['Prod 1'])
    assert.strictEqual(out[0].name, 'Prod 2')
  })

  it('apiKey containing pipes is preserved (parts after first rejoined)', () => {
    const out = planBulkAdd(['Prod|sk|with|pipes'], [])
    assert.strictEqual(out[0].apiKey, 'sk|with|pipes')
    assert.strictEqual(out[0].name, 'Prod 1')
  })
})

describe('planBulkAdd: cloudflare-ai (name|apiKey|accountId)', () => {
  it('parses 3-part lines into name + apiKey + accountId', () => {
    const out = planBulkAdd(['main|sk-key1|acc123', 'main|sk-key2|def789'], [], { isCloudflareAi: true })
    assert.deepStrictEqual(out.map((o) => o.name), ['main 1', 'main 2'])
    assert.strictEqual(out[0].apiKey, 'sk-key1')
    assert.deepStrictEqual(out[0].providerSpecificData, { accountId: 'acc123' })
    assert.deepStrictEqual(out[1].providerSpecificData, { accountId: 'def789' })
  })

  it('2-part cloudflare line is name|apiKey (no accountId)', () => {
    const out = planBulkAdd(['main|sk-key1'], [], { isCloudflareAi: true })
    assert.strictEqual(out[0].name, 'main 1')
    assert.strictEqual(out[0].apiKey, 'sk-key1')
    assert.strictEqual(out[0].providerSpecificData, undefined)
  })

  it('1-part cloudflare line is auto-named Key N', () => {
    const out = planBulkAdd(['sk-key1'], [], { isCloudflareAi: true })
    assert.strictEqual(out[0].name, 'Key 1')
    assert.strictEqual(out[0].apiKey, 'sk-key1')
  })
})

describe('planBulkAdd: robustness', () => {
  it('returns [] for no input', () => {
    assert.deepStrictEqual(planBulkAdd([], []), [])
    assert.deepStrictEqual(planBulkAdd(['', '  '], []), [])
  })

  it('trims names and apiKeys', () => {
    const out = planBulkAdd(['  Prod  |  sk-1  '], [])
    assert.strictEqual(out[0].name, 'Prod 1')
    assert.strictEqual(out[0].apiKey, 'sk-1')
  })

  it("falls back to base 'Key' when name part is empty", () => {
    const out = planBulkAdd(['|sk-1'], [])
    assert.strictEqual(out[0].name, 'Key 1')
    assert.strictEqual(out[0].apiKey, 'sk-1')
  })

  it('coerces non-array existingNames gracefully', () => {
    const out = planBulkAdd(['sk-a'], null)
    assert.strictEqual(out[0].name, 'Key 1')
  })
})
