package cluster

import (
	"runtime"
	"testing"
	"time"

	"celerum/pkg/model"
)

func TestCleanTextAndTokenize(t *testing.T) {
	raw := "<p>Bitcoin hits a new <b>All-Time High</b> of $100k! See https://example.com for more.</p>"
	tokens := Tokenize(raw)

	expectedContains := []string{"bitcoin", "hit", "new", "time", "high", "100k"}
	tokenMap := make(map[string]bool)
	for _, tok := range tokens {
		tokenMap[tok] = true
	}

	for _, exp := range expectedContains {
		if !tokenMap[exp] {
			t.Errorf("expected token %q to be present in %v", exp, tokens)
		}
	}

	if tokenMap["all"] || tokenMap["for"] {
		t.Errorf("expected stop-words 'all' and 'for' to be removed")
	}
}

func TestJaccardSimilarity(t *testing.T) {
	a := []uint64{10, 20, 30, 40}
	b := []uint64{20, 30, 40, 50}

	sim := Jaccard(a, b)
	// Intersection: {20, 30, 40} (3), Union: {10, 20, 30, 40, 50} (5) -> 3/5 = 0.6
	if sim < 0.59 || sim > 0.61 {
		t.Errorf("expected Jaccard 0.60, got %f", sim)
	}

	c := []uint64{100, 200}
	simZero := Jaccard(a, c)
	if simZero != 0.0 {
		t.Errorf("expected Jaccard 0.0, got %f", simZero)
	}
}

func TestDisjointSet(t *testing.T) {
	ds := NewDisjointSet(5)
	ds.Union(0, 1)
	ds.Union(1, 2)
	ds.Union(3, 4)

	if ds.Find(0) != ds.Find(2) {
		t.Errorf("elements 0 and 2 should be in the same set")
	}
	if ds.Find(0) == ds.Find(3) {
		t.Errorf("elements 0 and 3 should be in different sets")
	}
}

func TestClusterArticles(t *testing.T) {
	articles := []model.Article{
		{
			ID:          "1",
			Title:       "BlackRock Bitcoin ETF records $1 billion daily inflows as crypto markets surge",
			Description: "Institutional investors pour money into BlackRock spot Bitcoin ETF product.",
			PublishedAt: time.Now(),
		},
		{
			ID:          "2",
			Title:       "Spot Bitcoin ETF from BlackRock sees record $1B inflows amid crypto market rally",
			Description: "BlackRock ETF registers over one billion dollars in inflows as market surges.",
			PublishedAt: time.Now(),
		},
		{
			ID:          "3",
			Title:       "Federal Reserve leaves interest rates unchanged at 5.25 percent benchmark",
			Description: "Fed Chair Jerome Powell announced that monetary policy remains restrictive.",
			PublishedAt: time.Now(),
		},
	}

	clusters := ClusterArticles(articles, 0.20)

	if len(clusters) != 2 {
		t.Fatalf("expected 2 distinct clusters, got %d", len(clusters))
	}

	// Verify that articles 1 and 2 are in the same cluster
	foundMerged := false
	for _, c := range clusters {
		if len(c) == 2 {
			foundMerged = true
			ids := map[string]bool{c[0].ID: true, c[1].ID: true}
			if !ids["1"] || !ids["2"] {
				t.Errorf("expected cluster with articles 1 and 2, got %v", ids)
			}
		}
	}

	if !foundMerged {
		t.Errorf("expected a merged cluster of size 2 for articles 1 and 2")
	}
}

func TestBoundedLinkagePreventsChaining(t *testing.T) {
	now := time.Now()
	// A shares crypto ETF terms with B, B shares SEC court terms with C,
	// but A and C are unrelated topics and must not chain into one mega-cluster.
	articles := []model.Article{
		{
			ID:          "A",
			Title:       "BlackRock spot Bitcoin ETF attracts massive institutional inflows",
			Description: "Bitcoin ETF liquidity surges across Wall Street desks.",
			PublishedAt: now,
		},
		{
			ID:          "B",
			Title:       "BlackRock spot Bitcoin ETF faces SEC regulatory deadline review",
			Description: "SEC enforcement attorneys review compliance filings and court precedent.",
			PublishedAt: now,
		},
		{
			ID:          "C",
			Title:       "SEC enforcement attorneys file court precedent against biotech insider",
			Description: "Regulatory compliance filing targets insider pharmaceutical trading.",
			PublishedAt: now,
		},
	}

	groups, stats := ClusterArticlesWithStats(articles, 0.25)
	if stats.TotalPossiblePairs != 3 {
		t.Errorf("expected 3 total possible pairs, got %d", stats.TotalPossiblePairs)
	}
	for _, g := range groups {
		if len(g) == 3 {
			t.Fatalf("expected bounded linkage to prevent A-B-C chaining into single cluster of 3")
		}
	}
}

