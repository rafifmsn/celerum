package model

// SourceInfo represents an individual source within a dispatched cluster payload.
type SourceInfo struct {
	Name  string `json:"name"`
	Tier  int    `json:"tier"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

// Payload is the standardized data structure sent to Telegram and HTTP webhooks.
type Payload struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Summary     string       `json:"summary"`
	Takeaways   []string     `json:"takeaways"`
	Sentiment   *string      `json:"sentiment"`
	Enriched    bool         `json:"enriched"`
	ClusterSize int          `json:"cluster_size"`
	Score       float64      `json:"score"`
	Sources     []SourceInfo `json:"sources"`
	Timestamp   int64        `json:"timestamp"`
}

