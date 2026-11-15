package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"celerum/internal/ai"
	"celerum/internal/cluster"
	"celerum/internal/config"
	"celerum/internal/dispatch"
	"celerum/internal/enrich"
	"celerum/internal/feed"
	"celerum/internal/score"
	"celerum/internal/store"
	"celerum/pkg/model"
)

const (
	maxAdaptiveBackoff = 30 * time.Minute
	minClusterTokens   = 4
)

type feedSchedule struct {
	nextPollAt     time.Time
	consecutive304 int
}

// Engine coordinates the end-to-end RSS intelligence pipeline.
type Engine struct {
	cfg         *config.Config
	store       *store.Store
	ingester    *feed.Ingester
	scorer      *score.Scorer
	scraper     enrich.Scraper
	summarizer  ai.Summarizer
	dispatchers []dispatch.Dispatcher

	mu               sync.Mutex
	feedBuffers      map[string][]model.Article
	schedules        map[string]*feedSchedule
	lastFlushTime    time.Time
	lastClusterStats cluster.ClusterStats
	lastRawCount     int
	lastShingledCnt  int
	onTimeDispatches int
	eventListeners   map[chan EngineEvent]struct{}
}

// EngineEvent represents a real-time SSE notification emitted by the Engine.
type EngineEvent struct {
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"`
	Detail    string `json:"detail,omitempty"`
}

// New creates an initialized Engine instance.
func New(cfg *config.Config, st *store.Store) *Engine {
	ing := feed.NewIngester(cfg)
	sc := score.NewScorer(cfg)
	scr := enrich.NewScraper(cfg)

	var sum ai.Summarizer
	if cfg.LLM.Enabled {
		sum = ai.NewSummarizer(cfg)
	}

	disp := dispatch.BuildDispatchers(cfg)

	e := &Engine{
		cfg:            cfg,
		store:          st,
		ingester:       ing,
		scorer:         sc,
		scraper:        scr,
		summarizer:     sum,
		dispatchers:    disp,
		feedBuffers:    make(map[string][]model.Article),
		schedules:      make(map[string]*feedSchedule),
		lastFlushTime:  time.Now(),
		eventListeners: make(map[chan EngineEvent]struct{}),
	}
	e.hydrateFromStore()
	return e
}

func (e *Engine) hydrateFromStore() {
	if e.store == nil {
		return
	}
	cutoff := time.Now().Add(-e.cfg.Engine.WindowDur)
	if articles, err := e.store.GetRecentArticles(cutoff); err == nil {
		for _, a := range articles {
			e.feedBuffers[a.FeedURL] = append(e.feedBuffers[a.FeedURL], a)
		}
	}
	if sources, err := e.store.ListSources(""); err == nil {
		for _, s := range sources {
			e.schedules[s.URL] = &feedSchedule{
				nextPollAt:     s.NextPollAt,
				consecutive304: s.Consecutive304,
			}
		}
	}
}

// SubscribeEvents registers a channel for real-time SSE updates.
func (e *Engine) SubscribeEvents() (chan EngineEvent, func()) {
	ch := make(chan EngineEvent, 16)
	e.mu.Lock()
	e.eventListeners[ch] = struct{}{}
	e.mu.Unlock()

	cancel := func() {
		e.mu.Lock()
		if _, ok := e.eventListeners[ch]; ok {
			delete(e.eventListeners, ch)
			close(ch)
		}
		e.mu.Unlock()
	}
	return ch, cancel
}

