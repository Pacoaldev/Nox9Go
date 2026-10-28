# Security policy

NOX9GO is a local gateway: it holds provider credentials, proxy tokens and the
dashboard password for the machine it runs on. If you find a way to reach or
change any of that which this document does not already describe, report it
privately.

## Reporting a vulnerability

Use GitHub's private reporting:

**https://github.com/Pacoaldev/Nox9Go/security/advisories/new**

Do not open a public issue, and do not post the details in a discussion or a
pull request. A public issue is a working exploit for anyone watching the
repository.

### What to include

The faster a report can be reproduced, the faster it is fixed. Where you can:

- The version (`nox9go version`) and the platform.
- How the gateway was started: the `make build` binary, the tray app, a
  container, or `go run`. Whether it was bound to loopback or to `0.0.0.0`.
- The exact request or series of steps, with credentials replaced by
  placeholders.
- What you could do that you should not have been able to do, and what the
  attacker gains — read a key, change settings, reach the admin API, escape
  the dashboard.
- Any relevant log lines (`nox9go logs`), trimmed of secrets.

## How reports are handled

This is a small project maintained by one person, so no response-time
commitment is made here. Expect this shape:

1. The report is acknowledged and reproduced.
2. A fix and a release are prepared, and the advisory is drafted with you.
3. You are credited in the advisory and the changelog unless you ask not to be.

Please give a reasonable window for a fix to land before publishing details
yourself.

## Scope

In scope:

- The gateway and admin API (`/api/*`) — authentication, authorisation, and
  separation between client inference keys and administration.
- Credential handling: how provider keys, OAuth tokens and the dashboard
  password are stored, logged, exported and embedded in the binary.
- The updater: anything that would let a malicious or compromised release
  manifest install a binary this project did not build.
- The dashboard SPA: script injection through provider or model names, or
  anything that leaks a stored credential to the page.

Out of scope:

- A gateway deliberately bound to a public interface with a weak or
  operator-shared password, and no reverse proxy.
- Reports from automated scanners without a reproduction.
- Vulnerabilities in a provider's own service that this project merely calls.
- Anything that needs an attacker who can already read the `DATA_DIR` or write
  to it as the same user. That is game over by definition.

## If a credential leaks

If you have pushed a real provider key, OAuth secret, session cookie or
dashboard password to a fork, a snippet, a log paste or a public issue:

1. **Revoke it first.** Rotating the value in this repository does not help:
   the old one is still in the history you pushed, in every clone and in the
   provider's logs. Revoke at the provider, then issue a new one.
2. Assume the value was used. Check the provider's usage and billing for calls
   you did not make.
3. Rewrite history only if it helps someone — a fork that was never public and
   never cloned usually does not need it. Deleting the branch does not delete
   the objects from other clones.
4. Report it here if the leak was in this repository's own history, so the
   release can be rotated and the advisory published.
