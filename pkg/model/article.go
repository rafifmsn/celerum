package model

import "time"

// Article represents a normalized feed entry ingested from an RSS or Atom source.
type Article struct {
	ID          string    `json:"id"`
	FeedURL     string    `json:"feed_url"`
	FeedTier    int       `json:"feed_tier"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	URL         string    `json:"url"`
	PublishedAt   time.Time `json:"published_at"`
	TitleShingles []uint64  `json:"-"`
	Shingles      []uint64  `json:"-"`
}
