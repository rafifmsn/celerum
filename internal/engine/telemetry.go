package engine

import (
	"math"
	"strings"
	"time"

	"celerum/internal/cluster"
	"celerum/internal/store"
	"celerum/pkg/model"
)

// TimeSeriesPoint represents one bucket in the 3-hour window spline charts.
type TimeSeriesPoint struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

// TopClusterBrief represents a compact cluster item in Overview cards and the briefing panel.
type TopClusterBrief struct {
	ID          string             `json:"id"`
	Score       float64            `json:"score"`
	Tier        int                `json:"tier"`
	SourceCount int                `json:"source_count"`
	LeadSource  string             `json:"lead_source"`
	Headline    string             `json:"headline"`
	Summary     string             `json:"summary"`
	Category    string             `json:"category"`
	Enriched    bool               `json:"enriched"`
	UpdatedAt   time.Time          `json:"updated_at"`
	Sources     []model.SourceInfo `json:"sources"`
}

// OverviewMetrics matches all Bento telemetry cards in Overview.
type OverviewMetrics struct {
	ActiveFeedsCount      int               `json:"active_feeds_count"`
	WindowStartUTC        string            `json:"window_start_utc"`
	WindowEndUTC          string            `json:"window_end_utc"`
	WindowDuration        string            `json:"window_duration"`
	WindowRawArticles     int               `json:"window_raw_articles"`
	WindowClusters        int               `json:"window_clusters"`
	PrunedPairRatio       float64           `json:"pruned_pair_ratio"`
	Conditional304Ratio   float64           `json:"conditional_304_ratio"`
	ShingledCount         int               `json:"shingled_count"`
	RawFetchedCount       int               `json:"raw_fetched_count"`
	DedupEfficiencyPct    float64           `json:"dedup_efficiency_pct"`
	Feeds304Count         int               `json:"feeds_304_count"`
	FeedsTotalActive      int               `json:"feeds_total_active"`
	CacheHitPct           float64           `json:"cache_hit_pct"`
	BreakingClustersCount int               `json:"breaking_clusters_count"`
	ActiveClustersCount   int               `json:"active_clusters_count"`
	BreakingRatePct       float64           `json:"breaking_rate_pct"`
	IndexPrunedPct        float64           `json:"index_pruned_pct"`
	LLMEnrichedPct        float64           `json:"llm_enriched_pct"`
	JaccardReductionPct   float64           `json:"jaccard_reduction_pct"`
	ScraperSuccessPct     float64           `json:"scraper_success_pct"`
	RSSFallbackPct        float64           `json:"rss_fallback_pct"`
	ScraperLabel          string            `json:"scraper_label"`
	HourlyEfficiency      []TimeSeriesPoint `json:"hourly_efficiency_series"`
	DispatchedTotal       int               `json:"dispatched_total"`
	ImmediateAlertPct     float64           `json:"immediate_alert_pct"`
	DeliverySLAPct        float64           `json:"delivery_sla_pct"`
	OnTimeCount           int               `json:"on_time_count"`
	RetryQueueCount       int               `json:"retry_queue_count"`
	TopKBufferedCount     int               `json:"top_k_buffered_count"`
	TopKLimit             int               `json:"top_k_limit"`
	HourlyDispatch        []TimeSeriesPoint `json:"hourly_dispatch_series"`
	TopClusters           []TopClusterBrief `json:"top_clusters"`
	NextFlushInSeconds    int               `json:"next_flush_in_seconds"`
	BreakingThreshold     float64           `json:"breaking_threshold"`
}

// KeywordMatchCount reports a boost keyword and how many active clusters matched it.
type KeywordMatchCount struct {
	Keyword string `json:"keyword"`
	Matches int    `json:"matches"`
}