func TestFilterClusterableArticles(t *testing.T) {
	articles := []model.Article{
		{ID: "short", Title: "FLASH: BTC up", Description: ""},
		{ID: "valid", Title: "Federal Reserve cuts interest rates by 50 basis points", Description: ""},
	}
	filtered := FilterClusterableArticles(articles, 4)
	if len(filtered) != 1 || filtered[0].ID != "valid" {
		t.Fatalf("expected only 'valid' article to pass 4-token filter, got %+v", filtered)
	}
}

func BenchmarkCosineDot(b *testing.B) {
	vecA := normalizeVec(sparseVec{
		"blackrock": 2.4, "bitcoin": 1.9, "etf": 2.1, "inflow": 2.6, "billion": 1.8,
		"institution": 1.5, "market": 1.2, "surge": 1.4, "spot": 1.6, "sec": 2.0,
	})
	vecB := normalizeVec(sparseVec{
		"blackrock": 2.4, "bitcoin": 1.9, "etf": 2.1, "record": 1.7, "daily": 1.3,
		"investor": 1.5, "market": 1.2, "rally": 1.4, "spot": 1.6, "fund": 1.1,
	})

	b.ReportAllocs()
	for b.Loop() {
		_ = dotVec(vecA, vecB)
	}
}

func BenchmarkTokenizeAndShingle(b *testing.B) {
	title := "BlackRock Bitcoin ETF records $1 billion daily inflows as crypto markets surge"
	desc := "Institutional investors pour money into BlackRock spot Bitcoin ETF product amid broader market rally."

	b.ReportAllocs()
	for b.Loop() {
		toks := append(Tokenize(title), Tokenize(desc)...)
		_ = ShingleHashes(toks)
	}
}

func BenchmarkClusterArticles_100(b *testing.B) {
	topics := []string{
		"Federal Reserve cuts interest rates amid slowing inflation figures",
		"BlackRock files updated spot Ethereum ETF application with SEC",
		"Apple unveils new M4 Max chip with enhanced neural engine capabilities",
		"OpenAI announces multimodal reasoning model with real-time audio API",
		"Crude oil prices steady following OPEC production quota agreement",
		"SpaceX completes sixth Starship flight test with successful booster catch",
		"Nvidia reports record quarterly data center revenue driven by AI demand",
		"Treasury yields slide to multi-month lows as job market data cools",
		"Microsoft expands cloud infrastructure investment across Southeast Asia",
		"European Central Bank signals cautious easing cycle amid wage growth",
	}

	descriptions := []string{
		"Policymakers cite easing CPI figures and a softening labor market as catalysts for policy adjustment.",
		"The investment firm submitted an amendment following regulator feedback on custody arrangements.",
		"The next-generation silicon features increased unified memory bandwidth and ray tracing improvements.",
		"The new model integrates native voice and visual capabilities with reduced inference latency.",
		"Member nations agreed to extend voluntary output reductions to stabilize global energy markets.",
		"The prototype demonstrated precision thermal shielding resilience during atmospheric reentry.",
		"Enterprise hardware sales surged as hyper-scalers expanded GPU cluster compute capacity.",
		"Benchmark government debt rallied following dovish comments from central bank officials.",
		"The tech conglomerate announced multiple enterprise data center expansions to support regional demand.",
		"Monetary authorities emphasized data-dependent decision making while reviewing headline wage indicators.",
	}

	articles := make([]model.Article, 0, 100)
	for i := 0; i < 100; i++ {
		topicIdx := i % len(topics)
		articles = append(articles, model.Article{
			ID:          string(rune('A' + (i % 26))) + string(rune('0' + (i / 26))),
			Title:       topics[topicIdx],
			Description: descriptions[topicIdx],
			PublishedAt: time.Now(),
		})
	}

	b.ReportAllocs()
	for b.Loop() {
		batch := make([]model.Article, len(articles))
		copy(batch, articles)
		_ = ClusterArticles(batch, 0.28)
	}
}

