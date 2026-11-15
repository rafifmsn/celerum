package cluster

import (
	"hash/fnv"
	"math"
	"regexp"
	"sort"
	"strings"

	"celerum/pkg/model"
)

var (
	htmlTagRegex = regexp.MustCompile(`<[^>]*>`)
	urlRegex     = regexp.MustCompile(`https?://\S+`)
	nonWordRegex = regexp.MustCompile(`[^\p{L}\p{N}\s]+`)
	spaceRegex   = regexp.MustCompile(`\s+`)
)

// Common stop-words to eliminate uninformative noise.
var defaultStopwords = map[string]struct{}{
	"a": {}, "about": {}, "after": {}, "all": {}, "also": {}, "an": {}, "and": {}, "any": {}, "are": {}, "as": {},
	"at": {}, "be": {}, "because": {}, "been": {}, "before": {}, "being": {}, "between": {}, "both": {}, "but": {}, "by": {},
	"can": {}, "could": {}, "did": {}, "do": {}, "does": {}, "doing": {}, "down": {}, "during": {}, "each": {}, "few": {},
	"for": {}, "from": {}, "further": {}, "had": {}, "has": {}, "have": {}, "having": {}, "he": {}, "her": {}, "here": {},
	"hers": {}, "herself": {}, "him": {}, "himself": {}, "his": {}, "how": {}, "i": {}, "if": {}, "in": {}, "into": {},
	"is": {}, "it": {}, "its": {}, "itself": {}, "just": {}, "me": {}, "more": {}, "most": {}, "my": {}, "myself": {},
	"no": {}, "nor": {}, "not": {}, "now": {}, "of": {}, "off": {}, "on": {}, "once": {}, "only": {}, "or": {},
	"other": {}, "our": {}, "ours": {}, "ourselves": {}, "out": {}, "over": {}, "own": {}, "s": {}, "same": {}, "she": {},
	"should": {}, "so": {}, "some": {}, "such": {}, "than": {}, "that": {}, "the": {}, "their": {}, "theirs": {}, "them": {},
	"themselves": {}, "then": {}, "there": {}, "these": {}, "they": {}, "this": {}, "those": {}, "through": {}, "to": {},
	"too": {}, "under": {}, "until": {}, "up": {}, "very": {}, "was": {}, "we": {}, "were": {}, "what": {}, "when": {},
	"where": {}, "which": {}, "while": {}, "who": {}, "whom": {}, "why": {}, "will": {}, "with": {}, "would": {},
}

// ClusterStats records candidate-pruning telemetry from an inverted-index clustering run.
type ClusterStats struct {
	TotalArticles      int
	TotalPossiblePairs int
	EvaluatedPairs     int
	PrunedPairs        int
	PrunedRatio        float64
}

// CleanText normalizes raw text by stripping HTML, URLs, and non-alphanumeric punctuation.
func CleanText(raw string) string {
	noHTML := htmlTagRegex.ReplaceAllString(raw, " ")
	noURL := urlRegex.ReplaceAllString(noHTML, " ")
	cleaned := nonWordRegex.ReplaceAllString(noURL, " ")
	lowered := strings.ToLower(cleaned)
	return strings.TrimSpace(spaceRegex.ReplaceAllString(lowered, " "))
}

// Stem applies lightweight suffix stripping for common inflections (s, es, ed, ing).
func Stem(w string) string {
	if len(w) > 5 && strings.HasSuffix(w, "ing") {
		return w[:len(w)-3]
	}
	if len(w) > 4 && strings.HasSuffix(w, "es") {
		return w[:len(w)-2]
	}
	if len(w) > 4 && strings.HasSuffix(w, "ed") {
		return w[:len(w)-2]
	}
	if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
		return w[:len(w)-1]
	}
	return w
}

// Tokenize converts cleaned text into a slice of stemmed words excluding stop-words.
func Tokenize(text string) []string {
	cleaned := CleanText(text)
	if cleaned == "" {
		return nil
	}
	words := strings.Split(cleaned, " ")
	tokens := make([]string, 0, len(words))
	for _, w := range words {
		if len(w) < 2 {
			continue
		}
		if _, isStop := defaultStopwords[w]; !isStop {
			tokens = append(tokens, Stem(w))
		}
	}
	return tokens
}

// FilterClusterableArticles excludes ultra-short low-information articles from clustering
// while keeping them intact in raw storage.
func FilterClusterableArticles(articles []model.Article, minTokens int) []model.Article {
	if minTokens <= 0 {
		minTokens = 4
	}
	out := make([]model.Article, 0, len(articles))
	for _, art := range articles {
		toks := Tokenize(art.Title + " " + art.Description)
		if len(toks) >= minTokens {
			out = append(out, art)
		}
	}
	return out
}

