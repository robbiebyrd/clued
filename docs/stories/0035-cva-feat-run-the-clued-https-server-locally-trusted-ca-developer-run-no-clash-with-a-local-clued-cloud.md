---
id: 0035-cva
title: 'Run the clued HTTPS server locally: trusted CA, developer run, no clash with a local clued cloud'
type: feat
status: complete
priority: "2"
effort: M
created: "2026-10-14T14:15:13.117Z"
updated: "2026-10-14T14:15:19.354Z"
started: "2026-10-14T14:15:13.117Z"
completed: "2026-10-14T14:15:19.354Z"
tags:
  - server
  - setup
  - tls
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  plans:
    - ["0006-tsw", "included"]
progress:
  "1":
    status: complete
  "2":
    status: complete
  "3":
    status: complete
  "4":
    status: complete
  "5":
    status: complete
---

# Run the clued HTTPS server locally: trusted CA, developer run, no clash with a local clued cloud

## Problem Statement

`/clued-setup` started the Docker stack, but its private CA was only usable through `serverCaFile`: browsers and curl rejected the server, nothing installed the CA in the machine's trust stores, `--force` replaced the CA so every trusting client broke, and the CA could sign for any name. There was no way for a developer to run the server from a checkout with the same certificates, and nothing checked that a local clued cloud, an existing stack or the daemon held the ports, or that setup was about to overwrite another server's config.

**Impact:** Trusting an unconstrained CA vouches for every host; a developer could not run the HTTPS server locally without hand-made certificates; the cloud and the stack could be confused or one's config overwritten.

## Steps

### 1: Name-constrained, reusable CA

`server/stack` makes the CA with critical DNS and IP name constraints for the stack's hosts and keeps it across `--force` while it covers every host and has 30 days left; `--new-ca` replaces it. The result reports `ca` and `ca_fingerprint`.

### 2: server trust

`mind-palace server trust [--remove] [--dry-run]` installs the CA in the macOS login keychain, the Linux system store and the NSS databases of Chrome and Firefox, or the Windows user Root store; root steps run with `sudo -n` or come back as pending shell lines; it then verifies `/health` with the system roots.

### 3: Setup and uninstall

`/clued-setup` checks for an existing stack, a local clued cloud (8080) and the daemon (8085) before `server init`, offers `server trust`, documents the developer run from a source checkout, and asks before replacing another `serverUrl` (with a backup). `/clued-uninstall` and `hooks/uninstall --purge-server` untrust the CA before deleting it and say the plugin itself must be uninstalled with `/plugin uninstall clued@clued`.

### 4: server run never relays

`server run` clears `serverUrl`, `serverToken` and `serverCaFile` of the machine's config so a machine whose client points at it does not make the server relay to itself.

### 5: Docs and the cloud

docs/server.md, PROTOCOL.md and the READMEs describe trust, `--new-ca`, the developer run and coexistence; clued-cloud's compose file gets the fixed project name `clued-cloud` and a README section on running next to a local server.

## Acceptance Criteria

- [x] VERIFY: `go test ./server/stack/ ./cli/ ./plugin/` passes: a leaf for a host outside the constraints fails to verify, `--force` keeps the CA, trust plans per platform, pending root steps, uninstall untrusts
- [x] [MANUAL] In a Linux container `server trust` on a stack's CA makes `curl https://localhost:8443/health` succeed without `--cacert`, and `server trust --remove` makes it fail again
- [x] VERIFY: `MIND_PALACE_TEST_MONGODB_URI=mongodb://127.0.0.1:27017 go test -race ./...` passes, with gofmt and vet clean

## Work Log

### 2026-10-14T14:15:19.306Z - Name-constrained CA kept across --force, --new-ca, server trust (keychain, system store, NSS, Windows), the setup conflict check and developer run, untrust on purge, a plugin-uninstall reminder, server run ignoring the client's server keys; verified in a Linux container (curl fails before trust, 200 after, fails again after --remove); full suite with MongoDB green.
