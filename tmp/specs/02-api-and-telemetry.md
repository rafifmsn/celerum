# Spec 02: Engine Telemetry, REST API, and SSE Contract

Bridges the Go engine (`internal/engine`, `internal/store`, `internal/doctor`, `internal/ai`) to the embedded frontend (`tmp/*.html` -> `web/`).

## 1. Overview Bento Telemetry Mapping (`GET /api/overview`)

Every card on `tmp/overview.html` maps to deterministic counters tracked in `Engine` and `Store` over the active sliding window (default `3h`, with optional `tier` filter query param `?tier=all|1|2|3`):

| Bento Component (`tmp/overview.html`) | JSON Fields Required | Calculation / Source in Celerum Engine |
| :--- | :--- | :--- |
| **Hero Subtitle & Window Pills** | `active_feeds_count`, `window_start_utc`, `window_end_utc`, `window_duration` | Count of enabled `sources`; `[now - window_duration, now]` formatted in UTC (`HH:MM UTC`). |
| **Fanned Insight Cards (3 cards)** | `window_raw_articles`, `window_clusters`, `pruned_pair_ratio`, `conditional_304_ratio` | Card 1: raw articles collapsed into `window_clusters`. Card 2: `1 - (evaluated_pairs / total_possible_pairs)` from inverted shingle index. Card 3: `feeds_304_count / feeds_total_active`. |
| **Bento 1: Processed Articles** | `shingled_count`, `raw_fetched_count`, `dedup_efficiency_pct` | `shingled_count` (articles passing token filter into shingling), `raw_fetched_count` (total ingested in window), `dedup_efficiency_pct = (shingled_count / raw_fetched_count) * 100`. |
| **Bento 2: Conditional Poll Cache Gauge** | `feeds_304_count`, `feeds_total_active`, `cache_hit_pct` | Enabled feeds whose last poll returned `304 Not Modified` divided by `feeds_total_active`. Drives the SVG semi-circular arc gauge. |
| **Bento 3: Breaking Velocity Rate** | `breaking_clusters_count`, `active_clusters_count`, `breaking_rate_pct` | Clusters in active window with `score >= breaking_threshold` (12.0) divided by `active_clusters_count`. |
| **Wide Card 1: Shingle & Scrape Efficiency** | `index_pruned_pct`, `llm_enriched_pct`, `jaccard_reduction_pct`, `scraper_success_pct`, `rss_fallback_pct`, `hourly_efficiency_series[]` | `jaccard_reduction_pct = (1 - window_clusters / shingled_count) * 100`; percentage of dispatched/evaluated clusters enriched by LLM and scraper vs. RSS fallback; 6-12 bucket time series for the SVG wave. |
| **Wide Card 2: Dispatch & Delivery Cadence** | `dispatched_total`, `immediate_alert_pct`, `delivery_sla_pct`, `on_time_count`, `retry_queue_count`, `top_k_buffered_count`, `top_k_limit`, `hourly_dispatch_series[]` | Dispatched count in window, % triggered immediately via breaking threshold vs. heartbeat flush, `on_time_count / (on_time_count + retry_queue_count)`, current `webhook_retries` count, and buffered Top-K count. |
| **Bottom 3 Compact Cards & Right Briefing Panel** | `top_clusters[]` (`id`, `score`, `tier`, `source_count`, `lead_source`, `headline`, `summary`, `updated_at`) | Top-K ranked clusters from the current sliding window. The #1 cluster populates the right-hand "AI Synthesis Briefing" context block, and the top 3 populate the score breakdown list and bottom 3 cards. |

## 2. REST API Endpoints (`internal/server`)

All endpoints return `application/json` (except YAML export and SSE).

1. `GET /api/overview?tier=all`: Returns the full Overview Bento payload above.
2. `GET /api/clusters?filter=all|breaking|tier1&tier=all|1|2|3&sort=score|recent&q=`: Returns ranked clusters for `intelligence.html` along with sidebar distribution counts (`breaking_count`, `heartbeat_count`, `below_threshold_count`, `tier_counts`, and active `boost_keywords` with match counts).
3. `GET /api/articles?category=&source=&q=&page=1&limit=20`: Returns paginated raw ingested articles from SQLite `articles` for `raw-feed.html`, plus distinct `categories[]` and `sources[]` for filter dropdowns and `total_count`.
4. `GET /api/sources?q=`: Returns configured sources with live poll telemetry (`last_status`, `etag_status`, `last_latency_ms`, `next_poll_at`) and tier summary counts (`tier1_count`, `tier2_count`, `tier3_count`) for `sources.html`.
5. `POST /api/sources`, `PUT /api/sources/{id}`, `DELETE /api/sources/{id}`: Create, update (including `enabled` toggle), or delete a feed source in SQLite and dynamically update the active scheduler.
6. `GET /api/config/yaml` and `POST /api/config/yaml`: Export current SQLite sources + settings as a `celerum.yaml` file download, or import an uploaded YAML document into SQLite.
7. `GET /api/dispatch?q=`: Returns `next_flush_in_seconds`, `top_k`, `retries[]` (from `webhook_retries` with `id`, `target_type`, `attempts`, `next_retry_at`, `headline`, `last_error`), `buffered[]` (unflushed Top-K clusters in the digest queue), and `history[]` (dispatched clusters log) for `dispatch.html`.
8. `POST /api/dispatch/flush`: Triggers an immediate heartbeat buffer flush (`Flush Buffer Now` button on `overview.html` and `dispatch.html`).
9. `POST /api/dispatch/retries/{id}/retry` and `DELETE /api/dispatch/retries/{id}`: Immediately retry or cancel a queued webhook retry item (`dispatch.html`).
10. `GET /api/settings` and `PUT /api/settings`: Fetch or update runtime settings (LLM provider/model/key, Crawler provider/key, poll/flush/window intervals, breaking score, boosted keywords). `PUT /api/settings` enforces strict API key validation and returns `400 Bad Request` with field-level error messages if `jina`, `firecrawl`, or enabled LLM lacks a key.
11. `POST /api/models/discover`: Accepts `{"provider", "base_url", "api_key"}` and calls `ai.ListModels` to return `{"models": [...]}` for the `Fetch Models` button on `settings.html`.
12. `POST /api/doctor`: Executes the 6-stage `doctor.Run` check and returns stage-by-stage `PASS`/`FAIL` status, summary text, latency, and failure detail for `settings.html`.

## 3. Server-Sent Events Stream (`GET /api/events`)

Streams real-time JSON events so the UI `"Live"` indicator pulses and views update without manual page reloads:
1. `event: poll_complete`: Emitted after feed polls finish; triggers refresh of Overview metrics, Raw Feed count, and Source statuses.
2. `event: cluster_updated`: Emitted when cluster evaluation updates active clusters or dispatches a breaking alert.
3. `event: dispatch_updated`: Emitted when a payload is dispatched or a webhook retry succeeds/fails.
