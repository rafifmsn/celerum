# System Limitations & Edge Cases

This document outlines known operational boundaries for Celerum.

## Challenge-Gated Feeds

Although Celerum negotiates Chrome-aligned TLS fingerprints via `utls` (`HelloChrome_Auto`) with conditional `ETag`/`Last-Modified` and `Retry-After` handling, RSS/Atom feeds protected by interactive JavaScript Turnstile or Under-Attack interstitials cannot be parsed without an external proxy or RSS bridge.

## Storage & Single-Node Scope

- **Single-Instance Embedded SQLite:**
  Celerum targets single-binary / single-container deployments backed by SQLite in WAL mode with dual retention pruning (7 days TTL, max 500 articles per feed, max 10000 articles total). Multi-replica horizontal scaling sharing one database file over network filesystems (such as NFS) is not supported.
- **Short Headlines in Raw Feed vs Clustering:**
  `FilterAndCap` retains short breaking headlines in SQLite so operators see them in Raw Feed, while `FilterClusterableArticles` excludes entries with fewer than 4 non-stopword tokens from algorithmic clustering to prevent spurious merges unless all active articles fall below the threshold.
