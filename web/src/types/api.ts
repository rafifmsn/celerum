export interface ClusterSourceItem {
  name: string
  tier: number
  url: string
  title: string
  published_at: string
}

export interface HourlyPoint {
  label: string
  value: number
}

export interface OverviewClusterSummary {
  id: string
  score: number
  tier: number
  source_count: number
  lead_source: string
  headline: string
  summary: string
  category: string
  enriched: boolean
  updated_at: string
  sources: ClusterSourceItem[]
}

export interface OverviewMetrics {
  active_feeds_count: number
  window_start_utc: string
  window_end_utc: string
  window_duration: string
  next_flush_in_seconds: number
  breaking_threshold: number
  window_raw_articles: number
  window_clusters: number
  pruned_pair_ratio: number
  conditional_304_ratio: number
  shingled_count: number
  raw_fetched_count: number
  dedup_efficiency_pct: number
  feeds_304_count: number
  feeds_total_active: number
  cache_hit_pct: number
  breaking_clusters_count: number
  active_clusters_count: number
  breaking_rate_pct: number
  index_pruned_pct: number
  llm_enriched_pct: number
  jaccard_reduction_pct: number
  scraper_success_pct: number
  rss_fallback_pct: number
  scraper_label: string
  hourly_efficiency_series: HourlyPoint[]
  dispatched_total: number
  immediate_alert_pct: number
  delivery_sla_pct: number
  on_time_count: number
  retry_queue_count: number
  top_k_buffered_count: number
  top_k_limit: number
  hourly_dispatch_series: HourlyPoint[]
  top_clusters: OverviewClusterSummary[]
}

export interface ClusterRecord {
  cluster_hash: string
  representative_id: string
  title: string
  summary: string
  enriched: boolean
  score: number
  tier: number
  source_count: number
  category: string
  lead_source: string
  sources: ClusterSourceItem[]
  status: string
  dispatch_mode: string
  dispatch_targets: string
  scraper_used: string
  created_at: string
  updated_at: string
  dispatched_at: string
}

export interface KeywordBoostStat {
  keyword: string
  matches: number
}

export interface ClustersResponse {
  clusters: ClusterRecord[]
  total_window_clusters: number
  breaking_count: number
  heartbeat_count: number
  below_threshold_count: number
  tier_counts: Record<string, number>
  boost_keywords: KeywordBoostStat[]
  breaking_threshold: number
  window_duration: string
}

export interface ArticleRecord {
  id: string
  feed_url: string
  feed_name: string
  feed_tier: number
  category: string
  title: string
  description: string
  url: string
  image_url: string
  published_at: string
  ingested_at: string
}

export interface ArticlesResponse {
  articles: ArticleRecord[]
  total_count: number
  page: number
  limit: number
  categories: string[]
  sources: string[]
}

export interface SourceRecord {
  id: string
  name: string
  url: string
  category: string
  tier: number
  enabled: boolean
  etag: string
  last_modified: string
  last_polled_at: string
  last_status: number
  last_latency_ms: number
  next_poll_at: string
  consecutive_304: number
  last_error: string
}

export interface SourcesResponse {
  sources: SourceRecord[]
  total_count: number
  tier1_count: number
  tier2_count: number
  tier3_count: number
  etag_cached_count: number
  updated_200_count: number
}

export interface WebhookRetryRecord {
  id: number
  target_type: string
  target_url: string
  headline: string
  attempts: number
  last_error: string
  next_retry_at: string
  created_at: string
}

export interface DispatchResponse {
  next_flush_in_seconds: number
  top_k: number
  active_targets: number
  retries: WebhookRetryRecord[]
  buffered: ClusterRecord[]
  history: ClusterRecord[]
}

export interface SettingsPayload {
  engine: {
    poll_interval: string
    window_duration: string
    flush_interval: string
    max_articles_per_feed: number
    similarity_threshold: number
    breaking_threshold: number
    top_k: number
  }
  keywords: {
    boost: string[]
    blocklist: string[]
  }
  enrichment: {
    scraper: string
    jina_api_key: string
    firecrawl_api_key: string
  }
  llm: {
    enabled: boolean
    provider: string
    base_url: string
    model: string
    api_key: string
    language: string
    system_prompt: string
  }
  dispatch: {
    telegram: {
      enabled: boolean
      bot_token: string
      chat_id: string
    }
    webhook: {
      enabled: boolean
      url: string
      headers: Record<string, string>
    }
  }
}

export interface DoctorStageResult {
  stage: number
  name: string
  status: string
  summary: string
  detail?: string
  latency_ms: number
}

export interface DoctorReport {
  ok: boolean
  checked_at: string
  stages: DoctorStageResult[]
}