func BenchmarkClusterArticles_500(b *testing.B) {
	topics := []string{
		"Federal Reserve cuts interest rates amid slowing inflation figures",
		"BlackRock files updated spot Ethereum ETF application with SEC",
		"Apple unveils new M4 Max chip with enhanced neural engine capabilities",
		"OpenAI announces multimodal reasoning model with real-time audio API",
		"Crude oil prices steady following OPEC production quota agreement",
		"SpaceX completes sixth Starship flight test with successful booster catch",
		"Nvidia reports record quarterly data center revenue driven by AI demand",
		"Treasury yields slide to multi-month lows as job market data cools",
		"Microsoft expands cloud infrastructure investment across Southeast Asia",
		"European Central Bank signals cautious easing cycle amid wage growth",
	}
	descriptions := []string{
		"Policymakers cite easing CPI figures and a softening labor market as catalysts for policy adjustment.",
		"The investment firm submitted an amendment following regulator feedback on custody arrangements.",
		"The next-generation silicon features increased unified memory bandwidth and ray tracing improvements.",
		"The new model integrates native voice and visual capabilities with reduced inference latency.",
		"Member nations agreed to extend voluntary output reductions to stabilize global energy markets.",
		"The prototype demonstrated precision thermal shielding resilience during atmospheric reentry.",
		"Enterprise hardware sales surged as hyper-scalers expanded GPU cluster compute capacity.",
		"Benchmark government debt rallied following dovish comments from central bank officials.",
		"The tech conglomerate announced multiple enterprise data center expansions to support regional demand.",
		"Monetary authorities emphasized data-dependent decision making while reviewing headline wage indicators.",
	}

	articles := make([]model.Article, 0, 500)
	for i := 0; i < 500; i++ {
		topicIdx := i % len(topics)
		articles = append(articles, model.Article{
			ID:          string(rune('A' + (i % 26))) + string(rune('0' + (i / 26))),
			Title:       topics[topicIdx],
			Description: descriptions[topicIdx],
			PublishedAt: time.Now(),
		})
	}

	b.ReportAllocs()
	for b.Loop() {
		// Pass a copy so slice mutations on shingles do not accumulate
		batch := make([]model.Article, len(articles))
		copy(batch, articles)
		_ = ClusterArticles(batch, 0.28)
	}
}

func TestPruningEfficiency(t *testing.T) {
	topics := []string{
		"Federal Reserve cuts interest rates amid slowing inflation figures",
		"BlackRock files updated spot Ethereum ETF application with SEC",
		"Apple unveils new M4 Max chip with enhanced neural engine capabilities",
		"OpenAI announces multimodal reasoning model with real-time audio API",
		"Crude oil prices steady following OPEC production quota agreement",
		"SpaceX completes sixth Starship flight test with successful booster catch",
		"Nvidia reports record quarterly data center revenue driven by AI demand",
		"Treasury yields slide to multi-month lows as job market data cools",
		"Microsoft expands cloud infrastructure investment across Southeast Asia",
		"European Central Bank signals cautious easing cycle amid wage growth",
	}

	descriptions := []string{
		"Policymakers cite easing CPI figures and a softening labor market as catalysts for policy adjustment.",
		"The investment firm submitted an amendment following regulator feedback on custody arrangements.",
		"The next-generation silicon features increased unified memory bandwidth and ray tracing improvements.",
		"The new model integrates native voice and visual capabilities with reduced inference latency.",
		"Member nations agreed to extend voluntary output reductions to stabilize global energy markets.",
		"The prototype demonstrated precision thermal shielding resilience during atmospheric reentry.",
		"Enterprise hardware sales surged as hyper-scalers expanded GPU cluster compute capacity.",
		"Benchmark government debt rallied following dovish comments from central bank officials.",
		"The tech conglomerate announced multiple enterprise data center expansions to support regional demand.",
		"Monetary authorities emphasized data-dependent decision making while reviewing headline wage indicators.",
	}

	articles := make([]model.Article, 0, 500)
	for i := 0; i < 500; i++ {
		topicIdx := i % len(topics)
		articles = append(articles, model.Article{
			ID:          string(rune('A' + (i % 26))) + string(rune('0' + (i / 26))),
			Title:       topics[topicIdx],
			Description: descriptions[topicIdx],
			PublishedAt: time.Now(),
		})
	}

	n := len(articles)
	exhaustivePairs := n * (n - 1) / 2

	for i := range articles {
		titleTokens := Tokenize(articles[i].Title)
		articles[i].TitleShingles = ShingleHashes(titleTokens)
		descTokens := Tokenize(articles[i].Description)
		if len(descTokens) > 20 {
			descTokens = descTokens[:20]
		}
		combinedTokens := append(titleTokens, descTokens...)
		articles[i].Shingles = ShingleHashes(combinedTokens)
	}

	invertedIndex := make(map[uint64][]int)
	for idx, art := range articles {
		seen := make(map[uint64]bool)
		for _, s := range art.TitleShingles {
			if !seen[s] {
				seen[s] = true
				invertedIndex[s] = append(invertedIndex[s], idx)
			}
		}
		for _, s := range art.Shingles {
			if !seen[s] {
				seen[s] = true
				invertedIndex[s] = append(invertedIndex[s], idx)
			}
		}
	}

	compared := make(map[uint64]struct{})
	for i := 0; i < n; i++ {
		sharedCounts := make(map[int]int)
		for _, shingle := range articles[i].Shingles {
			for _, matchIdx := range invertedIndex[shingle] {
				if matchIdx > i {
					sharedCounts[matchIdx]++
				}
			}
		}
		for j, count := range sharedCounts {
			if count < 2 {
				continue
			}
			pairKey := (uint64(i) << 32) | uint64(j)
			compared[pairKey] = struct{}{}
		}
	}

	actualPairs := len(compared)
	prunedPercent := float64(exhaustivePairs-actualPairs) / float64(exhaustivePairs) * 100.0
	t.Logf("Exhaustive pairs: %d, Evaluated pairs: %d, Pruned: %.2f%%", exhaustivePairs, actualPairs, prunedPercent)
}

