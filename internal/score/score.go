package score

import (
	"sort"
	"strings"
	"time"

	"celerum/internal/config"
	"celerum/pkg/model"
)

const (
	WeightClusterSize = 3.0
	WeightTier        = 1.5
	WeightKeyword     = 2.0
	DecayPerHour      = 0.5
)

// Scorer calculates breaking news velocity scores for event clusters.
type Scorer struct {
	boostKeywords []string
	topK          int
}

// NewScorer creates a configured Scorer.
func NewScorer(cfg *config.Config) *Scorer {
	var cleanBoost []string
	for _, kw := range cfg.Keywords.Boost {
		trimmed := strings.ToLower(strings.TrimSpace(kw))
		if trimmed != "" {
			cleanBoost = append(cleanBoost, trimmed)
		}
	}
	topK := cfg.Engine.TopK
	if topK <= 0 {
		topK = 5
	}
	return &Scorer{
		boostKeywords: cleanBoost,
		topK:          topK,
	}
}

// ScoreCluster computes heuristic signal score for a group of articles.
func (s *Scorer) ScoreCluster(articles []model.Article, now time.Time) float64 {
	if len(articles) == 0 {
		return 0.0
	}

	// 1. Cluster size weight
	clusterSizeScore := float64(len(articles)) * WeightClusterSize

	// 2. Source tier score
	tierSum := 0.0
	newestTime := articles[0].PublishedAt

	// Combined text for keyword scanning
	var sb strings.Builder

	for _, art := range articles {
		tierSum += float64(art.FeedTier) * WeightTier
		if art.PublishedAt.After(newestTime) {
			newestTime = art.PublishedAt
		}
		sb.WriteString(" ")
		sb.WriteString(art.Title)
		sb.WriteString(" ")
		sb.WriteString(art.Description)
	}

	combinedText := strings.ToLower(sb.String())

	// 3. Keyword matches
	keywordBonus := 0.0
	for _, kw := range s.boostKeywords {
		if strings.Contains(combinedText, kw) {
			keywordBonus += WeightKeyword
		}
	}

	// 4. Time decay: penalize older clusters relative to evaluation time
	ageHours := now.Sub(newestTime).Hours()
	if ageHours < 0 {
		ageHours = 0
	}
	timeDecay := ageHours * DecayPerHour

	totalScore := clusterSizeScore + tierSum + keywordBonus - timeDecay
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

	best := articles[0]
	for _, art := range articles[1:] {
		if art.FeedTier < best.FeedTier { // Tier 1 is highest priority
			best = art
		} else if art.FeedTier == best.FeedTier {
			if len(art.Description) > len(best.Description) {
				best = art
			}
		}
	}
	return best
}

// BuildClusters scores clustered article groups, picks representatives, and returns top-K clusters.
func (s *Scorer) BuildClusters(groups [][]model.Article, now time.Time) []model.Cluster {
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
			CreatedAt:      now,
		})
	}

	// Sort descending by score
	sort.Slice(clusters, func(i, j int) bool {
		return clusters[i].Score > clusters[j].Score
	})

	if len(clusters) > s.topK {
		clusters = clusters[:s.topK]
	}

	return clusters
}

