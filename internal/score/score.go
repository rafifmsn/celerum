package score

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"celerum/internal/config"
	"celerum/pkg/model"
)

const (
	WeightClusterSize = 3.0
	WeightTier        = 1.5
	WeightKeyword     = 2.0
	HalfLifeHours     = 2.0
)

// Scorer calculates breaking news velocity scores for event clusters.
type Scorer struct {
	boostKeywords [][]string
	rawKeywords   []string
	topK          int
}

// NewScorer creates a configured Scorer.
func NewScorer(cfg *config.Config) *Scorer {
	var cleanBoost [][]string
	var rawKeywords []string
	for _, kw := range cfg.Keywords.Boost {
		trimmed := strings.ToLower(strings.TrimSpace(kw))
		if trimmed == "" {
			continue
		}
		tokens := tokenizeWords(trimmed)
		if len(tokens) == 0 {
			continue
		}
		cleanBoost = append(cleanBoost, tokens)
		rawKeywords = append(rawKeywords, trimmed)
	}
	topK := cfg.Engine.TopK
	if topK <= 0 {
		topK = 5
	}
	return &Scorer{
		boostKeywords: cleanBoost,
		rawKeywords:   rawKeywords,
		topK:          topK,
	}
}

// TierWeight returns the inverse tier multiplier weight (Tier 1 = 3.0x, Tier 2 = 2.0x, Tier 3+ = 1.0x).
func TierWeight(tier int) float64 {
	switch tier {
	case 1:
		return 3.0 * WeightTier
	case 2:
		return 2.0 * WeightTier
	default:
		return 1.0 * WeightTier
	}
}

func tokenizeWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

func containsTokenSequence(haystack, needle []string) bool {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return false
	}
	if len(needle) == 1 {
		target := needle[0]
		for _, w := range haystack {
			if w == target {
				return true
			}
		}
		return false
	}
	limit := len(haystack) - len(needle)
	for i := 0; i <= limit; i++ {
		if haystack[i] != needle[0] {
			continue
		}
		matched := true
		for j := 1; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// MatchedKeywords returns the configured boost keywords matched via token boundaries in the articles.
func (s *Scorer) MatchedKeywords(articles []model.Article) []string {
	if len(articles) == 0 || len(s.boostKeywords) == 0 {
		return nil
	}
	var sb strings.Builder
	for _, art := range articles {
		sb.WriteString(" ")
		sb.WriteString(art.Title)
		sb.WriteString(" ")
		sb.WriteString(art.Description)
	}
	words := tokenizeWords(sb.String())

	var matched []string
	for i, kwTokens := range s.boostKeywords {
		if containsTokenSequence(words, kwTokens) {
			matched = append(matched, s.rawKeywords[i])
		}
	}
	return matched
}

// UniqueSourceCount returns the number of distinct feed sources represented in the articles.
func UniqueSourceCount(articles []model.Article) int {
	seen := make(map[string]struct{}, len(articles))
	for i, art := range articles {
		seen[articleSourceKey(art, i)] = struct{}{}
	}
	return len(seen)
}

func articleSourceKey(art model.Article, idx int) string {
	if u := strings.TrimSpace(art.FeedURL); u != "" {
		return u
	}
	if n := strings.TrimSpace(art.FeedName); n != "" {
		return "name:" + n
	}
	if id := strings.TrimSpace(art.ID); id != "" {
		return "id:" + id
	}
	return fmt.Sprintf("idx:%d", idx)
}

// ScoreCluster computes heuristic signal score for a group of articles.
func (s *Scorer) ScoreCluster(articles []model.Article, now time.Time) float64 {
	if len(articles) == 0 {
		return 0.0
	}

	sourceBestTier := make(map[string]int, len(articles))
	newestTime := articles[0].PublishedAt
	var sb strings.Builder

	for i, art := range articles {
		srcKey := articleSourceKey(art, i)
		tier := art.FeedTier
		if tier <= 0 {
			tier = 3
		}
		if prev, exists := sourceBestTier[srcKey]; !exists || tier < prev {
			sourceBestTier[srcKey] = tier
		}

		if art.PublishedAt.After(newestTime) {
			newestTime = art.PublishedAt
		}
		sb.WriteString(" ")
		sb.WriteString(art.Title)
		sb.WriteString(" ")
		sb.WriteString(art.Description)
	}

	// 1. Unique source cluster size weight
	clusterSizeScore := float64(len(sourceBestTier)) * WeightClusterSize

	// 2. Inverse source tier score across unique sources
	tierSum := 0.0
	for _, tier := range sourceBestTier {
		tierSum += TierWeight(tier)
	}

	// 3. Word-boundary keyword matches
	words := tokenizeWords(sb.String())
	keywordBonus := 0.0
	for _, kwTokens := range s.boostKeywords {
		if containsTokenSequence(words, kwTokens) {
			keywordBonus += WeightKeyword
		}
	}

	// 4. Exponential half-life time decay
	ageHours := now.Sub(newestTime).Hours()
	if ageHours < 0 {
		ageHours = 0
	}
	decayFactor := math.Pow(0.5, ageHours/HalfLifeHours)

	totalScore := (clusterSizeScore + tierSum + keywordBonus) * decayFactor
	if totalScore < 0 {
		totalScore = 0
	}
	return totalScore
}

// PickRepresentative selects the highest-tier and most complete article in the cluster.
func PickRepresentative(articles []model.Article) model.Article {
	if len(articles) == 0 {
		return model.Article{}
	}

	effectiveTier := func(t int) int {
		if t <= 0 {
			return 99
		}
		return t
	}

	best := articles[0]
	for _, art := range articles[1:] {
		artTier := effectiveTier(art.FeedTier)
		bestTier := effectiveTier(best.FeedTier)
		if artTier < bestTier {
			best = art
		} else if artTier == bestTier {
			if len(art.Description) > len(best.Description) {
				best = art
			}
		}
	}
	return best
}

// ScoreAllClusters scores all clustered article groups, picks representatives, and sorts descending by score.
func (s *Scorer) ScoreAllClusters(groups [][]model.Article, now time.Time) []model.Cluster {
	clusters := make([]model.Cluster, 0, len(groups))

	for _, group := range groups {
		if len(group) == 0 {
			continue
		}
		sc := s.ScoreCluster(group, now)
		rep := PickRepresentative(group)

		clusters = append(clusters, model.Cluster{
			ID:             rep.ID,
			Articles:       group,
			Representative: rep,
			Score:          sc,
			SourceCount:    UniqueSourceCount(group),
			Category:       rep.Category,
			CreatedAt:      now,
		})
	}

	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].Score == clusters[j].Score {
			return clusters[i].Representative.PublishedAt.After(clusters[j].Representative.PublishedAt)
		}
		return clusters[i].Score > clusters[j].Score
	})

	return clusters
}

// BuildClusters scores clustered article groups, picks representatives, and returns top-K clusters.
func (s *Scorer) BuildClusters(groups [][]model.Article, now time.Time) []model.Cluster {
	clusters := s.ScoreAllClusters(groups, now)
	if len(clusters) > s.topK {
		clusters = clusters[:s.topK]
	}
	return clusters
}