func (e *Engine) emitEvent(evType, detail string) {
	ev := EngineEvent{
		Type:      evType,
		Timestamp: time.Now().UTC().Unix(),
		Detail:    detail,
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for ch := range e.eventListeners {
		select {
		case ch <- ev:
		default:
		}
	}
}

// ReloadConfig updates runtime config, scorer, scraper, summarizer, and dispatchers without restarting.
func (e *Engine) ReloadConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	sc := score.NewScorer(cfg)
	scr := enrich.NewScraper(cfg)
	var sum ai.Summarizer
	if cfg.LLM.Enabled {
		sum = ai.NewSummarizer(cfg)
	}
	disp := dispatch.BuildDispatchers(cfg)

	e.ingester.UpdateConfig(cfg)

	e.mu.Lock()
	e.cfg = cfg
	e.scorer = sc
	e.scraper = scr
	e.summarizer = sum
	e.dispatchers = disp
	for _, f := range cfg.Feeds {
		if _, ok := e.schedules[f.URL]; !ok {
			e.schedules[f.URL] = &feedSchedule{}
		}
	}
	e.mu.Unlock()
}

// ComputeClusterHash generates a deterministic SHA-256 fingerprint of a cluster.
func ComputeClusterHash(c model.Cluster) string {
	urls := make([]string, len(c.Articles))
	for i, a := range c.Articles {
		urls[i] = a.URL
	}
	sort.Strings(urls)

	h := sha256.New()
	for _, u := range urls {
		h.Write([]byte(u))
		h.Write([]byte(";"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func jitterInterval(base time.Duration) time.Duration {
	if base <= 0 {
		return 5 * time.Minute
	}
	tenth := int64(base / 10)
	if tenth <= 0 {
		return base
	}
	// +/- 10% randomized jitter
	delta := rand.Int63n(2*tenth+1) - tenth
	out := base + time.Duration(delta)
	if out < time.Second {
		return time.Second
	}
	return out
}

func computeAdaptiveDelay(base time.Duration, consecutive304 int, hint time.Duration, hasNewArticles bool) (time.Duration, int) {
	if base <= 0 {
		base = 5 * time.Minute
	}
	if hasNewArticles {
		delay := jitterInterval(base)
		if hint > delay {
			delay = hint
		}
		return delay, 0
	}

	nextCount := consecutive304 + 1
	multiplier := math.Pow(1.5, float64(nextCount))
	backoff := time.Duration(float64(base) * multiplier)
	if backoff > maxAdaptiveBackoff {
		backoff = maxAdaptiveBackoff
	}
	if hint > backoff {
		backoff = hint
	}
	return jitterInterval(backoff), nextCount
}

// PollFeeds queries all enabled configured feeds concurrently and buffers/persists resulting articles.
func (e *Engine) PollFeeds(ctx context.Context) error {
	e.mu.Lock()
	var feeds []config.FeedConfig
	for _, f := range e.cfg.Feeds {
		if f.IsEnabled() {
			feeds = append(feeds, f)
		}
	}
	e.mu.Unlock()
	return e.pollFeedList(ctx, feeds)
}

func (e *Engine) pollFeedList(ctx context.Context, feeds []config.FeedConfig) error {
	if len(feeds) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	type pollResult struct {
		feedCfg       config.FeedConfig
		articles      []model.Article
		etag          string
		lastModified  string
		notModified   bool
		statusCode    int
		latencyMs     int64
		nextPollAfter time.Duration
		err           error
	}

	results := make(chan pollResult, len(feeds))

	for _, f := range feeds {
		wg.Add(1)
		go func(fc config.FeedConfig) {
			defer wg.Done()
			var etag, lastMod string
			if e.store != nil {
				etag, lastMod, _, _ = e.store.GetFeedState(fc.URL)
			}

			res, err := e.ingester.FetchFeed(ctx, fc, etag, lastMod)
			if err != nil {
				var fe *feed.FetchError
				status := 0
				var lat int64
				var retryAfter time.Duration
				if errors.As(err, &fe) {
					status = fe.StatusCode
					lat = fe.LatencyMs
					retryAfter = fe.RetryAfter
				}
				results <- pollResult{
					feedCfg:       fc,
					statusCode:    status,
					latencyMs:     lat,
					nextPollAfter: retryAfter,
					err:           err,
				}
				return
			}

			results <- pollResult{
				feedCfg:       fc,
				articles:      res.Articles,
				etag:          res.ETag,
				lastModified:  res.LastModified,
				notModified:   res.NotModified,
				statusCode:    res.StatusCode,
				latencyMs:     res.LatencyMs,
				nextPollAfter: res.NextPollAfter,
			}
		}(f)
	}

	wg.Wait()
	close(results)

	now := time.Now()
	e.mu.Lock()
	basePoll := e.cfg.Engine.PollDuration
	if basePoll <= 0 {
		basePoll = 5 * time.Minute
	}
	maxPerFeed := e.cfg.Engine.MaxArticlesPerFeed
	if maxPerFeed <= 0 {
		maxPerFeed = 20
	}

	for r := range results {
		sched, ok := e.schedules[r.feedCfg.URL]
		if !ok {
			sched = &feedSchedule{}
			e.schedules[r.feedCfg.URL] = sched
		}

		if r.err != nil {
			log.Printf("[warn] polling %s: %v", r.feedCfg.URL, r.err)
			delay, nextCount := computeAdaptiveDelay(basePoll, sched.consecutive304, r.nextPollAfter, false)
			sched.consecutive304 = nextCount
			sched.nextPollAt = now.Add(delay)
			if e.store != nil {
				_ = e.store.UpdateSourcePollTelemetry(r.feedCfg.URL, "", "", now, r.statusCode, r.latencyMs, sched.nextPollAt, sched.consecutive304, r.err.Error())
			}
			continue
		}

		if r.notModified {
			delay, nextCount := computeAdaptiveDelay(basePoll, sched.consecutive304, r.nextPollAfter, false)
			sched.consecutive304 = nextCount
			sched.nextPollAt = now.Add(delay)
			if e.store != nil {
				_ = e.store.UpdateSourcePollTelemetry(r.feedCfg.URL, r.etag, r.lastModified, now, 304, r.latencyMs, sched.nextPollAt, sched.consecutive304, "")
			}
			continue
		}

		newCount := 0
		if len(r.articles) > 0 {
			if e.store != nil {
				if inserted, err := e.store.UpsertArticles(r.articles); err == nil {
					newCount = inserted
				}
			}

			current := e.feedBuffers[r.feedCfg.URL]
			seenURLs := make(map[string]bool, len(current))
			for _, a := range current {
				seenURLs[a.URL] = true
			}
			for _, a := range r.articles {
				if !seenURLs[a.URL] {
					current = append(current, a)
					seenURLs[a.URL] = true
					if e.store == nil {
						newCount++
					}
				}
			}
			if len(current) > maxPerFeed {
				current = current[len(current)-maxPerFeed:]
			}
			e.feedBuffers[r.feedCfg.URL] = current
		}

		delay, nextCount := computeAdaptiveDelay(basePoll, sched.consecutive304, r.nextPollAfter, newCount > 0)
		sched.consecutive304 = nextCount
		sched.nextPollAt = now.Add(delay)

		if e.store != nil {
			_ = e.store.UpdateSourcePollTelemetry(r.feedCfg.URL, r.etag, r.lastModified, now, r.statusCode, r.latencyMs, sched.nextPollAt, sched.consecutive304, "")
		}
	}
	e.mu.Unlock()

	e.emitEvent("poll_complete", fmt.Sprintf("polled %d feeds", len(feeds)))
	return nil
}

// CollectActiveArticles flattens feed buffers and discards articles older than window_duration.
func (e *Engine) CollectActiveArticles(now time.Time) []model.Article {
	e.mu.Lock()
	defer e.mu.Unlock()

	cutoff := now.Add(-e.cfg.Engine.WindowDur)
	var active []model.Article

	for feedURL, buf := range e.feedBuffers {
		var valid []model.Article
		for _, a := range buf {
			if a.PublishedAt.After(cutoff) {
				valid = append(valid, a)
				active = append(active, a)
			}
		}
		e.feedBuffers[feedURL] = valid
	}

	return active
}

func (e *Engine) resolveFeedName(art model.Article) string {
	if strings.TrimSpace(art.FeedName) != "" {
		return art.FeedName
	}
	for _, f := range e.cfg.Feeds {
		if f.URL == art.FeedURL && f.Name != "" {
			return f.Name
		}
	}
	return art.FeedURL
}

func (e *Engine) buildClusterSources(c model.Cluster) []model.SourceInfo {
	sources := make([]model.SourceInfo, len(c.Articles))
	for i, a := range c.Articles {
		sources[i] = model.SourceInfo{
			Name:        e.resolveFeedName(a),
			Tier:        a.FeedTier,
			URL:         a.URL,
			Title:       a.Title,
			PublishedAt: a.PublishedAt.Unix(),
		}
	}
	return sources
}

// BuildPayload converts a winning cluster into the standardized webhook payload.
func (e *Engine) BuildPayload(ctx context.Context, c model.Cluster, hash string) model.Payload {
	payload, _ := e.buildPayloadWithMeta(ctx, c, hash)
	return payload
}

func (e *Engine) buildPayloadWithMeta(ctx context.Context, c model.Cluster, hash string) (model.Payload, string) {
	rep := c.Representative
	sources := e.buildClusterSources(c)
	repFeedName := e.resolveFeedName(rep)

	payload := model.Payload{
		ID:          hash,
		FeedName:    repFeedName,
		Title:       rep.Title,
		Enriched:    false,
		ClusterSize: len(c.Articles),
		Score:       c.Score,
		Sources:     sources,
		Timestamp:   time.Now().Unix(),
	}

	scraperUsed := "none"

	if !e.cfg.LLM.Enabled || e.summarizer == nil {
		return payload, scraperUsed
	}

	maxScrape := len(c.Articles)
	if maxScrape > 3 {
		maxScrape = 3
	}

	content := rep.Description
	scraperCfg := strings.ToLower(strings.TrimSpace(e.cfg.Enrichment.Scraper))
	if scraperCfg != "" && scraperCfg != "none" && e.scraper != nil {
		scrapeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		anyScraped := false
		if maxScrape == 1 {
			if scraped, err := e.scraper.FetchContent(scrapeCtx, rep.URL); err == nil && strings.TrimSpace(scraped) != "" {
				content = scraped
				anyScraped = true
			}
		} else {
			type scrapeResult struct {
				name    string
				title   string
				content string
			}
			results := make([]scrapeResult, maxScrape)
			var wg sync.WaitGroup

			for i := 0; i < maxScrape; i++ {
				wg.Add(1)
				go func(idx int, art model.Article) {
					defer wg.Done()
					artContent := art.Description
					if scraped, err := e.scraper.FetchContent(scrapeCtx, art.URL); err == nil && strings.TrimSpace(scraped) != "" {
						artContent = scraped
						anyScraped = true
					}
					results[idx] = scrapeResult{
						name:    e.resolveFeedName(art),
						title:   art.Title,
						content: artContent,
					}
				}(i, c.Articles[i])
			}
			wg.Wait()

			var sb strings.Builder
			for i, r := range results {
				if i > 0 {
					sb.WriteString("\n\n---\n\n")
				}
				sb.WriteString(fmt.Sprintf("[Source %d: %s - %s]\n%s", i+1, r.name, r.title, r.content))
			}
			content = sb.String()
		}
		if anyScraped {
			scraperUsed = scraperCfg
		} else {
			scraperUsed = "fallback"
		}
	} else if maxScrape > 1 {
		var sb strings.Builder
		for i := 0; i < maxScrape; i++ {
			art := c.Articles[i]
			if i > 0 {
				sb.WriteString("\n\n---\n\n")
			}
			sb.WriteString(fmt.Sprintf("[Source %d: %s - %s]\n%s", i+1, e.resolveFeedName(art), art.Title, art.Description))
		}
		content = sb.String()
	}

	llmCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	summaryRes, err := e.summarizer.Summarize(llmCtx, rep.Title, content, e.cfg.LLM.Language)
	if err != nil {
		log.Printf("[warn] LLM summarization failed for cluster %s: %v (falling back to raw alert)", hash, err)
		return payload, "fallback"
	}

	payload.Content = summaryRes
	payload.Enriched = true

	return payload, scraperUsed
}

// DispatchPayload delivers payload to all enabled dispatchers and queues retries on failure.
func (e *Engine) DispatchPayload(ctx context.Context, p model.Payload) string {
	var targets []string
	for _, d := range e.dispatchers {
		targets = append(targets, d.Name())
		if err := d.Dispatch(ctx, p); err != nil {
			log.Printf("[warn] dispatcher %s failed for payload %s: %v (queueing retry)", d.Name(), p.ID, err)
			payloadBytes, _ := json.Marshal(p)
			nextRetry := time.Now().Add(1 * time.Minute)
			targetURL := ""
			if d.Name() == "webhook" {
				targetURL = e.cfg.Dispatch.Webhook.URL
			} else if d.Name() == "telegram" {
				targetURL = "chat_id:" + e.cfg.Dispatch.Telegram.ChatID
			}
			if e.store != nil {
				_ = e.store.QueueWebhookRetry(d.Name(), targetURL, string(payloadBytes), nextRetry, err.Error())
			}
		} else {
			e.mu.Lock()
			e.onTimeDispatches++
			e.mu.Unlock()
			log.Printf("[info] dispatched alert %s via %s: %s", p.ID[:8], d.Name(), p.Title)
		}
	}
	e.emitEvent("dispatch_updated", p.ID)
	return strings.Join(targets, " + ")
}

// ProcessRetries executes pending webhook retries from SQLite.
func (e *Engine) ProcessRetries(ctx context.Context) {
	if e.store == nil {
		return
	}
	now := time.Now()
	retries, err := e.store.GetPendingWebhookRetries(10, now)
	if err != nil {
		log.Printf("[warn] querying retries: %v", err)
		return
	}

	for _, item := range retries {
		_ = e.executeRetryItem(ctx, item, now)
	}
}

// RetryNow immediately executes a specific queued webhook retry by ID.
func (e *Engine) RetryNow(ctx context.Context, retryID int64) error {
	if e.store == nil {
		return fmt.Errorf("store not initialized")
	}
	item, err := e.store.GetWebhookRetryByID(retryID)
	if err != nil {
		return err
	}
	return e.executeRetryItem(ctx, *item, time.Now())
}

func (e *Engine) executeRetryItem(ctx context.Context, item store.WebhookRetry, now time.Time) error {
	var p model.Payload
	if err := json.Unmarshal([]byte(item.Payload), &p); err != nil {
		_ = e.store.DeleteWebhookRetry(item.ID)
		return err
	}

	var lastErr error
	dispatched := false
	e.mu.Lock()
	dispatchers := append([]dispatch.Dispatcher(nil), e.dispatchers...)
	e.mu.Unlock()

	for _, d := range dispatchers {
		if d.Name() == item.TargetType {
			if err := d.Dispatch(ctx, p); err == nil {
				_ = e.store.DeleteWebhookRetry(item.ID)
				dispatched = true
				e.emitEvent("dispatch_updated", fmt.Sprintf("retry %d succeeded", item.ID))
				log.Printf("[info] retry %d succeeded for %s", item.ID, item.TargetType)
			} else {
				lastErr = err
			}
			break
		}
	}

	if !dispatched {
		attempts := item.Attempts + 1
		backoff := time.Duration(1<<attempts) * time.Minute
		errMsg := ""
		if lastErr != nil {
			errMsg = lastErr.Error()
		} else {
			errMsg = fmt.Sprintf("dispatcher %q is disabled or unavailable", item.TargetType)
		}
		_ = e.store.UpdateWebhookRetry(item.ID, attempts, now.Add(backoff), errMsg)
		e.emitEvent("dispatch_updated", fmt.Sprintf("retry %d failed", item.ID))
		return fmt.Errorf("%s", errMsg)
	}
	return nil
}

// EvaluateAndDispatch clusters buffered articles, persists clusters, and executes hybrid dispatch.
func (e *Engine) EvaluateAndDispatch(ctx context.Context, now time.Time, forceFlush bool) ([]model.Payload, error) {
	active := e.CollectActiveArticles(now)
	if len(active) == 0 {
		return nil, nil
	}

	rawCount := len(active)
	clusterable := cluster.FilterClusterableArticles(active, minClusterTokens)
	if len(clusterable) == 0 {
		clusterable = active
	}

	e.mu.Lock()
	simThreshold := e.cfg.Engine.SimilarityThreshold
	breakingThreshold := e.cfg.Engine.BreakingThreshold
	topK := e.cfg.Engine.TopK
	flushDur := e.cfg.Engine.FlushDur
	scorer := e.scorer
	lastFlush := e.lastFlushTime
	e.mu.Unlock()

	groups, stats := cluster.ClusterArticlesWithStats(clusterable, simThreshold)
	allClusters := scorer.ScoreAllClusters(groups, now)

	e.mu.Lock()
	e.lastClusterStats = stats
	e.lastRawCount = rawCount
	e.lastShingledCnt = len(clusterable)
	e.mu.Unlock()

	isFlushTime := forceFlush || now.Sub(lastFlush) >= flushDur
	var dispatchedPayloads []model.Payload

	records := make([]store.ClusterRecord, 0, len(allClusters))
	type dispatchCandidate struct {
		cluster model.Cluster
		hash    string
		mode    string
	}
	var toDispatch []dispatchCandidate
	bufferedCount := 0

	for _, c := range allClusters {
		hash := ComputeClusterHash(c)
		alreadyDispatched := false
		if e.store != nil {
			if ok, err := e.store.IsClusterDispatched(hash); err == nil && ok {
				alreadyDispatched = true
			}
		}

		rep := c.Representative
		tier := rep.FeedTier
		if tier <= 0 {
			tier = 1
		}
		status := "active"
		if alreadyDispatched {
			status = "dispatched"
		} else if c.Score >= breakingThreshold {
			toDispatch = append(toDispatch, dispatchCandidate{cluster: c, hash: hash, mode: "breaking"})
		} else if bufferedCount < topK {
			bufferedCount++
			if isFlushTime {
				mode := "heartbeat"
				if forceFlush {
					mode = "manual"
				}
				toDispatch = append(toDispatch, dispatchCandidate{cluster: c, hash: hash, mode: mode})
			} else {
				status = "buffered"
			}
		}

		records = append(records, store.ClusterRecord{
			ClusterHash:      hash,
			RepresentativeID: rep.ID,
			Title:            rep.Title,
			Summary:          rep.Description,
			Enriched:         false,
			Score:            c.Score,
			Tier:             tier,
			SourceCount:      score.UniqueSourceCount(c.Articles),
			Category:         rep.Category,
			LeadSource:       e.resolveFeedName(rep),
			Sources:          e.buildClusterSources(c),
			Status:           status,
			CreatedAt:        now,
			UpdatedAt:        now,
		})
	}

	if e.store != nil {
		_ = e.store.UpsertActiveClusters(records)
	}

	for _, cand := range toDispatch {
		payload, scraperUsed := e.buildPayloadWithMeta(ctx, cand.cluster, cand.hash)
		targets := e.DispatchPayload(ctx, payload)
		if e.store != nil {
			_ = e.store.MarkClusterDispatchedFull(cand.hash, payload.Content, payload.Enriched, cand.mode, targets, scraperUsed, now)
		}
		dispatchedPayloads = append(dispatchedPayloads, payload)
	}

	if isFlushTime {
		e.mu.Lock()
		e.lastFlushTime = now
		e.mu.Unlock()
	}

	e.emitEvent("cluster_updated", fmt.Sprintf("%d active clusters", len(allClusters)))
	return dispatchedPayloads, nil
}

// FlushNow immediately flushes Top-K buffered clusters.
func (e *Engine) FlushNow(ctx context.Context) ([]model.Payload, error) {
	return e.EvaluateAndDispatch(ctx, time.Now(), true)
}

// Check evaluates feeds without mutating database, calling LLMs, or sending webhooks.
func (e *Engine) Check(ctx context.Context) ([]model.Payload, error) {
	var allArticles []model.Article

	for _, f := range e.cfg.Feeds {
		if !f.IsEnabled() {
			continue
		}
		res, err := e.ingester.FetchFeed(ctx, f, "", "")
		if err != nil {
			log.Printf("[warn] check feed %s: %v", f.URL, err)
			continue
		}
		allArticles = append(allArticles, res.Articles...)
	}

	if len(allArticles) == 0 {
		return nil, nil
	}

	clusterable := cluster.FilterClusterableArticles(allArticles, minClusterTokens)
	if len(clusterable) == 0 {
		clusterable = allArticles
	}

	groups := cluster.ClusterArticles(clusterable, e.cfg.Engine.SimilarityThreshold)
	now := time.Now()
	clusters := e.scorer.BuildClusters(groups, now)

	payloads := make([]model.Payload, 0, len(clusters))
	for _, c := range clusters {
		hash := ComputeClusterHash(c)
		rep := c.Representative
		payloads = append(payloads, model.Payload{
			ID:          hash,
			FeedName:    e.resolveFeedName(rep),
			Title:       rep.Title,
			Enriched:    false,
			ClusterSize: len(c.Articles),
			Score:       c.Score,
			Sources:     e.buildClusterSources(c),
			Timestamp:   now.Unix(),
		})
	}

	return payloads, nil
}

// Run executes the non-blocking per-feed adaptive polling and dispatch scheduler.
func (e *Engine) Run(ctx context.Context) error {
	log.Printf("[info] celerum engine starting (poll: %s, flush: %s, top_k: %d)",
		e.cfg.Engine.PollInterval, e.cfg.Engine.FlushInterval, e.cfg.Engine.TopK)

	// Poll all due feeds immediately at startup
	e.runDueFeeds(ctx, time.Now())

	schedulerTicker := time.NewTicker(2 * time.Second)
	defer schedulerTicker.Stop()

	retryTicker := time.NewTicker(30 * time.Second)
	defer retryTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[info] engine received stop signal, shutting down")
			return ctx.Err()
		case <-retryTicker.C:
			e.ProcessRetries(ctx)
		case now := <-schedulerTicker.C:
			e.runDueFeeds(ctx, now)
		}
	}
}

func (e *Engine) runDueFeeds(ctx context.Context, now time.Time) {
	e.mu.Lock()
	var due []config.FeedConfig
	for _, f := range e.cfg.Feeds {
		if !f.IsEnabled() {
			continue
		}
		sched := e.schedules[f.URL]
		if sched == nil || sched.nextPollAt.IsZero() || !sched.nextPollAt.After(now) {
			due = append(due, f)
		}
	}
	flushDue := now.Sub(e.lastFlushTime) >= e.cfg.Engine.FlushDur
	e.mu.Unlock()

	if len(due) > 0 {
		if err := e.pollFeedList(ctx, due); err != nil {
			log.Printf("[warn] poll cycle error: %v", err)
		}
	}

	if len(due) > 0 || flushDue {
		if _, err := e.EvaluateAndDispatch(ctx, now, false); err != nil {
			log.Printf("[warn] evaluate cycle error: %v", err)
		}
		if e.store != nil {
			_ = e.store.PruneOldRecords(7)
		}
	}
}
