# System Limitations & Edge Cases

This document outlines known operational boundaries, scraper constraints, and configuration semantics across Celerum.

## 1. Enrichment Scraper Trade-Offs (`internal/enrich`)

Celerum supports four `enrichment.scraper` modes: `none`, `direct`, `jina`, and `firecrawl`.

- **Direct Scraper (`scraper: "direct"`):**
  Performs raw HTTP GET requests followed by HTML tag stripping and truncation to 2000 characters.
  It performs no domain-level rate limiting, IP rotation, or headless JavaScript execution.
  Publishers behind Cloudflare Bot Management, Akamai, Datadome, or client-side React/Next.js hydration may return HTTP 403/429 errors or empty skeleton markup.
- **Managed Scrapers (`jina` and `firecrawl`):**
  Using `jina` (`r.jina.ai`) or `firecrawl` (`https://api.firecrawl.dev/v2/scrape`, truncated to 3000 characters) delegates JS rendering and markdown extraction externally, at the cost of third-party API latency and token/credit quotas.
- **Strict API Key Validation:**
  Selecting `jina` requires a non-empty `enrichment.jina_api_key`, and selecting `firecrawl` requires a non-empty `enrichment.firecrawl_api_key`.
  Likewise, enabling `llm` with any non-`ollama` provider rejects empty `llm.api_key` values during config validation, `PUT /api/settings`, and `doctor` diagnostics rather than deferring failure until alert time.
  When external scrapers fail during live dispatch despite valid keys, Celerum falls back gracefully to RSS descriptions and dispatches with `enriched: false` if LLM synthesis fails.

## 2. Paywalled Content & Protected Feeds

- **Paywall Boilerplate Pollution:**
  Scrapers hitting subscription gates ingest teaser notices or login prompts alongside headlines.
  Setting `enrichment.scraper: "none"` avoids full-page extraction entirely and limits LLM input cleanly to publisher-supplied RSS titles and descriptions.
- **Challenge-Gated RSS / Atom Feeds:**
  Although Celerum negotiates Chrome-aligned TLS fingerprints via `utls` (`HelloChrome_Auto`) with conditional `ETag`/`Last-Modified` and `Retry-After` handling, feeds protected by interactive JavaScript Turnstile/Under-Attack interstitials cannot be parsed without an external proxy or RSS bridge.

## 3. SQLite Single Source of Truth vs YAML Drift

- **Bootstrap Semantics:**
  Starting `celerum run` with `-c celerum.yaml` seeds or refreshes SQLite (`sources` and `settings`) via `BootstrapConfig` / `ImportConfig`.
  Starting `celerum run` without `-c` resumes exclusively from the existing SQLite database (default `data/celerum.db`).
- **UI Edits Do Not Rewrite Local YAML Automatically:**
  Changes made through `/api/settings`, `/api/sources`, or the web Settings and Sources pages update SQLite immediately and hot-reload the running engine.
  The `celerum.yaml` file on disk is not automatically rewritten, so keeping `-c celerum.yaml` on future restarts can overwrite UI changes unless you export an updated YAML from `GET /api/config/yaml` (or the Sources page Export YAML action).

## 4. Storage & Single-Node Scope

- **Single-Instance Embedded SQLite:**
  Celerum targets single-binary / single-container deployments backed by SQLite in WAL mode with dual retention pruning (7 days TTL, max 500 articles per feed, max 10000 articles total).
  Multi-replica horizontal scaling sharing one database file over network filesystems (such as NFS) is not supported.
- **Short Headlines in Raw Feed vs Clustering:**
  `FilterAndCap` retains short breaking headlines in SQLite so operators see them in Raw Feed, while `FilterClusterableArticles` excludes entries with fewer than 4 non-stopword tokens from algorithmic clustering to prevent spurious merges unless all active articles fall below the threshold.
