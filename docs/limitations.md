# System Limitations & Edge Cases

This document outlines known operational boundaries for Celerum.

## Challenge-Gated Feeds

Although Celerum negotiates Chrome-aligned TLS fingerprints via `utls` (`HelloChrome_Auto`) with conditional `ETag`/`Last-Modified` and `Retry-After` handling, RSS/Atom feeds protected by interactive JavaScript Turnstile or Under-Attack interstitials cannot be parsed without an external proxy or RSS bridge.

## Storage & Single-Node Scope

- **Single-Instance Embedded SQLite:**
  Celerum targets single-binary / single-container deployments backed by SQLite in WAL mode with dual retention pruning (7 days TTL, max 500 articles per feed, max 10000 articles total). Multi-replica horizontal scaling sharing one database file over network filesystems (such as NFS) is not supported.
- **Short Headlines in Raw Feed vs Clustering:**
  `FilterAndCap` retains short breaking headlines in SQLite so operators see them in Raw Feed, while `FilterClusterableArticles` excludes entries with fewer than 4 non-stopword tokens from algorithmic clustering to prevent spurious merges unless all active articles fall below the threshold.

## MCP Server & Access Boundaries

- **No Built-In Auth on `/mcp`:**
  Like `/api/*`, `/mcp` has no built-in authentication middleware and must remain restricted to localhost or trusted networks unless placed behind an authenticated reverse proxy. Pass `--mcp-read-only` to `celerum run` (or `--read-only` to `celerum mcp`) whenever untrusted content or wider network access is involved.
- **Process Isolation in `celerum mcp`:**
  Stdio mode (`celerum mcp`) shares embedded SQLite state (`articles`, `clusters`, `sources`, and non-destructive `doctor` diagnostics) without starting a polling loop.
  Live polling schedules and in-memory engine state remain active inside `celerum run`.
- **Cross-Process Sync Latency:**
  Source mutations made via stdio `celerum mcp` (or `celerum import`) bump `config_revision` in SQLite and sync to a running `celerum run` instance on its 2-second scheduler tick rather than instantaneously.
