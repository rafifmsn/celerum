# Celerum Implementation Task Plan

Master orchestration checklist and architectural guide for implementing the Celerum overhaul across backend, persistence, diagnostics, and the embedded web dashboard.

## Context & Prerequisites

Before writing code, review the open GitHub issues (`gh issue list --state open` and `gh issue view 9` through `14`) alongside the static HTML mockups in `tmp/*.html` and the specification documents in `tmp/specs/`.
All work must stay on a single branch without creating commits unless explicitly requested by the user.

1. [tmp/specs/01-backend-and-store.md](file:///home/rafif/Documents/Dev/Github/celerum/tmp/specs/01-backend-and-store.md): Core pipeline fixes, SQLite schema, YAML bootstrap, dual retention pruning, and `celerum doctor`.
2. [tmp/specs/02-api-and-telemetry.md](file:///home/rafif/Documents/Dev/Github/celerum/tmp/specs/02-api-and-telemetry.md): Overview Bento telemetry formulas, REST API routes, and Server-Sent Events (SSE) contract.
3. [tmp/specs/03-frontend-and-ui-nuances.md](file:///home/rafif/Documents/Dev/Github/celerum/tmp/specs/03-frontend-and-ui-nuances.md): Translating `tmp/*.html` into the embedded React SPA, full-viewport layout, mobile icon navigation fix, and deferred UI elements.

## Phase 1: Core Pipeline Fixes & Adaptive Transport (Issues #10, #11)

- [ ] **Fix Tier Weighting (`internal/score/score.go`)**: Invert `FeedTier` weight calculation so Tier 1 contributes the highest score (3.0x for Tier 1, 2.0x for Tier 2, 1.0x for Tier 3) matching `PickRepresentative`.
- [ ] **Token-Boundary Keyword Matching (`internal/score/score.go`)**: Replace `strings.Contains` substring matching with tokenized word-boundary matching so short keywords like `"sec"` do not match `"second"` or `"sector"`.
- [ ] **Unique Source Scoring & Recency Decay (`internal/score/score.go`)**: Count unique feed sources (`FeedURL`) in `ScoreCluster` instead of raw `len(articles)` to prevent a single noisy feed from inflating cluster scores, and strengthen time decay.
- [ ] **Local TF-IDF / BM25 Bounded Clustering (`internal/cluster/cluster.go`)**: Upgrade unweighted Jaccard + unbounded single-linkage Union-Find to local TF-IDF or BM25 cosine similarity with a complete-linkage or medoid guard so unrelated stories cannot chain into mega-clusters.
- [ ] **Always Load Persisted Conditional Headers (`internal/engine/engine.go`)**: Query `e.store.GetFeedState(fc.URL)` on every poll regardless of whether `e.feedBuffers[fc.URL]` is empty.
- [ ] **uTLS Chrome Transport & Cache Header Parsing (`internal/feed/feed.go`)**: Wrap feed HTTP transport with `refraction-networking/utls` (Chrome ClientHello + HTTP/2/ALPN negotiation) and parse `Retry-After` and `Cache-Control: max-age` on responses (including `429` and `503`).
- [ ] **Per-Feed Adaptive Scheduler (`internal/engine/engine.go`)**: Replace the single global poll ticker and blocking `time.Sleep(jitter)` with a non-blocking per-feed scheduler (`next_poll_at` with randomized jitter, exponential backoff on consecutive `304 Not Modified` or empty polls, and reset to base `poll_interval` when new items arrive).

## Phase 2: SQLite Persistence, Runtime Config & Rich RSS Parsing (Issues #12, #13)

- [ ] **Extend Article Model (`pkg/model/article.go`)**: Add `ImageURL`, `Category`, and `FeedName` fields to `model.Article`.
- [ ] **Rich RSS/Atom Parsing (`internal/feed/feed.go`)**: Extract `ImageURL` from `media:content`, `media:thumbnail`, `enclosure` (image MIME types), or first `<img>` in description/content; sanitize HTML tags and decode HTML entities in `Description`.
- [ ] **Raw vs. Clustered Filtering Boundary (`internal/feed/feed.go`, `internal/engine/engine.go`)**: Persist all valid parsed articles (including short breaking headlines and entries where `Description` is empty or identical to `Title`) to SQLite for the Raw RSS Feed view, applying minimum token-count noise filtering only when feeding the clustering pipeline.
- [ ] **SQLite Schema Expansion (`internal/store/store.go`)**: Add tables for `sources` (with `category`, `tier`, `enabled`, `etag`, `last_modified`, `last_status`, `last_latency_ms`, `next_poll_at`), `settings` (runtime engine/LLM/enrichment/dispatch config), `articles` (raw ingested items), and `clusters` (active window clusters and dispatched history).
- [ ] **YAML Bootstrap & Import/Export (`internal/config`, `internal/store`)**: Seed SQLite `sources` and `settings` from `celerum.yaml` and `.env` on first boot when SQLite tables are empty; use SQLite as the single runtime source of truth thereafter, and provide YAML import/export helpers.
- [ ] **Dual Retention Auto-Purge (`internal/store/store.go`)**: Implement automatic pruning on `articles` and `clusters` using both a time window (default 7 days) and a volume cap (max 500 articles per feed and 10,000 global rows) so high-volume RSS feeds never bloat SQLite.

## Phase 3: Non-Destructive Diagnostics & Model Discovery (Issues #9, #13)

- [ ] **Live Model Discovery (`internal/ai/ai.go`)**: Add `ListModels(ctx, provider, baseURL, apiKey)` querying `/v1/models` (or Ollama `/api/tags`) to return live available model IDs.
- [ ] **Strict Provider Validation (`internal/config`, `internal/ai`, `internal/enrich`)**: Reject configurations or UI saves when `jina`, `firecrawl`, or an enabled LLM provider is selected without its required API key instead of silently falling back.
- [ ] **Reusable Doctor Package (`internal/doctor/doctor.go`)**: Implement the 6-stage non-destructive diagnostic runner (1. Config/Env, 2. SQLite WAL/schema, 3. Feed reachability + ETag/304 check, 4. Scraper test on a real article URL, 5. LLM auth + live test summary, 6. Dispatcher `getMe`/`getChat` or webhook check with optional `--send`) returning structured `PASS`/`FAIL` stage results.
- [ ] **CLI `celerum doctor` & Flag Cleanup (`cmd/celerum/main.go`, `internal/engine/engine.go`)**: Add `celerum doctor [-c config.yaml] [--send]` subcommand and remove `run --once` along with `once` scaffolding in `Engine.Run`.

## Phase 4: Engine Telemetry & HTTP + SSE Server (Issue #13 Backend)

- [ ] **Pipeline Telemetry Counters (`internal/engine`, `internal/cluster`, `internal/feed`)**: Track rolling 3-hour window and hourly series metrics required by the Overview Bento grid (raw articles, shingled count, inverted-index pruned pair ratio, 304 vs. 200 feed counts, breaking vs. active cluster counts, scraper/LLM enrichment vs. RSS fallback rates, and dispatch SLA/retry counts).
- [ ] **REST & SSE Server (`internal/server/server.go`)**: Implement `net/http` endpoints (`/api/overview`, `/api/clusters`, `/api/articles`, `/api/sources`, `/api/config/yaml`, `/api/dispatch`, `/api/settings`, `/api/models/discover`, `/api/doctor`, `/api/dispatch/flush`) and `/api/events` SSE broadcaster.
- [ ] **Unified Binary Serving (`cmd/celerum/main.go`)**: Start the HTTP dashboard server alongside `Engine.Run` in `celerum run` (configurable via `-addr :8080` or `CELERUM_ADDR`), serving embedded static assets from `web/dist`.

## Phase 5: Embedded React + TypeScript + Tailwind SPA (Issue #13 Frontend)

- [ ] **Scaffold `web/` SPA**: Initialize React + TypeScript + Vite + Tailwind CSS + `shadcn/ui` (Lucide icons) matching the dark monochrome Bento aesthetic in `tmp/*.html`.
- [ ] **Full-Viewport Shell & Mobile Icon Navigation**: Convert the fixed mockup shell into a full-screen responsive layout (`min-h-screen w-full`) and render top navigation items as **icons on mobile viewports (`< md`)** with text labels on desktop (`>= md`) so small screens never overflow or break layout.
- [ ] **Overview Page (`tmp/overview.html`)**: Wire all Bento cards, polar semi-circular 304 cache gauge with gradient arc and thumb dot, dynamic barcode progress strips, SVG `<pattern>` vertical-comb area fill clipped under the hourly spline wave (`d3-shape` `curveMonotoneX` or `Recharts` `<AreaChart>`), top-3 compact cluster cards, and right-hand AI Synthesis Briefing panel to `/api/overview` and SSE.
- [ ] **Intelligence Feed Page (`tmp/intelligence.html`)**: Wire ranked cluster list, velocity/tier filters, expandable source article drawers, and right-hand scoring distribution sidebar to `/api/clusters`.
- [ ] **Raw RSS Feed Page (`tmp/raw-feed.html`)**: Wire paginated raw article stream with Category filter, Source filter, search input, **Show/Hide Thumbnails toggle**, and compact title-only row rendering when description is empty or equal to title.
- [ ] **Sources & Categories Page (`tmp/sources.html`)**: Wire tier summary cards, feed table (live HTTP status, ETag/304 badge, latency, `next_poll_at`, enable switch), Add/Edit/Delete modals, and Import/Export YAML actions.
- [ ] **Dispatch Queue & History Page (`tmp/dispatch.html`)**: Wire two-tab view (`Pending & Retrying` with immediate retry/cancel actions + buffered heartbeat queue, and `Dispatched History`) plus `Flush Buffer Now`.
- [ ] **Settings & Doctor Page (`tmp/settings.html`)**: Wire LLM provider/key + `Fetch Models` searchable selector, Crawler provider/key with strict inline validation error, scoring intervals + keyword boost tag editor, and live 6-stage `Run Doctor` checklist.

## Phase 6: Deferred / Post-Core Features (Unimplemented Until Core Passes)

Do not implement the items below until Phases 1 through 5 are complete, verified, and approved.

- [ ] **Deferred 1 - MCP Server (Issue #14)**: No internal Celerum subsystem depends on MCP. Once core backend and UI are complete, wrap `Engine`, `Store`, and `Doctor` using the official Go MCP SDK (`github.com/modelcontextprotocol/go-sdk`) over stdio (`celerum mcp`) and Streamable HTTP (`/mcp`).
- [ ] **Deferred 2 - Overview Right-Panel LLM Chat / SQL Tool-Calling (`tmp/overview.html` lines 585-597)**: The bottom-right input bar (`"Filter or inspect cluster..."` with `+` and `send` buttons) in the Overview AI Synthesis Briefing panel represents an interactive LLM query/tool-call surface. For the core release, wire the text input as a local cluster filter or render the `send` button with a `"Coming soon"` tooltip; do not build an LLM SQL/tool-calling agent in the initial pass.
