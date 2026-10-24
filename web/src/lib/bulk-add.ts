// Bulk-add API-key planner — port of upstream src/shared/utils/bulkAdd.js.
//
// Background: connections are upserted BY NAME, so a generated name that
// collides with an existing connection silently replaces it. planBulkAdd
// gap-fills the smallest free "<base> <n>" against both existing connection
// names and names already assigned earlier in the same batch, so a generated
// name is never reused.

export interface BulkAddEntry {
  name: string
  apiKey: string
  skipped: boolean
  providerSpecificData?: Record<string, unknown>
}

export interface PlanBulkAddOptions {
  isCloudflareAi?: boolean
}

interface ParsedLine {
  baseName: string
  apiKey: string
  providerSpecificData?: Record<string, unknown>
}

// parseLine turns one pipe-separated bulk line into {baseName, apiKey, ...}.
function parseLine(line: string, opts: PlanBulkAddOptions = {}): ParsedLine | null {
  const { isCloudflareAi = false } = opts
  const parts = line.split('|')

  if (isCloudflareAi && parts.length >= 3) {
    // name|apiKey|accountId (apiKey may itself contain pipes)
    const baseName = parts[0].trim()
    const apiKey = parts.slice(1, -1).join('|').trim()
    const accountId = parts[parts.length - 1].trim()
    return { baseName: baseName || 'Key', apiKey, providerSpecificData: { accountId } }
  }

  if (parts.length >= 2) {
    // name|apiKey (apiKey may itself contain pipes)
    const baseName = parts[0].trim()
    const apiKey = parts.slice(1).join('|').trim()
    return { baseName: baseName || 'Key', apiKey }
  }

  // apiKey only — auto-named "Key N"
  return { baseName: 'Key', apiKey: parts[0].trim() }
}

// planBulkAdd parses the pasted lines and assigns collision-free "<base> <n>"
// names, skipping blank lines and lines without a key.
export function planBulkAdd(
  lines: string[],
  existingNames?: string[] | null,
  opts: PlanBulkAddOptions = {}
): BulkAddEntry[] {
  const { isCloudflareAi = false } = opts

  const safeExisting = Array.isArray(existingNames) ? existingNames : []
  const used = new Set(safeExisting.map((n) => (typeof n === 'string' ? n.toLowerCase() : '')))

  const out: BulkAddEntry[] = []
  for (const raw of lines) {
    const line = typeof raw === 'string' ? raw.trim() : ''
    if (!line) continue

    const parsed = parseLine(line, { isCloudflareAi })
    if (!parsed || !parsed.apiKey) continue

    // Gap-fill from 1: smallest free "<base> <n>" not in `used`.
    let idx = 1
    let name = `${parsed.baseName} ${idx}`
    while (used.has(name.toLowerCase())) {
      idx += 1
      name = `${parsed.baseName} ${idx}`
    }
    used.add(name.toLowerCase())

    const entry: BulkAddEntry = { name, apiKey: parsed.apiKey, skipped: false }
    if (parsed.providerSpecificData) entry.providerSpecificData = parsed.providerSpecificData
    out.push(entry)
  }
  return out
}
