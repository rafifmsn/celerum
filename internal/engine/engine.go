package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
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

// Engine coordinates the end-to-end RSS intelligence pipeline.
type Engine struct {
	cfg         *config.Config
	store       *store.Store
	ingester    *feed.Ingester
	scorer      *score.Scorer
	scraper     enrich.Scraper
	summarizer  ai.Summarizer
	dispatchers []dispatch.Dispatcher

	mu            sync.Mutex
	feedBuffers   map[string][]model.Article
	lastFlushTime time.Time
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

	return &Engine{
		cfg:           cfg,
		store:         st,
		ingester:      ing,
		scorer:        sc,
		scraper:       scr,
		summarizer:    sum,
		dispatchers:   disp,
		feedBuffers:   make(map[string][]model.Article),
		lastFlushTime: time.Now(),
	}
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

// PollFeeds queries all configured feeds concurrently and buffers resulting articles.
func (e *Engine) PollFeeds(ctx context.Context) error {
	var wg sync.WaitGroup
	type pollResult struct {
		feedURL      string
		articles     []model.Article
		etag         string
		lastModified string
		notModified  bool
		err          error
	}

	results := make(chan pollResult, len(e.cfg.Feeds))

	for _, f := range e.cfg.Feeds {
		wg.Add(1)
		go func(fc config.FeedConfig) {
			defer wg.Done()
			etag, lastMod, _, _ := e.store.GetFeedState(fc.URL)

			res, err := e.ingester.FetchFeed(ctx, fc, etag, lastMod)
			if err != nil {
				results <- pollResult{feedURL: fc.URL, err: err}
				return
			}

			results <- pollResult{
				feedURL:      fc.URL,
				articles:     res.Articles,
				etag:         res.ETag,
				lastModified: res.LastModified,
				notModified:  res.NotModified,
			}
		}(f)
	}

	wg.Wait()
	close(results)

	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()

	for r := range results {
		if r.err != nil {
			log.Printf("[warn] polling %s: %v", r.feedURL, r.err)
			continue
		}

		if r.notModified {
			continue
		}

		_ = e.store.UpdateFeedState(r.feedURL, r.etag, r.lastModified, now)

		if len(r.articles) > 0 {
			current := e.feedBuffers[r.feedURL]
			seenURLs := make(map[string]bool)
			for _, a := range current {
				seenURLs[a.URL] = true
			}

			for _, a := range r.articles {
				if !seenURLs[a.URL] {
					current = append(current, a)
					seenURLs[a.URL] = true
				}
			}

			// Tail-drop oldest entries beyond max_articles_per_feed
			if len(current) > e.cfg.Engine.MaxArticlesPerFeed {
				current = current[len(current)-e.cfg.Engine.MaxArticlesPerFeed:]
			}
			e.feedBuffers[r.feedURL] = current
		}
	}

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

// BuildPayload converts a winning cluster into the standardized webhook payload.
func (e *Engine) BuildPayload(ctx context.Context, c model.Cluster, hash string) model.Payload {
	rep := c.Representative

	sources := make([]model.SourceInfo, len(c.Articles))
	for i, a := range c.Articles {
		name := a.FeedURL
		for _, f := range e.cfg.Feeds {
			if f.URL == a.FeedURL && f.Name != "" {
				name = f.Name
				break
			}
		}
		sources[i] = model.SourceInfo{
			Name:        name,
			Tier:        a.FeedTier,
			URL:         a.URL,
			Title:       a.Title,
			PublishedAt: a.PublishedAt.Unix(),
		}
	}

	repFeedName := rep.FeedURL
	for _, f := range e.cfg.Feeds {
		if f.URL == rep.FeedURL && f.Name != "" {
			repFeedName = f.Name
			break
		}
	}

	// Base fallback payload
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

	if !e.cfg.LLM.Enabled || e.summarizer == nil {
		return payload
	}

	// Enrich with scraper if configured (scrape up to 3 articles in cluster)
	maxScrape := len(c.Articles)
	if maxScrape > 3 {
		maxScrape = 3
	}

	content := rep.Description
	if e.cfg.Enrichment.Scraper != "none" && e.scraper != nil {
		scrapeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		if maxScrape == 1 {
			if scraped, err := e.scraper.FetchContent(scrapeCtx, rep.URL); err == nil && strings.TrimSpace(scraped) != "" {
				content = scraped
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
					}
					name := art.FeedURL
					for _, f := range e.cfg.Feeds {
						if f.URL == art.FeedURL && f.Name != "" {
							name = f.Name
							break
						}
					}
					results[idx] = scrapeResult{
						name:    name,
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
	} else if maxScrape > 1 {
		var sb strings.Builder
		for i := 0; i < maxScrape; i++ {
			art := c.Articles[i]
			name := art.FeedURL
			for _, f := range e.cfg.Feeds {
				if f.URL == art.FeedURL && f.Name != "" {
					name = f.Name
					break
				}
			}
			if i > 0 {
				sb.WriteString("\n\n---\n\n")
			}
			sb.WriteString(fmt.Sprintf("[Source %d: %s - %s]\n%s", i+1, name, art.Title, art.Description))
		}
		content = sb.String()
	}

	// LLM summarization
	llmCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	summaryRes, err := e.summarizer.Summarize(llmCtx, rep.Title, content, e.cfg.LLM.Language)
	if err != nil {
		log.Printf("[warn] LLM summarization failed for cluster %s: %v (falling back to raw alert)", hash, err)
		return payload
	}

	payload.Content = summaryRes
	payload.Enriched = true

	return payload
}

// DispatchPayload delivers payload to all enabled dispatchers and queues retries on failure.
func (e *Engine) DispatchPayload(ctx context.Context, p model.Payload) {
	for _, d := range e.dispatchers {
		if err := d.Dispatch(ctx, p); err != nil {
			log.Printf("[warn] dispatcher %s failed for payload %s: %v (queueing retry)", d.Name(), p.ID, err)
			payloadBytes, _ := json.Marshal(p)
			nextRetry := time.Now().Add(1 * time.Minute)
			_ = e.store.QueueWebhookRetry(d.Name(), "", string(payloadBytes), nextRetry)
		} else {
			log.Printf("[info] dispatched alert %s via %s: %s", p.ID[:8], d.Name(), p.Title)
		}
	}
}

// ProcessRetries executes pending webhook retries from SQLite.
func (e *Engine) ProcessRetries(ctx context.Context) {
	now := time.Now()
	retries, err := e.store.GetPendingWebhookRetries(10, now)
	if err != nil {
		log.Printf("[warn] querying retries: %v", err)
		return
	}

	for _, item := range retries {
		var p model.Payload
		if err := json.Unmarshal([]byte(item.Payload), &p); err != nil {
			_ = e.store.DeleteWebhookRetry(item.ID)
			continue
		}

		dispatched := false
		for _, d := range e.dispatchers {
			if d.Name() == item.TargetType {
				if err := d.Dispatch(ctx, p); err == nil {
					_ = e.store.DeleteWebhookRetry(item.ID)
					dispatched = true
					log.Printf("[info] retry %d succeeded for %s", item.ID, item.TargetType)
				}
				break
			}
		}

		if !dispatched {
			attempts := item.Attempts + 1
			backoff := time.Duration(1<<attempts) * time.Minute
			_ = e.store.UpdateWebhookRetry(item.ID, attempts, now.Add(backoff))
		}
	}
}

// EvaluateAndDispatch clusters buffered articles and executes hybrid dispatch.
func (e *Engine) EvaluateAndDispatch(ctx context.Context, now time.Time, forceFlush bool) ([]model.Payload, error) {
	active := e.CollectActiveArticles(now)
	if len(active) == 0 {
		return nil, nil
	}

	groups := cluster.ClusterArticles(active, e.cfg.Engine.SimilarityThreshold)
	clusters := e.scorer.BuildClusters(groups, now)

	isFlushTime := forceFlush || now.Sub(e.lastFlushTime) >= e.cfg.Engine.FlushDur
	var dispatchedPayloads []model.Payload

	for _, c := range clusters {
		hash := ComputeClusterHash(c)
		alreadyDispatched, err := e.store.IsClusterDispatched(hash)
		if err != nil || alreadyDispatched {
			continue
		}

		isBreaking := c.Score >= e.cfg.Engine.BreakingThreshold
		if isBreaking || isFlushTime {
			payload := e.BuildPayload(ctx, c, hash)
			e.DispatchPayload(ctx, payload)
			_ = e.store.MarkClusterDispatched(hash, now)
			dispatchedPayloads = append(dispatchedPayloads, payload)
		}
	}

	if isFlushTime {
		e.lastFlushTime = now
	}

	return dispatchedPayloads, nil
}

// Check evaluates feeds without mutating database, calling LLMs, or sending webhooks.
func (e *Engine) Check(ctx context.Context) ([]model.Payload, error) {
	var allArticles []model.Article

	for _, f := range e.cfg.Feeds {
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

	groups := cluster.ClusterArticles(allArticles, e.cfg.Engine.SimilarityThreshold)
	now := time.Now()
	clusters := e.scorer.BuildClusters(groups, now)

	payloads := make([]model.Payload, 0, len(clusters))
	for _, c := range clusters {
		hash := ComputeClusterHash(c)
		rep := c.Representative

		repFeedName := rep.FeedURL
		for _, f := range e.cfg.Feeds {
			if f.URL == rep.FeedURL && f.Name != "" {
				repFeedName = f.Name
				break
			}
		}

		sources := make([]model.SourceInfo, len(c.Articles))
		for i, a := range c.Articles {
			name := a.FeedURL
			for _, f := range e.cfg.Feeds {
				if f.URL == a.FeedURL && f.Name != "" {
					name = f.Name
					break
				}
			}
			sources[i] = model.SourceInfo{
				Name:        name,
				Tier:        a.FeedTier,
				URL:         a.URL,
				Title:       a.Title,
				PublishedAt: a.PublishedAt.Unix(),
			}
		}

		payloads = append(payloads, model.Payload{
			ID:          hash,
			FeedName:    repFeedName,
			Title:       rep.Title,
			Enriched:    false,
			ClusterSize: len(c.Articles),
			Score:       c.Score,
			Sources:     sources,
			Timestamp:   now.Unix(),
		})
	}

	return payloads, nil
}

// Run executes the continuous background polling and dispatch loop.
func (e *Engine) Run(ctx context.Context, once bool) error {
	log.Printf("[info] celerum engine starting (poll: %s, flush: %s, top_k: %d)",
		e.cfg.Engine.PollInterval, e.cfg.Engine.FlushInterval, e.cfg.Engine.TopK)

	cycle := func(forceFlush bool) {
		now := time.Now()
		if err := e.PollFeeds(ctx); err != nil {
			log.Printf("[warn] poll cycle error: %v", err)
		}

		_, err := e.EvaluateAndDispatch(ctx, now, forceFlush)
		if err != nil {
			log.Printf("[warn] evaluate cycle error: %v", err)
		}

		e.ProcessRetries(ctx)
		_ = e.store.PruneOldRecords(7)
	}

	// First cycle immediately
	cycle(once)
	if once {
		log.Printf("[info] completed single cycle (--once), exiting")
		return nil
	}

	pollTicker := time.NewTicker(e.cfg.Engine.PollDuration)
	defer pollTicker.Stop()

	retryTicker := time.NewTicker(1 * time.Minute)
	defer retryTicker.Stop()

	for {
		// Add up to 10% randomized interval jitter to prevent publisher spikes
		jitter := time.Duration(rand.Int63n(int64(e.cfg.Engine.PollDuration / 10)))

		select {
		case <-ctx.Done():
			log.Printf("[info] engine received stop signal, shutting down")
			return ctx.Err()
		case <-retryTicker.C:
			e.ProcessRetries(ctx)
		case <-pollTicker.C:
			time.Sleep(jitter)
			cycle(false)
		}
	}
}
