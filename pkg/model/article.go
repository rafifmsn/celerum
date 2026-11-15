package model

import "time"

// Article represents a normalized feed entry ingested from an RSS or Atom source.
type Article struct {
	ID            string    `json:"id"`
	FeedURL       string    `json:"feed_url"`
	FeedName      string    `json:"feed_name"`
	FeedTier      int       `json:"feed_tier"`
	Category      string    `json:"category"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	URL           string    `json:"url"`
	ImageURL      string    `json:"image_url,omitempty"`
	PublishedAt   time.Time `json:"published_at"`
	IngestedAt    time.Time `json:"ingested_at,omitempty"`
	TitleShingles []uint64  `json:"-"`
	Shingles      []uint64  `json:"-"`
}
