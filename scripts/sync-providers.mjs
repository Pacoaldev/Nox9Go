#!/usr/bin/env node
// Sync the free / API-key provider catalogue from the two upstream catalogues.
//
//   node scripts/sync-providers.mjs           # refresh docs/providers_free.md
//   node scripts/sync-providers.mjs --check   # refresh, then print only NEW providers
//
// Sources (both upstream, never the local registry — the local copy is the
// thing this file is meant to keep in sync with):
//
//   decolua/9router              open-sse/providers/registry/*.js
//   cheaper-inference/OmniRoute  open-sse/config/providers/registry/*/index.ts
//                                 config/quality/free-catalog-evidence.json
//
// Filter: API-key auth only. OAuth-only providers, web-cookie/session
// providers and localhost endpoints are dropped — a provider that needs a
// browser login is not usable as a plain API key.
//
// Outputs:
//   docs/providers_free.md          the catalogue (gitignored)
//   docs/.providers_baseline.tsv    last-seen snapshot for --check (gitignored)
//
// No dependency: Node's built-in fetch only (AGENTS.md §4.F).

import { readFile, writeFile, mkdir } from "node:fs/promises";
import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const OUT_MD = join(ROOT, "docs", "providers_free.md");
const BASELINE = join(ROOT, "docs", ".providers_baseline.tsv");

const REPOS = {
  "9router": "decolua/9router",
  omniroute: "cheaper-inference/OmniRoute",
};

const LOCALISH =
  /localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\]|^(devin|wss):|^edge-tts$|^google-tts$|^local-device$|^searxng$/i;

// ---------------------------------------------------------------------------
// fetch helpers
// ---------------------------------------------------------------------------

async function gh(path) {
  const res = await fetch(`https://api.github.com${path}`, {
    headers: { "User-Agent": "nox9go-sync-providers" },
  });
  if (!res.ok) throw new Error(`GET ${path} -> ${res.status}`);
  return res.json();
}

async function raw(repo, path) {
  const res = await fetch(`https://raw.githubusercontent.com/${repo}/HEAD/${path}`, {
    headers: { "User-Agent": "nox9go-sync-providers" },
  });
  if (!res.ok) return null;
  return res.text();
}

async function mapLimit(items, limit, fn) {
  const out = new Array(items.length);
  let cursor = 0;
  const workers = Array.from({ length: Math.min(limit, items.length) }, async () => {
    while (cursor < items.length) {
      const i = cursor++;
      out[i] = await fn(items[i], i);
    }
  });
  await Promise.all(workers);
  return out;
}

// ---------------------------------------------------------------------------
// parse helpers
// ---------------------------------------------------------------------------

const quoted = (body, key) => {
  const m = new RegExp(`\\b${key}\\s*:\\s*"([^"]*)"`).exec(body);
  return m ? m[1] : "";
};
const flagged = (body, key) => new RegExp(`\\b${key}\\s*:\\s*true`).test(body);
const displayName = (body, fallback) => {
  const m = /display\s*:\s*\{[\s\S]*?\bname\s*:\s*"([^"]+)"/.exec(body);
  return m ? m[1] : fallback;
};
const localish = (u) => !u || LOCALISH.test(u);

// ---------------------------------------------------------------------------
// 9router
// ---------------------------------------------------------------------------

