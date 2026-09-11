package cluster

import (
	"hash/fnv"
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

// ShingleHashes generates sorted unique 64-bit FNV-1a hashes of unigrams and 2-grams.
func ShingleHashes(tokens []string) []uint64 {
	if len(tokens) == 0 {
		return nil
	}

	seen := make(map[uint64]struct{}, len(tokens)*2)
	hashes := make([]uint64, 0, len(tokens)*2)

	h := fnv.New64a()

	// Unigram hashes (bag-of-words)
	for _, t := range tokens {
		h.Reset()
		_, _ = h.Write([]byte(t))
		val := h.Sum64()
		if _, exists := seen[val]; !exists {
			seen[val] = struct{}{}
			hashes = append(hashes, val)
		}
	}

	// Bigram hashes (2-grams)
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

// ClusterArticles groups articles using shingling, an inverted index, and Union-Find.
func ClusterArticles(articles []model.Article, threshold float64) [][]model.Article {
	n := len(articles)
	if n == 0 {
		return nil
	}

	// Step 1: Compute title and combined shingle hashes for all articles.
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

	// Step 2: Build inverted index: token_hash -> []article_idx.
	invertedIndex := make(map[uint64][]int)
	for idx, art := range articles {
		// Index all unique shingles (title and combined)
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

	// Step 3: Candidate pairing via shared shingles and Union-Find merging.
	ds := NewDisjointSet(n)
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
			// Require at least 2 shared shingles for candidate consideration.
			if count < 2 {
				continue
			}
			pairKey := (uint64(i) << 32) | uint64(j)
			if _, done := compared[pairKey]; done {
				continue
			}
			compared[pairKey] = struct{}{}

			titleSim := Jaccard(articles[i].TitleShingles, articles[j].TitleShingles)
			combinedSim := Jaccard(articles[i].Shingles, articles[j].Shingles)
			sim := titleSim
			if combinedSim > sim {
				sim = combinedSim
			}

			if sim >= threshold {
				ds.Union(i, j)
			}
		}
	}

	// Step 4: Group articles by cluster root.
	groups := make(map[int][]model.Article)
	for i := 0; i < n; i++ {
		root := ds.Find(i)
		groups[root] = append(groups[root], articles[i])
	}

	result := make([][]model.Article, 0, len(groups))
	for _, group := range groups {
		result = append(result, group)
	}

	return result
}