// ClustersResponse holds ranked clusters and sidebar distribution metadata.
type ClustersResponse struct {
	Clusters            []store.ClusterRecord `json:"clusters"`
	TotalWindowClusters int                   `json:"total_window_clusters"`
	BreakingCount       int                   `json:"breaking_count"`
	HeartbeatCount      int                   `json:"heartbeat_count"`
	BelowThresholdCount int                   `json:"below_threshold_count"`
	TierCounts          map[string]int        `json:"tier_counts"`
	BoostKeywords       []KeywordMatchCount   `json:"boost_keywords"`
	BreakingThreshold   float64               `json:"breaking_threshold"`
	WindowDuration      string                `json:"window_duration"`
}

// NextFlushInSeconds returns seconds remaining until the next heartbeat flush.
func (e *Engine) NextFlushInSeconds() int {
	e.mu.Lock()
	rem := e.cfg.Engine.FlushDur - time.Since(e.lastFlushTime)
	e.mu.Unlock()
	if rem < 0 {
		return 0
	}
	return int(rem.Seconds())
}

// GetOverviewTelemetry computes Overview Bento metrics over the active sliding window.
func (e *Engine) GetOverviewTelemetry(tierFilter string) OverviewMetrics {
	now := time.Now().UTC()

	e.mu.Lock()
	winDur := e.cfg.Engine.WindowDur
	if winDur <= 0 {
		winDur = 3 * time.Hour
	}
	winLabel := e.cfg.Engine.WindowDuration
	if winLabel == "" {
		winLabel = "3h"
	}
	breakingThresh := e.cfg.Engine.BreakingThreshold
	if breakingThresh <= 0 {
		breakingThresh = 12.0
	}
	topKLimit := e.cfg.Engine.TopK
	if topKLimit <= 0 {
		topKLimit = 5
	}
	scraperCfg := strings.ToLower(strings.TrimSpace(e.cfg.Enrichment.Scraper))
	stats := e.lastClusterStats
	onTime := e.onTimeDispatches
	remFlush := e.cfg.Engine.FlushDur - time.Since(e.lastFlushTime)
	e.mu.Unlock()

	if remFlush < 0 {
		remFlush = 0
	}

	windowStart := now.Add(-winDur)
	out := OverviewMetrics{
		WindowStartUTC:     windowStart.Format("15:04 UTC"),
		WindowEndUTC:       now.Format("15:04 UTC"),
		WindowDuration:     winLabel,
		TopKLimit:          topKLimit,
		NextFlushInSeconds: int(remFlush.Seconds()),
		BreakingThreshold:  breakingThresh,
		ScraperLabel:       formatScraperLabel(scraperCfg),
	}

	if e.store == nil {
		return out
	}

	sources, _ := e.store.ListSources("")
	tierNum := 0
	switch strings.TrimSpace(tierFilter) {
	case "1":
		tierNum = 1
	case "2":
		tierNum = 2
	case "3":
		tierNum = 3
	}

	for _, s := range sources {
		if !s.Enabled {
			continue
		}
		if tierNum > 0 && s.Tier != tierNum {
			continue
		}
		out.FeedsTotalActive++
		if s.LastStatus == 304 {
			out.Feeds304Count++
		}
	}
	out.ActiveFeedsCount = out.FeedsTotalActive
	if out.FeedsTotalActive > 0 {
		out.Conditional304Ratio = float64(out.Feeds304Count) / float64(out.FeedsTotalActive)
		out.CacheHitPct = round2(out.Conditional304Ratio * 100.0)
	}

	rawArticles, _ := e.store.GetRecentArticles(windowStart)
	var filteredArticles []model.Article
	for _, a := range rawArticles {
		if tierNum > 0 && a.FeedTier != tierNum {
			continue
		}
		filteredArticles = append(filteredArticles, a)
	}
	out.RawFetchedCount = len(filteredArticles)
	out.WindowRawArticles = len(filteredArticles)

	shingledArticles := cluster.FilterClusterableArticles(filteredArticles, minClusterTokens)
	out.ShingledCount = len(shingledArticles)
	if out.RawFetchedCount > 0 {
		out.DedupEfficiencyPct = round2(float64(out.ShingledCount) / float64(out.RawFetchedCount) * 100.0)
	}

	allWindowClusters, _ := e.store.QueryClusters("all", tierFilter, "", windowStart, "score")
	out.ActiveClustersCount = len(allWindowClusters)
	out.WindowClusters = len(allWindowClusters)

	var enrichedCount, scraperOKCount, fallbackCount, immediateCount int
	var dispatchedClusters []store.ClusterRecord
	for _, c := range allWindowClusters {
		if c.Score >= breakingThresh {
			out.BreakingClustersCount++
		}
		if c.Status == "buffered" {
			out.TopKBufferedCount++
		}
		if c.Status == "dispatched" {
			dispatchedClusters = append(dispatchedClusters, c)
			if c.DispatchMode == "breaking" {
				immediateCount++
			}
		}
		if c.Enriched {
			enrichedCount++
		} else if c.Status == "dispatched" {
			fallbackCount++
		}
		if c.ScraperUsed == "jina" || c.ScraperUsed == "firecrawl" || c.ScraperUsed == "direct" {
			scraperOKCount++
		}
	}

	if out.ActiveClustersCount > 0 {
		out.BreakingRatePct = round2(float64(out.BreakingClustersCount) / float64(out.ActiveClustersCount) * 100.0)
	}

	// Pruning efficiency from last clustering pass (or calculate if zero)
	if stats.TotalPossiblePairs > 0 {
		out.PrunedPairRatio = stats.PrunedRatio
		out.IndexPrunedPct = round1(stats.PrunedRatio * 100.0)
	} else if len(shingledArticles) > 1 {
		_, computedStats := cluster.ClusterArticlesWithStats(shingledArticles, e.cfg.Engine.SimilarityThreshold)
		out.PrunedPairRatio = computedStats.PrunedRatio
		out.IndexPrunedPct = round1(computedStats.PrunedRatio * 100.0)
	}

	if out.ShingledCount > 0 {
		red := (1.0 - float64(out.WindowClusters)/float64(out.ShingledCount)) * 100.0
		if red < 0 {
			red = 0
		}
		out.JaccardReductionPct = round1(red)
	}

	evalDenom := len(dispatchedClusters)
	if evalDenom > 0 {
		out.LLMEnrichedPct = round1(float64(enrichedCount) / float64(evalDenom) * 100.0)
		out.ScraperSuccessPct = round1(float64(scraperOKCount) / float64(evalDenom) * 100.0)
		out.RSSFallbackPct = round1(float64(fallbackCount) / float64(evalDenom) * 100.0)
	}

	retries, _ := e.store.ListAllWebhookRetries("")
	out.RetryQueueCount = len(retries)
	out.DispatchedTotal = len(dispatchedClusters)
	if out.DispatchedTotal > 0 {
		out.ImmediateAlertPct = round1(float64(immediateCount) / float64(out.DispatchedTotal) * 100.0)
	}
	if onTime < out.DispatchedTotal {
		onTime = out.DispatchedTotal
	}
	out.OnTimeCount = onTime
	if out.OnTimeCount+out.RetryQueueCount > 0 {
		out.DeliverySLAPct = round1(float64(out.OnTimeCount) / float64(out.OnTimeCount+out.RetryQueueCount) * 100.0)
	} else {
		out.DeliverySLAPct = 100.0
	}

	// Build 8-bucket time series over windowDuration for the two spline charts
	const buckets = 8
	bucketDur := winDur / buckets
	out.HourlyEfficiency = make([]TimeSeriesPoint, buckets)
	out.HourlyDispatch = make([]TimeSeriesPoint, buckets)
	for i := 0; i < buckets; i++ {
		bStart := windowStart.Add(time.Duration(i) * bucketDur)
		bEnd := bStart.Add(bucketDur)
		label := bStart.Format("15:04")

		var artCnt int
		for _, a := range filteredArticles {
			if !a.PublishedAt.Before(bStart) && a.PublishedAt.Before(bEnd) {
				artCnt++
			}
		}
		var dispCnt int
		for _, d := range dispatchedClusters {
			t := d.DispatchedAt
			if t.IsZero() {
				t = d.UpdatedAt
			}
			if !t.Before(bStart) && t.Before(bEnd) {
				dispCnt++
			}
		}
		out.HourlyEfficiency[i] = TimeSeriesPoint{Label: label, Value: float64(artCnt)}
		out.HourlyDispatch[i] = TimeSeriesPoint{Label: label, Value: float64(dispCnt)}
	}

	// Top clusters (up to max(3, topKLimit))
	limit := topKLimit
	if limit < 3 {
		limit = 3
	}
	for i, c := range allWindowClusters {
		if i >= limit {
			break
		}
		out.TopClusters = append(out.TopClusters, TopClusterBrief{
			ID:          c.ClusterHash,
			Score:       round1(c.Score),
			Tier:        c.Tier,
			SourceCount: c.SourceCount,
			LeadSource:  c.LeadSource,
			Headline:    c.Title,
			Summary:     c.Summary,
			Category:    c.Category,
			Enriched:    c.Enriched,
			UpdatedAt:   c.UpdatedAt,
			Sources:     c.Sources,
		})
	}

	return out
}

