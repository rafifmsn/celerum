package model

// SourceInfo represents an individual source within a dispatched cluster payload.
type SourceInfo struct {
	Name        string `json:"name"`
	Tier        int    `json:"tier"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	PublishedAt int64  `json:"published_at"`
}

// Payload represents the alert delivered to downstream dispatchers.
type Payload struct {
	ID          string       `json:"id"`
	FeedName    string       `json:"feed_name"`
	Title       string       `json:"title"`
	Content     string       `json:"content"`
	Enriched    bool         `json:"enriched"`
	ClusterSize int          `json:"cluster_size"`
	Score       float64      `json:"score"`
	Sources     []SourceInfo `json:"sources"`
	Timestamp   int64        `json:"timestamp"`
}

