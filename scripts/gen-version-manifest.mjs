#!/usr/bin/env bun
// Regenerate version.json from the built release binaries.
//
// The manifest used to carry a single downloadUrl and sha256, which described
// the Windows executable. The updater took them on every platform when no
// platform-specific entry existed, so a Linux or macOS install could download
// and swap in a binary it cannot run. version.json now names one asset and one
// digest per platform, and the updater refuses a platform it does not list.
//
// Usage, after `make cross` and with the release assets in the working
// directory or the paths given:
//
//   bun scripts/gen-version-manifest.mjs --version 1.0.3 \
//     --repo Pacoaldev/Nox9Go --notes "..." \
//     nox9go-windows-amd64.exe nox9go-linux-amd64 nox9go-linux-arm64 \
//     nox9go-darwin-amd64 nox9go-darwin-arm64
//
// The manifest is written to version.json and the matching SHA256SUMS.txt is
// refreshed beside it.
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";

const ROOT = join(import.meta.dir, "..");

function parseArgs(argv) {
  const args = { version: "", repo: "Pacoaldev/Nox9Go", notes: "", files: [], check: false };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === "--version") args.version = argv[++i];
    else if (arg === "--repo") args.repo = argv[++i];
    else if (arg === "--notes") args.notes = argv[++i];
    else if (arg === "--check") args.check = true;
    else if (!arg.startsWith("--")) args.files.push(arg);
  }
  return args;
}

// assetPlatform maps a release asset name to the manifest key the updater
// looks up (platformKey in internal/updater).
function assetPlatform(name) {
  const match = /nox9go-(\w+)-(\w+)(?:\.exe)?$/.exec(name);
  if (!match) return null;
  return `${match[1]}_${match[2]}`;
}

const args = parseArgs(process.argv.slice(2));
const version = args.version || readFileSync(join(ROOT, "VERSION"), "utf8").trim();

// --check validates the committed manifest without touching it. CI runs this:
// an asset list that lost a platform, or a VERSION bump that never reached the
// manifest, is exactly what would serve an install the wrong binary.
if (args.check) {
  let manifest;
  try {
    manifest = JSON.parse(readFileSync(join(ROOT, "version.json"), "utf8"));
  } catch (err) {
    console.error(`error: version.json is not readable JSON: ${err.message}`);
    process.exit(1);
  }
  const problems = [];
  if (manifest.latestVersion !== version) {
    problems.push(
      `latestVersion is ${manifest.latestVersion ?? "(missing)"}, VERSION says ${version}`,
    );
  }
  const platforms = Object.keys(manifest.downloadUrls ?? {});
  if (!platforms.length) problems.push("downloadUrls is empty");
  for (const [key, url] of Object.entries(manifest.downloadUrls ?? {})) {
    if (!/^https:\/\//.test(url)) problems.push(`${key}: download URL is not https`);
    if (!url.endsWith(`.exe`) && key.startsWith("windows_")) {
      problems.push(`${key}: Windows asset does not end in .exe`);
    }
    if (url.endsWith(`.exe`) && !key.startsWith("windows_")) {
      problems.push(`${key}: non-Windows asset ends in .exe`);
    }
    const digest = manifest.sha256?.[key];
    if (!/^[0-9a-f]{64}$/.test(digest ?? "")) {
      problems.push(`${key}: sha256 is missing or not 64 lowercase hex characters`);
    }
  }
  if (problems.length) {
    for (const problem of problems) console.error(`error: ${problem}`);
    process.exit(1);
  }
  console.log(`version.json OK for ${version}: ${platforms.join(", ")}`);
  process.exit(0);
}

if (!args.files.length) {
  console.error("error: list at least one release asset");
  process.exit(2);
}

const downloadUrls = {};
const sha256 = {};
const sums = [];
for (const file of args.files) {
  const name = basename(file);
  const key = assetPlatform(name);
  if (!key) {
    console.error(`error: cannot tell the platform of ${name}; expected nox9go-<os>-<arch>[.exe]`);
    process.exit(2);
  }
  if (downloadUrls[key]) {
    console.error(`error: two assets claim ${key}`);
    process.exit(2);
  }
  const digest = createHash("sha256").update(readFileSync(file)).digest("hex");
  downloadUrls[key] = `https://github.com/${args.repo}/releases/download/v${version}/${name}`;
  sha256[key] = digest;
  sums.push(`${digest}  ${name}`);
}

// downloadUrl is the legacy single-asset key. It is kept pointing at the
// Windows build because installs that predate per-platform URLs still read it.
// The updater never trusts it for a platform it does not list, so it cannot
// steer a Linux or macOS install onto a Windows binary.
const manifest = {
  latestVersion: version,
  downloadUrl: downloadUrls.windows_amd64 ?? "",
  downloadUrls,
  sha256,
  releaseNotes: args.notes,
};

writeFileSync(join(ROOT, "version.json"), `${JSON.stringify(manifest, null, 2)}\n`);
writeFileSync(join(ROOT, "SHA256SUMS.txt"), `${sums.join("\n")}\n`);
console.log(`wrote version.json for ${version}: ${Object.keys(downloadUrls).join(", ")}`);