// GetClustersView returns ranked clusters and scoring distribution metadata for Intelligence Feed.
func (e *Engine) GetClustersView(filter, tierFilter, sortBy, search string) (ClustersResponse, error) {
	e.mu.Lock()
	winDur := e.cfg.Engine.WindowDur
	if winDur <= 0 {
		winDur = 3 * time.Hour
	}
	winLabel := e.cfg.Engine.WindowDuration
	if winLabel == "" {
		winLabel = "3h"
	}
	breakingThresh := e.cfg.Engine.BreakingThreshold
	if breakingThresh <= 0 {
		breakingThresh = 12.0
	}
	boostKws := append([]string(nil), e.cfg.Keywords.Boost...)
	scorer := e.scorer
	e.mu.Unlock()

	since := time.Now().UTC().Add(-winDur)
	allInWindow, err := e.store.QueryClusters("all", "all", "", since, "score")
	if err != nil {
		return ClustersResponse{}, err
	}

	resp := ClustersResponse{
		TotalWindowClusters: len(allInWindow),
		TierCounts: map[string]int{
			"all": len(allInWindow),
			"1":   0,
			"2":   0,
			"3":   0,
		},
		BreakingThreshold: breakingThresh,
		WindowDuration:    winLabel,
	}

	kwCounts := make(map[string]int, len(boostKws))
	for _, c := range allInWindow {
		if c.Score >= breakingThresh {
			resp.BreakingCount++
		} else if c.Score >= 8.0 {
			resp.HeartbeatCount++
		} else {
			resp.BelowThresholdCount++
		}
		switch c.Tier {
		case 1:
			resp.TierCounts["1"]++
		case 2:
			resp.TierCounts["2"]++
		default:
			resp.TierCounts["3"]++
		}

		var arts []model.Article
		arts = append(arts, model.Article{Title: c.Title, Description: c.Summary})
		for _, s := range c.Sources {
			arts = append(arts, model.Article{Title: s.Title})
		}
		for _, mk := range scorer.MatchedKeywords(arts) {
			kwCounts[mk]++
		}
	}

	for _, kw := range boostKws {
		clean := strings.ToLower(strings.TrimSpace(kw))
		if clean != "" {
			resp.BoostKeywords = append(resp.BoostKeywords, KeywordMatchCount{
				Keyword: clean,
				Matches: kwCounts[clean],
			})
		}
	}

	filtered, err := e.store.QueryClusters("all", tierFilter, search, since, sortBy)
	if err != nil {
		return ClustersResponse{}, err
	}
	for _, c := range filtered {
		if filter == "breaking" && c.Score < breakingThresh {
			continue
		}
		if filter == "tier1" && c.Tier != 1 {
			continue
		}
		resp.Clusters = append(resp.Clusters, c)
	}
	if resp.Clusters == nil {
		resp.Clusters = []store.ClusterRecord{}
	}

	return resp, nil
}

func formatScraperLabel(s string) string {
	switch s {
	case "jina":
		return "Jina Scraped"
	case "firecrawl":
		return "Firecrawl Scraped"
	case "direct":
		return "Direct Scraped"
	default:
		return "Scraper Disabled"
	}
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