async function collect9router() {
  const tree = await gh("/repos/decolua/9router/git/trees/HEAD?recursive=1");
  const paths = [
    ...new Set(
      tree.tree
        .filter((e) => e.type === "blob" && e.path.startsWith("open-sse/providers/registry/") && e.path.endsWith(".js"))
        .map((e) => e.path),
    ),
  ].sort();

  const bodies = await mapLimit(paths, 16, async (p) => raw(REPOS["9router"], p));
  const out = [];

  for (let i = 0; i < paths.length; i++) {
    const body = bodies[i];
    if (!body) continue;
    const id = quoted(body, "id");
    if (!id) continue;

    const category = quoted(body, "category");
    const authType = quoted(body, "authType");
    const noAuth = flagged(body, "noAuth");
    const hasFree = flagged(body, "hasFree");
    const oauthFlow =
      flagged(body, "hasOAuth") || /\b(oauthUrl|authorizeUrl|tokenUrl|authUrl|deviceAuthUrl)\s*:/.test(body);

    let base = quoted(body, "baseUrl");
    if (!base) {
      const m = /transport\s*:\s*\{[\s\S]*?\bbaseUrl\s*:\s*"([^"]+)"/.exec(body);
      base = m ? m[1] : "";
    }
    const keyUrl = quoted(body, "apiKeyUrl") || quoted(body, "website");

    // --- filter ---
    if (category === "oauth" || authType === "oauth") continue;
    if (category === "webCookie" || authType === "cookie") continue;
    if (category === "free") {
      // Free lanes split two ways: keyless (opencode, mimo-free) and OAuth-backed
      // (gemini-cli, kiro). Only the keyless ones qualify here.
      if (oauthFlow || !noAuth) continue;
    } else if (category !== "apikey" && category !== "freeTier") {
      continue;
    }
    if (localish(base) && localish(keyUrl)) continue;
    // A localhost endpoint is not shareable with anyone else: exclude it even
    // when the entry also carries a public signup page.
    if (LOCALISH.test(base)) continue;
    if (!base && /localhost|127\.0\.0\.1/i.test(body)) continue;
    // A non-HTTP endpoint (devin:// ACP, local sockets) is not an API.
    if (base && !/^https?:\/\//i.test(base)) continue;
    // No endpoint but a signup page: keyless free services (veoaifree-web).
    if (!base && !keyUrl) continue;

    let tier = "api-key";
    if (category === "free") tier = "free";
    else if (category === "freeTier") tier = "free-tier";
    else if (hasFree) tier = "free-tier";

    out.push({
      id,
      name: displayName(body, id),
      base,
      key: keyUrl,
      tier,
      src: "9router",
    });
  }
  return out;
}

// ---------------------------------------------------------------------------
// OmniRoute
// ---------------------------------------------------------------------------

async function collectOmniRoute() {
  const tree = await gh("/repos/cheaper-inference/OmniRoute/git/trees/HEAD?recursive=1");
  const paths = [
    ...new Set(
      tree.tree
        .filter((e) => e.type === "blob" && e.path.includes("providers/registry") && e.path.endsWith(".ts"))
        .map((e) => e.path),
    ),
  ].sort();

  // Verified free-tier evidence: the catalogue OmniRoute maintains with a
  // source URL per claim.
  let evidence = {};
  const evRaw = await raw(REPOS.omniroute, "config/quality/free-catalog-evidence.json");
  if (evRaw) {
    try {
      for (const rec of JSON.parse(evRaw).evidence ?? []) {
        (evidence[rec.provider] ??= []).push(rec.modelId);
      }
    } catch {
      /* evidence is advisory; a malformed file must not break the sync */
    }
  }

  const bodies = await mapLimit(paths, 16, async (p) => raw(REPOS.omniroute, p));
  const out = [];

  for (let i = 0; i < paths.length; i++) {
    const body = bodies[i];
    if (!body) continue;
    const id = quoted(body, "id");
    if (!id) continue;

    const helper = body.includes("buildOpenAiCompatibleRegistryEntry");
    const authType = quoted(body, "authType") || (helper ? "apikey" : "");
    const authHeader = quoted(body, "authHeader") || (helper ? "bearer" : "");
    const executor = quoted(body, "executor");

    let base = quoted(body, "baseUrl");
    if (!base) {
      const m = /\bbaseUrl\s*:\s*"([^"]+)"/.exec(body);
      base = m ? m[1] : "";
    }

    // --- filter ---
    if (authType === "oauth") continue;
    // authHeader "cookie" is a browser session pasted as a header, not a key.
    if (authHeader === "cookie" || authHeader === "none") continue;
    if (executor.endsWith("-web") || id.endsWith("-web") || id.endsWith("-web-codex")) continue;
    if (authType !== "apikey" && authType !== "optional") continue;
    if (localish(base)) continue;

    let tier = "api-key";
    if (evidence[id]) tier = "free";
    else if (/id\s*:\s*"[^"]*-free"/.test(body)) tier = "free";
    else if (authType === "optional") tier = "free";

    out.push({
      id,
      name: displayName(body, id),
      base,
      key: "",
      tier,
      src: "omniroute",
    });
  }
  return out;
}

