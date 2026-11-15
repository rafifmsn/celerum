# Spec 01: Backend Pipeline, SQLite Store, and Diagnostics

Detailed engineering requirements for Phases 1, 2, and 3 covering GitHub Issues `#9`, `#10`, `#11`, `#12`, and `#13` (storage layer).

## 1. Scoring & Clustering Fixes (Issue #10)

### 1.1 Tier Weight Inversion (`internal/score/score.go`)
`PickRepresentative` treats Tier 1 as the highest-priority institutional tier (`art.FeedTier < best.FeedTier`), but `ScoreCluster` currently computes `tierSum += float64(art.FeedTier) * WeightTier`, awarding Tier 3 three times the points of Tier 1.
Replace the linear multiplication with inverse tier multipliers matching the UI (`tmp/sources.html`):
1. Tier 1: `3.0 * WeightTier` (or direct weight `3.0`)
2. Tier 2: `2.0 * WeightTier` (or direct weight `2.0`)
3. Tier 3+: `1.0 * WeightTier` (or direct weight `1.0`)

### 1.2 Word-Boundary Keyword Boosts (`internal/score/score.go`)
`ScoreCluster` currently uses `strings.Contains(combinedText, kw)`, causing false positives such as `"sec"` matching `"second"` or `"sector"`.
Tokenize the combined title and description into lowercase alphanumeric words (or reuse `cluster.CleanText`) and match keywords against whole tokens (supporting multi-word phrases via token sequence or word-boundary regex `\b...\b`).

### 1.3 Unique Source Count & Time Decay (`internal/score/score.go`)
Count unique `art.FeedURL` values in a cluster rather than raw `len(articles)` when computing `clusterSizeScore` so a single feed publishing five repetitive updates cannot trigger a breaking alert on its own.
Apply steeper recency decay (`DecayPerHour` or exponential half-life decay across the 3-hour window) so stale clusters drop below fresh velocity spikes.

### 1.4 Local TF-IDF / BM25 Bounded Clustering (`internal/cluster/cluster.go`)
Unweighted Jaccard similarity paired with single-linkage `DisjointSet.Union` allows transitive chaining (A merges with B, B merges with C, even when A and C share almost nothing).
Keep the inverted shingle index for fast candidate pruning (and count total possible pairs vs. evaluated pairs for the Overview telemetry card), then:
1. Compute corpus IDF weights across the active window's tokenized articles.
2. Score candidate pairs using weighted TF-IDF / BM25 cosine similarity (weighting title tokens 2x over description tokens).
3. Enforce a bounded linkage check before merging clusters: verify similarity against the target cluster's representative/medoid (or cap maximum cluster diameter) to prevent snowballing mega-clusters.

## 2. Transport, ETag State, and Adaptive Polling (Issue #11)

### 2.1 Always Lookup Persisted `feed_state` (`internal/engine/engine.go`)
In `Engine.PollFeeds`, remove the `if hasBuffered` guard around `e.store.GetFeedState(fc.URL)`.
Always query SQLite for `etag` and `last_modified` so restarts and empty buffers still send `If-None-Match` and `If-Modified-Since`.

### 2.2 uTLS ClientHello Mimicry & Backoff Headers (`internal/feed/feed.go`)
Wrap the feed `http.RoundTripper` using `github.com/refraction-networking/utls` with `tls.HelloChrome_Auto` so Cloudflare and publisher WAFs do not block Go's default TLS fingerprint.
Inspect `Retry-After` (seconds or HTTP-date) and `Cache-Control: max-age=N` on responses (`200`, `304`, `429`, `503`) and return a suggested `NextPollAfter time.Duration` on `FetchResult`.

### 2.3 Non-Blocking Per-Feed Adaptive Scheduler (`internal/engine/engine.go`)
Remove `time.Sleep(jitter)` from `Engine.Run`'s `select` loop.
Maintain per-feed `next_poll_at` and `consecutive_304`:
1. When a feed returns `200 OK` with new articles, reset its interval to base `poll_interval` (plus +/-10% jitter).
2. When a feed returns `304 Not Modified` or zero new articles on consecutive polls, back off its interval (`poll_interval * 1.5^consecutive_304`, capped at `30m`), or honor `Retry-After` / `max-age` if larger.
3. Tick a lightweight scheduler check every few seconds to poll only feeds whose `next_poll_at <= now` concurrently.

## 3. Rich Feed Parsing, SQLite Persistence & Dual Retention (Issues #12, #13)

