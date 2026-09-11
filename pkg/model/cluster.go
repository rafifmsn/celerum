package model

import "time"

// Cluster represents a group of related articles detected by Union-Find clustering.
type Cluster struct {
	ID             string    `json:"id"`
	Articles       []Article `json:"articles"`
	Representative Article   `json:"representative"`
	Score          float64   `json:"score"`
	CreatedAt      time.Time `json:"created_at"`
}

