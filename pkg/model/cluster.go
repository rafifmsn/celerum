package model

import "time"

// Cluster represents a group of related articles detected by bounded TF-IDF clustering.
type Cluster struct {
	ID             string    `json:"id"`
	Hash           string    `json:"hash,omitempty"`
	Articles       []Article `json:"articles"`
	Representative Article   `json:"representative"`
	Score          float64   `json:"score"`
	SourceCount    int       `json:"source_count,omitempty"`
	Category       string    `json:"category,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}