func TestMemoryFootprint(t *testing.T) {
	measureForCount := func(count int) {
		topics := []string{
			"Federal Reserve cuts interest rates amid slowing inflation figures",
			"BlackRock files updated spot Ethereum ETF application with SEC",
			"Apple unveils new M4 Max chip with enhanced neural engine capabilities",
			"OpenAI announces multimodal reasoning model with real-time audio API",
			"Crude oil prices steady following OPEC production quota agreement",
			"SpaceX completes sixth Starship flight test with successful booster catch",
			"Nvidia reports record quarterly data center revenue driven by AI demand",
			"Treasury yields slide to multi-month lows as job market data cools",
			"Microsoft expands cloud infrastructure investment across Southeast Asia",
			"European Central Bank signals cautious easing cycle amid wage growth",
		}
		descriptions := []string{
			"Policymakers cite easing CPI figures and a softening labor market as catalysts for policy adjustment.",
			"The investment firm submitted an amendment following regulator feedback on custody arrangements.",
			"The next-generation silicon features increased unified memory bandwidth and ray tracing improvements.",
			"The new model integrates native voice and visual capabilities with reduced inference latency.",
			"Member nations agreed to extend voluntary output reductions to stabilize global energy markets.",
			"The prototype demonstrated precision thermal shielding resilience during atmospheric reentry.",
			"Enterprise hardware sales surged as hyper-scalers expanded GPU cluster compute capacity.",
			"Benchmark government debt rallied following dovish comments from central bank officials.",
			"The tech conglomerate announced multiple enterprise data center expansions to support regional demand.",
			"Monetary authorities emphasized data-dependent decision making while reviewing headline wage indicators.",
		}

		articles := make([]model.Article, 0, count)
		for i := 0; i < count; i++ {
			topicIdx := i % len(topics)
			articles = append(articles, model.Article{
				ID:          string(rune('A' + (i % 26))) + string(rune('0' + (i / 26))),
				Title:       topics[topicIdx],
				Description: descriptions[topicIdx],
				PublishedAt: time.Now(),
			})
		}

		// Force GC before measurement to establish clean baseline
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)

		clusters := ClusterArticles(articles, 0.28)
		_ = clusters

		var after runtime.MemStats
		runtime.ReadMemStats(&after)

		allocatedBytes := after.TotalAlloc - before.TotalAlloc
		heapInUse := float64(after.HeapInuse) / (1024 * 1024)
		sysMemory := float64(after.Sys) / (1024 * 1024)

		t.Logf("[%d Articles] Heap Allocated during run: %.2f MB | Heap In-Use: %.2f MB | OS Sys Memory: %.2f MB",
			count, float64(allocatedBytes)/(1024*1024), heapInUse, sysMemory)
	}

	measureForCount(100)
	measureForCount(500)
	measureForCount(1000)
}
