#!/usr/bin/env bun
// Predict Codecov's patch percentage locally, before the push.
//
// Codecov's `patch` status compares the lines a commit touches against the
// coverage profile CI uploads, and it does that without asking whether a
// touched line is executable. A whitespace-only commit therefore scored 50%
// on two "changed lines" and failed a 67.9% target, while every real change
// in the same series passed. That failure is invisible until the push lands,
// which is exactly the wrong time to learn about it.
//
// This script answers the same question against Go's own profile, so the
// number exists before the push. It differs from Codecov in one deliberate
// way: a changed line that no cover block contains is not counted at all.
// Go only instruments statements, so struct alignment, comments, imports and
// a trailing newline carry no coverage either way — counting them as "missed"
// is the bug that produced the 50%.
//
// Usage:
//
//   bun scripts/diff-coverage.mjs                    # base origin/main, target 67.9
//   bun scripts/diff-coverage.mjs --worktree         # include uncommitted changes
//   bun scripts/diff-coverage.mjs --base HEAD~3      # any git range
//
// Exit codes: 0 pass, 1 below target, 2 error.
import { execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const args = { base: "origin/main", target: 67.9, worktree: false, keep: false };
for (let i = 2; i < process.argv.length; i++) {
  const arg = process.argv[i];
  if (arg === "--base") args.base = process.argv[++i];
  else if (arg === "--target") args.target = Number(process.argv[++i]);
  else if (arg === "--worktree") args.worktree = true;
  else if (arg === "--keep") args.keep = true;
  else {
    console.error(`unknown argument: ${arg}`);
    process.exit(2);
  }
}

const git = (...argv) => execFileSync("git", argv, { encoding: "utf8", maxBuffer: 64 << 20 });

// The module prefix is what cover profiles print; strip it to compare against
// git's repo-relative paths.
const moduleName = /^module\s+(\S+)/m.exec(readFileSync("go.mod", "utf8"))[1].trim();

// changedLines maps repo-relative .go path -> Set of line numbers on the new
// side of the diff. -U0 keeps the hunks to the lines that actually changed,
// and a hunk with no new lines is a pure deletion, which adds nothing to cover.
function changedLines(range) {
  const out = git("diff", "--unified=0", "--diff-filter=AM", "--no-color", range, "--");
  const files = new Map();
  let current = null;
  for (const line of out.split("\n")) {
    if (line.startsWith("+++ b/")) {
      current = line.slice(6);
      if (!current.endsWith(".go")) current = null;
      else if (!files.has(current)) files.set(current, new Set());
      continue;
    }
    if (!current) continue;
    const hunk = /^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@/.exec(line);
    if (!hunk) continue;
    const start = Number(hunk[1]);
    const count = hunk[2] === undefined ? 1 : Number(hunk[2]);
    for (let i = 0; i < count; i++) files.get(current).add(start + i);
  }
  return files;
}

// Untracked files never appear in a diff, so --worktree reads them whole: a
// brand new file is entirely "changed" by definition.
function untrackedLines(files) {
  const out = git("status", "--porcelain");
  for (const line of out.split("\n")) {
    if (!line.startsWith("?? ")) continue;
    const path = line.slice(3).trim();
    if (!path.endsWith(".go")) continue;
    const text = readFileSync(path, "utf8");
    const set = new Set();
    text.split("\n").forEach((_, index) => set.add(index + 1));
    files.set(path, set);
  }
}

const range = args.worktree ? args.base : `${args.base}...HEAD`;
const files = changedLines(range);
if (args.worktree) untrackedLines(files);

if (files.size === 0) {
  console.log(`diff-coverage: no Go files changed against ${args.base} — nothing to measure.`);
  process.exit(0);
}

// Only packages that contain a changed file need running; the whole suite would
// spend most of its time proving that untouched code is still covered.
const packages = [
  ...new Set(
    [...files.keys()].map((path) => {
      const dir = path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : ".";
      return `./${dir}`;
    }),
  ),
];
const testable = packages.filter((pkg) => {
  try {
    execFileSync("go", ["list", "-f", "{{.Name}}", pkg], { encoding: "utf8" });
    return true;
  } catch {
    return false; // no Go package there (e.g. a directory of fixtures)
  }
});

// testEnv is the environment `go test` gets, and it is deliberately not ours.
//
// Bun loads the project's .env automatically, so a script run with bun inherits
// every secret the gateway owns — CLOUDFLARE_ACCOUNT_ID, ROUTER_MASTER_KEY, the
// OAuth client secrets — and passes them to every child process. That is not
// only a leak: it changes test results. The dashboard validation suite proves
// that a Cloudflare key without an account id is refused, and with
// CLOUDFLARE_ACCOUNT_ID in the environment the handler finds one and the test
// fails, which is a red CI that never reaches GitHub and makes this script
// useless. Only what Go itself needs survives.
const KEEP_VARS = [
  "PATH",
  "PATHEXT",
  "SYSTEMROOT",
  "SystemRoot",
  "windir",
  "COMSPEC",
  "HOME",
  "USERPROFILE",
  "APPDATA",
  "LOCALAPPDATA",
  "TEMP",
  "TMP",
  "TMPDIR",
  "GOROOT",
  "GOPATH",
  "GOCACHE",
  "GOMODCACHE",
  "GOFLAGS",
  "GOOS",
  "GOARCH",
  "CGO_ENABLED",
];
const testEnv = Object.fromEntries(
  KEEP_VARS.filter((name) => process.env[name] !== undefined).map((name) => [name, process.env[name]]),
);

const work = mkdtempSync(join(tmpdir(), "diffcov-"));
const profile = join(work, "coverage.out");
try {
  execFileSync(
    "go",
    ["test", "-count=1", "-covermode=atomic", `-coverprofile=${profile}`, ...testable],
    { stdio: ["ignore", "ignore", "pipe"], env: testEnv },
  );
} catch (err) {
  // Go's own output is the only useful thing here: a compile error and a
  // failing assertion look identical without it.
  const stderr = err.stderr ? err.stderr.toString() : err.message;
  console.error(stderr.trim().split("\n").slice(-15).join("\n"));
  console.error("diff-coverage: go test failed; run the suite before measuring coverage.");
  process.exit(2);
}

// blocks[file] = [{start, end, count}], in profile order.
const blocks = new Map();
for (const line of readFileSync(profile, "utf8").split("\n")) {
  if (line === "" || line.startsWith("mode:")) continue;
  const match = /^(.+?):(\d+)\.\d+,(\d+)\.\d+ \d+ (\d+)$/.exec(line);
  if (!match) continue;
  const path = match[1].replace(`${moduleName}/`, "");
  if (!blocks.has(path)) blocks.set(path, []);
  blocks.get(path).push({ start: Number(match[2]), end: Number(match[3]), count: Number(match[4]) });
}

let hit = 0;
let miss = 0;
const rows = [];
for (const [path, lines] of [...files].sort((a, b) => a[0].localeCompare(b[0]))) {
  const fileBlocks = blocks.get(path) ?? [];
  let fileHit = 0;
  let fileMiss = 0;
  for (const line of lines) {
    for (const block of fileBlocks) {
      if (line < block.start || line > block.end) continue;
      if (block.count > 0) fileHit++;
      else fileMiss++;
      break;
    }
  }
  hit += fileHit;
  miss += fileMiss;
  rows.push({ path, hit: fileHit, miss: fileMiss });
}

const measured = hit + miss;
if (measured === 0) {
  console.log(`diff-coverage: ${files.size} Go file(s) changed, no instrumented line among them.`);
  console.log("  Formatting, comments and declarations carry no coverage either way, so");
  console.log("  there is nothing for a patch target to measure. Passing.");
  if (!args.keep) rmSync(work, { recursive: true, force: true });
  process.exit(0);
}

const pct = (hit / measured) * 100;
const width = Math.max(...rows.map((r) => r.path.length));
for (const row of rows) {
  if (row.hit + row.miss === 0) continue;
  const value = ((row.hit / (row.hit + row.miss)) * 100).toFixed(0).padStart(3);
  console.log(`  ${row.path.padEnd(width)}  ${value}%  ${row.hit}/${row.hit + row.miss} lines`);
}

console.log(
  `\ndiff-coverage: ${pct.toFixed(2)}% of ${measured} changed instrumented line(s) covered (target ${args.target}%)`,
);
if (args.keep) console.log(`profile kept at ${profile}`);
else rmSync(work, { recursive: true, force: true });

if (pct + 1e-9 < args.target) {
  console.log("FAIL: Codecov's patch status would flag this push.");
  process.exit(1);
}
console.log("PASS");
