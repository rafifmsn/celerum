package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"celerum/internal/ai"
	"celerum/internal/config"
	"celerum/internal/dispatch"
	"celerum/internal/enrich"
	"celerum/internal/feed"
	"celerum/internal/store"
	"celerum/pkg/model"
)

// StageResult holds the structured verification outcome for one diagnostic stage.
type StageResult struct {
	Stage     int    `json:"stage"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Summary   string `json:"summary"`
	Detail    string `json:"detail,omitempty"`
	LatencyMs int64  `json:"latency_ms"`
}

// Report aggregates all 6 non-destructive diagnostic stages.
type Report struct {
	OK        bool          `json:"ok"`
	CheckedAt time.Time     `json:"checked_at"`
	Stages    []StageResult `json:"stages"`
}

// Options configures optional overrides and live-send behavior for Doctor.
type Options struct {
	Send            bool
	TelegramBaseURL string
	FeedClient      *http.Client
	Scraper         enrich.Scraper
	Summarizer      ai.Summarizer
}

// Run executes the 6-stage non-destructive diagnostic suite without mutating dispatched_clusters.
func Run(ctx context.Context, cfg *config.Config, st *store.Store, opts Options) Report {
	report := Report{
		OK:        true,
		CheckedAt: time.Now().UTC(),
		Stages:    make([]StageResult, 0, 6),
	}

	addStage := func(res StageResult) {
		if res.Status != "PASS" {
			report.OK = false
		}
		report.Stages = append(report.Stages, res)
	}

	// Stage 1: Config & Environment
	s1Start := time.Now()
	if cfg == nil {
		addStage(StageResult{
			Stage:     1,
			Name:      "Config & Environment",
			Status:    "FAIL",
			Summary:   "No active configuration loaded.",
			LatencyMs: time.Since(s1Start).Milliseconds(),
		})
		return report
	}
	if err := cfg.ValidateAndSetDefaults(); err != nil {
		addStage(StageResult{
			Stage:     1,
			Name:      "Config & Environment",
			Status:    "FAIL",
			Summary:   "Configuration validation failed.",
			Detail:    err.Error(),
			LatencyMs: time.Since(s1Start).Milliseconds(),
		})
	} else {
		addStage(StageResult{
			Stage:     1,
			Name:      "Config & Environment",
			Status:    "PASS",
			Summary:   fmt.Sprintf("Verified runtime config (%d feeds, scraper=%s, llm=%v).", len(cfg.Feeds), nonEmpty(cfg.Enrichment.Scraper, "none"), cfg.LLM.Enabled),
			LatencyMs: time.Since(s1Start).Milliseconds(),
		})
	}

	// Stage 2: SQLite Storage
	s2Start := time.Now()
	if st == nil {
		addStage(StageResult{
			Stage:     2,
			Name:      "SQLite Embedded Storage",
			Status:    "FAIL",
			Summary:   "SQLite store handle is nil.",
			LatencyMs: time.Since(s2Start).Milliseconds(),
		})
	} else if jm, sizeBytes, err := st.CheckHealth(); err != nil {
		addStage(StageResult{
			Stage:     2,
			Name:      "SQLite Embedded Storage",
			Status:    "FAIL",
			Summary:   "SQLite health probe failed.",
			Detail:    err.Error(),
			LatencyMs: time.Since(s2Start).Milliseconds(),
		})
	} else {
		sizeMB := float64(sizeBytes) / (1024 * 1024)
		addStage(StageResult{
			Stage:     2,
			Name:      "SQLite Embedded Storage",
			Status:    "PASS",
			Summary:   fmt.Sprintf("%s writable in %s mode (%.2f MB).", st.DBPath(), strings.ToUpper(jm), sizeMB),
			LatencyMs: time.Since(s2Start).Milliseconds(),
		})
	}

	// Stage 3: RSS / Atom Feed Reachability
	s3Start := time.Now()
	var ing *feed.Ingester
	if opts.FeedClient != nil {
		ing = feed.NewIngesterWithClient(cfg, opts.FeedClient)
	} else {
		ing = feed.NewIngester(cfg)
	}

	var enabledFeeds []config.FeedConfig
	for _, f := range cfg.Feeds {
		if f.IsEnabled() {
			enabledFeeds = append(enabledFeeds, f)
		}
	}

	var sampleArticle *model.Article
	if len(enabledFeeds) == 0 {
		addStage(StageResult{
			Stage:     3,
			Name:      "RSS / Atom Feed Reachability",
			Status:    "FAIL",
			Summary:   "No enabled feeds configured.",
			LatencyMs: time.Since(s3Start).Milliseconds(),
		})
	} else {
		type feedCheckRes struct {
			url         string
			notModified bool
			articles    []model.Article
			err         error
		}
		resCh := make(chan feedCheckRes, len(enabledFeeds))
		var wg sync.WaitGroup
		for _, fc := range enabledFeeds {
			wg.Add(1)
			go func(f config.FeedConfig) {
				defer wg.Done()
				var etag, lastMod string
				if st != nil {
					etag, lastMod, _, _ = st.GetFeedState(f.URL)
				}
				fCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
				defer cancel()
				res, err := ing.FetchFeed(fCtx, f, etag, lastMod)
				if err != nil {
					resCh <- feedCheckRes{url: f.URL, err: err}
					return
				}
				resCh <- feedCheckRes{url: f.URL, notModified: res.NotModified, articles: res.Articles}
			}(fc)
		}
		wg.Wait()
		close(resCh)

		var okCount, count304, count200 int
		var failures []string
		for r := range resCh {
			if r.err != nil {
				failures = append(failures, fmt.Sprintf("%s (%v)", r.url, r.err))
				continue
			}
			okCount++
			if r.notModified {
				count304++
			} else {
				count200++
			}
			if sampleArticle == nil && len(r.articles) > 0 {
				a := r.articles[0]
				sampleArticle = &a
			}
		}

		// Fallback to recent article in SQLite if every feed returned 304 Not Modified
		if sampleArticle == nil && st != nil {
			if items, _, _, _, qErr := st.QueryArticles("", "", "", 1, 1); qErr == nil && len(items) > 0 {
				a := items[0]
				sampleArticle = &a
			}
		}

		if len(failures) > 0 {
			addStage(StageResult{
				Stage:     3,
				Name:      "RSS / Atom Feed Reachability",
				Status:    "FAIL",
				Summary:   fmt.Sprintf("%d/%d enabled feeds reachable (%d 304, %d 200).", okCount, len(enabledFeeds), count304, count200),
				Detail:    strings.Join(failures, "; "),
				LatencyMs: time.Since(s3Start).Milliseconds(),
			})
		} else {
			addStage(StageResult{
				Stage:     3,
				Name:      "RSS / Atom Feed Reachability",
				Status:    "PASS",
				Summary:   fmt.Sprintf("%d/%d enabled feeds reachable (%d returned 304 Not Modified, %d returned 200 OK).", okCount, len(enabledFeeds), count304, count200),
				LatencyMs: time.Since(s3Start).Milliseconds(),
			})
		}
	}

	// Stage 4: Article Scraper
	s4Start := time.Now()
	scraperName := strings.ToLower(strings.TrimSpace(cfg.Enrichment.Scraper))
	if scraperName == "" {
		scraperName = "none"
	}
	scrapedContent := ""
	if scraperName == "none" {
		addStage(StageResult{
			Stage:     4,
			Name:      "Article Scraper (none)",
			Status:    "PASS",
			Summary:   "Scraper set to 'none'; enrichment scraping disabled.",
			LatencyMs: time.Since(s4Start).Milliseconds(),
		})
	} else {
		scr := opts.Scraper
		if scr == nil {
			scr = enrich.NewScraper(cfg)
		}
		if sampleArticle == nil || strings.TrimSpace(sampleArticle.URL) == "" {
			addStage(StageResult{
				Stage:     4,
				Name:      fmt.Sprintf("Article Scraper (%s)", scraperName),
				Status:    "FAIL",
				Summary:   "No fetched article URL available to verify scraper.",
				LatencyMs: time.Since(s4Start).Milliseconds(),
			})
		} else {
			scCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			content, err := scr.FetchContent(scCtx, sampleArticle.URL)
			cancel()
			if err != nil {
				addStage(StageResult{
					Stage:     4,
					Name:      fmt.Sprintf("Article Scraper (%s)", scraperName),
					Status:    "FAIL",
					Summary:   fmt.Sprintf("Scraper %q failed on %s.", scraperName, sampleArticle.URL),
					Detail:    err.Error(),
					LatencyMs: time.Since(s4Start).Milliseconds(),
				})
			} else if strings.TrimSpace(content) == "" {
				addStage(StageResult{
					Stage:     4,
					Name:      fmt.Sprintf("Article Scraper (%s)", scraperName),
					Status:    "FAIL",
					Summary:   fmt.Sprintf("Scraper %q returned empty content for %s.", scraperName, sampleArticle.URL),
					LatencyMs: time.Since(s4Start).Milliseconds(),
				})
			} else {
				scrapedContent = content
				addStage(StageResult{
					Stage:     4,
					Name:      fmt.Sprintf("Article Scraper (%s)", scraperName),
					Status:    "PASS",
					Summary:   fmt.Sprintf("Scraped %d chars from %s.", len(content), sampleArticle.URL),
					LatencyMs: time.Since(s4Start).Milliseconds(),
				})
			}
		}
	}

	// Stage 5: LLM Synthesis Endpoint
	s5Start := time.Now()
	if !cfg.LLM.Enabled {
		addStage(StageResult{
			Stage:     5,
			Name:      "LLM Synthesis Endpoint",
			Status:    "PASS",
			Summary:   "LLM synthesis is disabled in runtime configuration.",
			LatencyMs: time.Since(s5Start).Milliseconds(),
		})
	} else if strings.ToLower(strings.TrimSpace(cfg.LLM.Provider)) != "ollama" && strings.TrimSpace(cfg.LLM.APIKey) == "" {
		addStage(StageResult{
			Stage:     5,
			Name:      "LLM Synthesis Endpoint",
			Status:    "FAIL",
			Summary:   fmt.Sprintf("Provider %q selected but API key is missing.", cfg.LLM.Provider),
			LatencyMs: time.Since(s5Start).Milliseconds(),
		})
	} else {
		sum := opts.Summarizer
		if sum == nil {
			sum = ai.NewSummarizer(cfg)
		}
		testTitle := "Federal Reserve maintains benchmark rate corridor"
		testBody := "Federal Reserve policymakers maintained the benchmark interest rate range while evaluating incoming inflation and labor figures."
		if sampleArticle != nil {
			testTitle = sampleArticle.Title
			if scrapedContent != "" {
				testBody = scrapedContent
			} else if strings.TrimSpace(sampleArticle.Description) != "" {
				testBody = sampleArticle.Description
			} else {
				testBody = sampleArticle.Title
			}
		}

		llmCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		res, err := sum.Summarize(llmCtx, testTitle, testBody, cfg.LLM.Language)
		cancel()
		elapsed := time.Since(s5Start).Milliseconds()
		if err != nil {
			addStage(StageResult{
				Stage:     5,
				Name:      "LLM Synthesis Endpoint",
				Status:    "FAIL",
				Summary:   fmt.Sprintf("Provider %s (%s) synthesis call failed.", cfg.LLM.Provider, cfg.LLM.Model),
				Detail:    err.Error(),
				LatencyMs: elapsed,
			})
		} else if strings.TrimSpace(res) == "" {
			addStage(StageResult{
				Stage:     5,
				Name:      "LLM Synthesis Endpoint",
				Status:    "FAIL",
				Summary:   fmt.Sprintf("Provider %s (%s) returned empty summary.", cfg.LLM.Provider, cfg.LLM.Model),
				LatencyMs: elapsed,
			})
		} else {
			addStage(StageResult{
				Stage:     5,
				Name:      "LLM Synthesis Endpoint",
				Status:    "PASS",
				Summary:   fmt.Sprintf("%s /chat/completions verified in %dms (%s).", nonEmpty(cfg.LLM.Provider, "openai"), elapsed, cfg.LLM.Model),
				LatencyMs: elapsed,
			})
		}
	}

	// Stage 6: Alert Dispatchers
	s6Start := time.Now()
	if !cfg.Dispatch.Telegram.Enabled && !cfg.Dispatch.Webhook.Enabled {
		addStage(StageResult{
			Stage:     6,
			Name:      "Alert Dispatchers (Telegram & Webhook)",
			Status:    "PASS",
			Summary:   "No alert dispatchers currently enabled.",
			LatencyMs: time.Since(s6Start).Milliseconds(),
		})
	} else {
		var notes []string
		var errs []string
		httpClient := &http.Client{Timeout: 10 * time.Second}

		if cfg.Dispatch.Telegram.Enabled {
			tgBase := strings.TrimRight(opts.TelegramBaseURL, "/")
			if tgBase == "" {
				tgBase = "https://api.telegram.org"
			}
			username, err := verifyTelegram(ctx, httpClient, tgBase, cfg.Dispatch.Telegram.BotToken, cfg.Dispatch.Telegram.ChatID)
			if err != nil {
				errs = append(errs, fmt.Sprintf("telegram: %v", err))
			} else {
				msg := fmt.Sprintf("Telegram getMe/getChat verified (@%s, chat_id %s)", username, cfg.Dispatch.Telegram.ChatID)
				if opts.Send {
					disp := dispatch.NewTelegramDispatcher(cfg.Dispatch.Telegram.BotToken, cfg.Dispatch.Telegram.ChatID)
					if err := disp.Dispatch(ctx, model.Payload{
						ID:       "doctor-probe",
						FeedName: "Celerum Doctor",
						Title:    "Celerum Doctor live verification probe",
						Content:  "Non-destructive diagnostic alert verification succeeded.",
						Enriched: true,
						Sources: []model.SourceInfo{{
							Name:  "Celerum Doctor",
							Title: "Diagnostic Probe",
							URL:   "https://github.com/rafifmsn/celerum",
						}},
						Timestamp: time.Now().Unix(),
					}); err != nil {
						errs = append(errs, fmt.Sprintf("telegram send: %v", err))
					} else {
						msg += " + live message delivered"
					}
				}
				notes = append(notes, msg)
			}
		}

		if cfg.Dispatch.Webhook.Enabled {
			if err := verifyWebhookEndpoint(ctx, httpClient, cfg.Dispatch.Webhook.URL, cfg.Dispatch.Webhook.Headers); err != nil {
				errs = append(errs, fmt.Sprintf("webhook: %v", err))
			} else {
				msg := fmt.Sprintf("Webhook endpoint %s reachable", cfg.Dispatch.Webhook.URL)
				if opts.Send {
					disp := dispatch.NewGenericWebhookDispatcher(cfg.Dispatch.Webhook.URL, cfg.Dispatch.Webhook.Headers)
					if err := disp.Dispatch(ctx, model.Payload{
						ID:        "doctor-probe",
						FeedName:  "Celerum Doctor",
						Title:     "Celerum Doctor live verification probe",
						Timestamp: time.Now().Unix(),
					}); err != nil {
						errs = append(errs, fmt.Sprintf("webhook send: %v", err))
					} else {
						msg += " + live payload delivered"
					}
				}
				notes = append(notes, msg)
			}
		}

		elapsed := time.Since(s6Start).Milliseconds()
		if len(errs) > 0 {
			addStage(StageResult{
				Stage:     6,
				Name:      "Alert Dispatchers (Telegram & Webhook)",
				Status:    "FAIL",
				Summary:   "One or more dispatchers failed verification.",
				Detail:    strings.Join(errs, "; "),
				LatencyMs: elapsed,
			})
		} else {
			addStage(StageResult{
				Stage:     6,
				Name:      "Alert Dispatchers (Telegram & Webhook)",
				Status:    "PASS",
				Summary:   strings.Join(notes, "; "),
				LatencyMs: elapsed,
			})
		}
	}

	return report
}

func verifyTelegram(ctx context.Context, client *http.Client, baseURL, token, chatID string) (string, error) {
	if strings.TrimSpace(token) == "" || strings.TrimSpace(chatID) == "" {
		return "", fmt.Errorf("bot_token or chat_id is missing")
	}

	getMeURL := fmt.Sprintf("%s/bot%s/getMe", baseURL, token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getMeURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("getMe request failed: %w", err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()

	var meResp struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &meResp); err != nil || resp.StatusCode != http.StatusOK || !meResp.OK {
		return "", fmt.Errorf("getMe HTTP %d: %s", resp.StatusCode, nonEmpty(meResp.Description, strings.TrimSpace(string(body))))
	}

	getChatURL := fmt.Sprintf("%s/bot%s/getChat?chat_id=%s", baseURL, token, url.QueryEscape(chatID))
	reqChat, err := http.NewRequestWithContext(ctx, http.MethodGet, getChatURL, nil)
	if err != nil {
		return "", err
	}
	respChat, err := client.Do(reqChat)
	if err != nil {
		return "", fmt.Errorf("getChat request failed: %w", err)
	}
	chatBody, _ := io.ReadAll(io.LimitReader(respChat.Body, 64<<10))
	_ = respChat.Body.Close()

	var chatResp struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(chatBody, &chatResp); err != nil || respChat.StatusCode != http.StatusOK || !chatResp.OK {
		return "", fmt.Errorf("getChat HTTP %d: %s", respChat.StatusCode, nonEmpty(chatResp.Description, strings.TrimSpace(string(chatBody))))
	}

	return meResp.Result.Username, nil
}

func verifyWebhookEndpoint(ctx context.Context, client *http.Client, targetURL string, headers map[string]string) error {
	if strings.TrimSpace(targetURL) == "" {
		return fmt.Errorf("webhook url is missing")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, targetURL, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("endpoint returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}