// ShingleHashes generates sorted unique 64-bit FNV-1a hashes of unigrams and 2-grams.
func ShingleHashes(tokens []string) []uint64 {
	if len(tokens) == 0 {
		return nil
	}

	seen := make(map[uint64]struct{}, len(tokens)*2)
	hashes := make([]uint64, 0, len(tokens)*2)

	h := fnv.New64a()

	for _, t := range tokens {
		h.Reset()
		_, _ = h.Write([]byte(t))
		val := h.Sum64()
		if _, exists := seen[val]; !exists {
			seen[val] = struct{}{}
			hashes = append(hashes, val)
		}
	}

	for i := 0; i < len(tokens)-1; i++ {
		h.Reset()
		_, _ = h.Write([]byte(tokens[i]))
		_, _ = h.Write([]byte(" "))
		_, _ = h.Write([]byte(tokens[i+1]))
		val := h.Sum64()
		if _, exists := seen[val]; !exists {
			seen[val] = struct{}{}
			hashes = append(hashes, val)
		}
	}

	sort.Slice(hashes, func(i, j int) bool {
		return hashes[i] < hashes[j]
	})
	return hashes
}

// Jaccard computes the set intersection over union between two sorted slices of uint64 hashes.
func Jaccard(a, b []uint64) float64 {
	i, j, intersection := 0, 0, 0
	for i < len(a) && j < len(b) {
		if a[i] == b[j] {
			intersection++
			i++
			j++
		} else if a[i] < b[j] {
			i++
		} else {
			j++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// DisjointSet implements Union-Find with path compression and rank optimization.
type DisjointSet struct {
	parent []int
	rank   []int
}

// NewDisjointSet creates a DisjointSet with size elements.
func NewDisjointSet(size int) *DisjointSet {
	ds := &DisjointSet{
		parent: make([]int, size),
		rank:   make([]int, size),
	}
	for i := 0; i < size; i++ {
		ds.parent[i] = i
	}
	return ds
}

// Find returns the root representative of element i with path compression.
func (ds *DisjointSet) Find(i int) int {
	if ds.parent[i] != i {
		ds.parent[i] = ds.Find(ds.parent[i])
	}
	return ds.parent[i]
}

// Union merges the sets containing element i and element j.
func (ds *DisjointSet) Union(i, j int) {
	rootI := ds.Find(i)
	rootJ := ds.Find(j)
	if rootI == rootJ {
		return
	}
	if ds.rank[rootI] < ds.rank[rootJ] {
		ds.parent[rootI] = rootJ
	} else if ds.rank[rootI] > ds.rank[rootJ] {
		ds.parent[rootJ] = rootI
	} else {
		ds.parent[rootJ] = rootI
		ds.rank[rootI]++
	}
}

type sparseVec map[string]float64

func normalizeVec(v sparseVec) sparseVec {
	var normSq float64
	for _, w := range v {
		normSq += w * w
	}
	if normSq == 0 {
		return v
	}
	invNorm := 1.0 / math.Sqrt(normSq)
	out := make(sparseVec, len(v))
	for k, w := range v {
		out[k] = w * invNorm
	}
	return out
}

func dotVec(a, b sparseVec) float64 {
	if len(a) > len(b) {
		a, b = b, a
	}
	var sum float64
	for k, va := range a {
		if vb, ok := b[k]; ok {
			sum += va * vb
		}
	}
	return sum
}

func mergeCentroids(a, b sparseVec, sizeA, sizeB int) sparseVec {
	merged := make(sparseVec, len(a)+len(b))
	wa := float64(sizeA)
	wb := float64(sizeB)
	for k, v := range a {
		merged[k] += v * wa
	}
	for k, v := range b {
		merged[k] += v * wb
	}
	return normalizeVec(merged)
}

// ClusterArticles groups articles using inverted-index pruning, local BM25/TF-IDF cosine
// similarity, and bounded complete/centroid linkage to prevent mega-cluster chaining.
func ClusterArticles(articles []model.Article, threshold float64) [][]model.Article {
	groups, _ := ClusterArticlesWithStats(articles, threshold)
	return groups
}

// ClusterArticlesWithStats runs bounded TF-IDF clustering and returns candidate-pruning telemetry.
func ClusterArticlesWithStats(articles []model.Article, threshold float64) ([][]model.Article, ClusterStats) {
	n := len(articles)
	stats := ClusterStats{TotalArticles: n}
	if n == 0 {
		return nil, stats
	}

	stats.TotalPossiblePairs = n * (n - 1) / 2

	titleTokenList := make([][]string, n)
	descTokenList := make([][]string, n)
	df := make(map[string]int, n*8)

	// Step 1: Tokenize, compute shingles, and build document frequencies for IDF.
	for i := range articles {
		titleTokens := Tokenize(articles[i].Title)
		articles[i].TitleShingles = ShingleHashes(titleTokens)
		titleTokenList[i] = titleTokens

		descTokens := Tokenize(articles[i].Description)
		if len(descTokens) > 25 {
			descTokens = descTokens[:25]
		}
		descTokenList[i] = descTokens

		combinedTokens := make([]string, 0, len(titleTokens)+len(descTokens))
		combinedTokens = append(combinedTokens, titleTokens...)
		combinedTokens = append(combinedTokens, descTokens...)
		articles[i].Shingles = ShingleHashes(combinedTokens)

		seenTerms := make(map[string]struct{}, len(combinedTokens))
		for _, tok := range combinedTokens {
			if _, ok := seenTerms[tok]; !ok {
				seenTerms[tok] = struct{}{}
				df[tok]++
			}
		}
	}

	// Step 2: Build smoothed TF-IDF IDF weights (1 + ln((1+N)/(1+df))) and L2-normalized sparse vectors.
	const k1 = 1.2
	idf := make(map[string]float64, len(df))
	for term, freq := range df {
		idf[term] = 1.0 + math.Log(float64(n+1)/float64(freq+1))
	}

	vectors := make([]sparseVec, n)
	titleVectors := make([]sparseVec, n)
	for i := 0; i < n; i++ {
		tfCombined := make(map[string]float64, len(titleTokenList[i])+len(descTokenList[i]))
		tfTitle := make(map[string]float64, len(titleTokenList[i]))
		for _, tok := range titleTokenList[i] {
			tfCombined[tok] += 2.0
			tfTitle[tok] += 1.0
		}
		for _, tok := range descTokenList[i] {
			tfCombined[tok] += 1.0
		}

		vec := make(sparseVec, len(tfCombined))
		for tok, tf := range tfCombined {
			bm25tf := (tf * (k1 + 1.0)) / (tf + k1)
			vec[tok] = bm25tf * idf[tok]
		}
		vectors[i] = normalizeVec(vec)

		tVec := make(sparseVec, len(tfTitle))
		for tok, tf := range tfTitle {
			bm25tf := (tf * (k1 + 1.0)) / (tf + k1)
			tVec[tok] = bm25tf * idf[tok]
		}
		titleVectors[i] = normalizeVec(tVec)
	}

	// Step 3: Build inverted shingle index for fast candidate pruning.
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

	// Step 4: Score candidate pairs via BM25/TF-IDF cosine similarity.
	type candidatePair struct {
		i   int
		j   int
		sim float64
	}
	var qualifying []candidatePair
	evaluated := make(map[uint64]struct{})

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
			if _, done := evaluated[pairKey]; done {
				continue
			}
			evaluated[pairKey] = struct{}{}

			combinedSim := dotVec(vectors[i], vectors[j])
			titleSim := dotVec(titleVectors[i], titleVectors[j]) * 0.92
			sim := combinedSim
			if titleSim > sim {
				sim = titleSim
			}

			if sim >= threshold {
				qualifying = append(qualifying, candidatePair{i: i, j: j, sim: sim})
			}
		}
	}

	stats.EvaluatedPairs = len(evaluated)
	if stats.TotalPossiblePairs > 0 {
		stats.PrunedPairs = stats.TotalPossiblePairs - stats.EvaluatedPairs
		if stats.PrunedPairs < 0 {
			stats.PrunedPairs = 0
		}
		stats.PrunedRatio = float64(stats.PrunedPairs) / float64(stats.TotalPossiblePairs)
	}

	// Step 5: Bounded agglomerative merging (strongest pairs first, guarded by centroid + complete-linkage floor).
	sort.Slice(qualifying, func(a, b int) bool {
		return qualifying[a].sim > qualifying[b].sim
	})

	ds := NewDisjointSet(n)
	members := make([][]int, n)
	centroids := make([]sparseVec, n)
	for i := 0; i < n; i++ {
		members[i] = []int{i}
		centroids[i] = vectors[i]
	}

	const maxClusterDiameterSize = 50
	minLinkFloor := threshold * 0.60

	for _, pair := range qualifying {
		rootI := ds.Find(pair.i)
		rootJ := ds.Find(pair.j)
		if rootI == rootJ {
			continue
		}

		mI := members[rootI]
		mJ := members[rootJ]
		if len(mI)+len(mJ) > maxClusterDiameterSize {
			continue
		}

		// Centroid guard
		if dotVec(centroids[rootI], centroids[rootJ]) < threshold*0.85 {
			continue
		}

		// Complete-linkage floor guard to block transitive A-B-C mega-cluster chaining
		bounded := true
		for _, u := range mI {
			for _, v := range mJ {
				simUV := dotVec(vectors[u], vectors[v])
				tSimUV := dotVec(titleVectors[u], titleVectors[v]) * 0.92
				if tSimUV > simUV {
					simUV = tSimUV
				}
				if simUV < minLinkFloor {
					bounded = false
					break
				}
			}
			if !bounded {
				break
			}
		}
		if !bounded {
			continue
		}

		ds.Union(rootI, rootJ)
		newRoot := ds.Find(rootI)
		otherRoot := rootJ
		if newRoot == rootJ {
			otherRoot = rootI
		}

		centroids[newRoot] = mergeCentroids(centroids[rootI], centroids[rootJ], len(mI), len(mJ))
		members[newRoot] = append(members[newRoot], members[otherRoot]...)
		members[otherRoot] = nil
	}

	// Step 6: Collect final cluster groups.
	groups := make(map[int][]model.Article)
	for i := 0; i < n; i++ {
		root := ds.Find(i)
		groups[root] = append(groups[root], articles[i])
	}

	result := make([][]model.Article, 0, len(groups))
	for _, group := range groups {
		result = append(result, group)
	}

	return result, stats
}