// ---------------------------------------------------------------------------
// merge + render
// ---------------------------------------------------------------------------

const RANK = { free: 0, "free-tier": 1, "api-key": 2 };

function merge(a, b) {
  const byId = new Map();
  for (const r of [...a, ...b]) {
    const prev = byId.get(r.id);
    if (!prev) {
      byId.set(r.id, { ...r });
      continue;
    }
    if (RANK[r.tier] < RANK[prev.tier]) prev.tier = r.tier;
    if (!prev.key && r.key) prev.key = r.key;
    if (!prev.base && r.base) prev.base = r.base;
    prev.src = prev.src.includes(r.src) ? prev.src : `${prev.src}+${r.src}`;
  }
  return [...byId.values()].sort((x, y) => RANK[x.tier] - RANK[y.tier] || x.id.localeCompare(y.id));
}

function renderMarkdown(rows, stamp) {
  const head = [
    "# NOX9GO — Free & API-key providers",
    "# Sources (upstream, not local):",
    "#   - decolua/9router            open-sse/providers/registry/*.js",
    "#   - cheaper-inference/OmniRoute open-sse/config/providers/registry/*/index.ts",
    "#                                 + config/quality/free-catalog-evidence.json",
    `# Generated: ${stamp}`,
    "# Filter: API-key auth only. OAuth-only, web-cookie and localhost providers excluded.",
    "# Format (TSV): id <TAB> name <TAB> api-base-url <TAB> key-url <TAB> tier <TAB> source",
    "# tier: free = keyless or verified free tier | free-tier = API key with free quota | api-key = API key required",
    "",
  ];
  const body = rows.map(
    (r) => [r.id, r.name, r.base, r.key, r.tier, r.src].join("\t"),
  );
  return head.join("\n") + body.join("\n") + "\n";
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

const check = process.argv.includes("--check");
const stamp = new Date().toISOString().slice(0, 10);

const [rows9, rowsO] = await Promise.all([collect9router(), collectOmniRoute()]);
const rows = merge(rows9, rowsO);

await mkdir(dirname(OUT_MD), { recursive: true });
await writeFile(OUT_MD, renderMarkdown(rows, stamp), "utf8");

const snapshot = rows.map((r) => `${r.id}\t${r.name}\t${r.base}\t${r.key}\t${r.tier}`).join("\n");

let previous = null;
if (existsSync(BASELINE)) previous = await readFile(BASELINE, "utf8");

if (check) {
  if (previous === null) {
    console.log(`Baseline created (${rows.length} providers). Nothing to compare against yet.`);
  } else {
    const seen = new Set(previous.split("\n").map((l) => l.split("\t")[0]));
    const fresh = rows.filter((r) => !seen.has(r.id));
    if (fresh.length === 0) {
      console.log(`No new providers. (${rows.length} in catalogue)`);
    } else {
      console.log(`${fresh.length} NEW provider(s):\n`);
      for (const r of fresh) {
        console.log(`  ${r.name}`);
        console.log(`    ${r.id}`);
        console.log(`    ${r.base}`);
        if (r.key) console.log(`    key: ${r.key}`);
        console.log(`    tier: ${r.tier} (${r.src})`);
        console.log("");
      }
    }
  }
}

await writeFile(BASELINE, snapshot + "\n", "utf8");
console.log(`docs/providers_free.md written: ${rows.length} providers (${stamp})`);