### 3.1 Image Extraction & HTML Sanitization (`internal/feed/feed.go`, `pkg/model/article.go`)
Extend `model.Article` with `ImageURL string`, `Category string`, and `FeedName string`.
During `Ingester.Parse`:
1. Extract `ImageURL` in priority order: `<media:content url="...">`, `<media:thumbnail url="...">`, `<enclosure url="..." type="image/...">`, or the first `<img src="...">` inside description/content HTML.
2. Strip all HTML tags and unescape HTML entities (`html.UnescapeString`) in `Title` and `Description`.
3. If `Description == Title`, set `Description = ""` so the Raw Feed UI cleanly renders a title-only row (`tmp/raw-feed.html` items 2 and 5) instead of dropping the article.

### 3.2 Raw vs. Clustered Filtering Boundary
Do not drop short titles or title-only items before persisting to SQLite `articles`.
Only exclude blocklisted URLs during ingestion.
Apply minimum token-count filtering (e.g., require `>= 4` meaningful tokens across title + description) when selecting articles for `ClusterArticles` so short noisy blurbs do not pollute clusters while breaking one-line flashes remain visible in Raw RSS Feed.

### 3.3 SQLite Schema & YAML Bootstrap (`internal/store/store.go`)
Make SQLite the single runtime source of truth:
1. `sources`: `id`, `name`, `url` (UNIQUE), `category`, `tier`, `enabled`, `etag`, `last_modified`, `last_polled_at`, `last_status`, `last_latency_ms`, `next_poll_at`, `consecutive_304`.
2. `settings`: key-value JSON or structured single-row config storing `engine`, `keywords`, `enrichment`, `llm`, and `dispatch` settings.
3. `articles`: `id` (PRIMARY KEY), `feed_url`, `feed_name`, `feed_tier`, `category`, `title`, `description`, `url`, `image_url`, `published_at`, `ingested_at`.
4. `clusters`: `cluster_hash` (PRIMARY KEY), `representative_id`, `title`, `summary`, `enriched`, `score`, `tier`, `source_count`, `category`, `sources_json`, `status` (`active`, `buffered`, `dispatched`), `created_at`, `updated_at`, `dispatched_at`.
5. `webhook_retries`: existing table plus `last_error TEXT` so `tmp/dispatch.html` can display the exact upstream HTTP error message.

On startup, if `sources` and `settings` are empty and `celerum.yaml` exists, seed SQLite from `celerum.yaml` (with `.env` expansion).
If `celerum.yaml` is absent, seed default settings so the binary boots cleanly into the web UI.

### 3.4 Dual Retention Auto-Purge (`internal/store/store.go`)
To prevent intense RSS feeds from bloating SQLite, `PruneOldRecords` must enforce both time and volume bounds after each poll cycle:
1. Delete `articles` and `clusters` older than `7 days`.
2. Delete oldest `articles` per `feed_url` exceeding `500` rows, and cap total `articles` at `10,000` rows.

## 4. Non-Destructive `celerum doctor` (Issue #9)

Create `internal/doctor/doctor.go` callable from both `celerum doctor [-c config.yaml] [--send]` and `POST /api/doctor`:
1. **Stage 1 (Config & Env)**: Verify active configuration syntax and fail strictly if `enrichment.scraper` (`jina` or `firecrawl`), `llm.enabled`, or enabled dispatchers lack required API keys/tokens.
2. **Stage 2 (SQLite Storage)**: Verify WAL mode, schema tables, write/read transaction, and file size.
3. **Stage 3 (Feed Reachability)**: Concurrently test configured enabled feeds, reporting reachable count, `200 OK` vs. `304 Not Modified` counts, and any failing URLs.
4. **Stage 4 (Article Scraper)**: If scraper != `none`, run `scraper.FetchContent` against a real article URL fetched in Stage 3; fail strictly if HTTP status != 200 or content is empty.
5. **Stage 5 (LLM Synthesis)**: If LLM is enabled, call `summarizer.Summarize` on the real article from Stage 3/4 and report latency and model name; fail strictly on error without fallback masking.
6. **Stage 6 (Alert Dispatchers)**: Verify Telegram Bot token via `getMe` and `getChat` (and webhook URL reachability); only send a live message if `--send` (or `"send": true` in API) is set.
Remove `--once` from `cmd/celerum/main.go` and remove `once` parameter from `Engine.Run`.
