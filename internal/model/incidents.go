package model

import "time"

type IncidentNote struct {
	At    time.Time `json:"at"`
	Actor string    `json:"actor"`
	Text  string    `json:"text"`
}

// Incident groups all failed checks on a node until every check recovers.
type Incident struct {
	ID                       int64          `json:"id"`
	NodeID                   int64          `json:"nodeId"`
	NodeName                 string         `json:"nodeName"`
	State                    string         `json:"state"`
	OpenedAt                 time.Time      `json:"openedAt"`
	AcknowledgedAt           *time.Time     `json:"acknowledgedAt,omitempty"`
	AcknowledgedBy           string         `json:"acknowledgedBy,omitempty"`
	ResolvedAt               *time.Time     `json:"resolvedAt,omitempty"`
	ResolvedBy               string         `json:"resolvedBy,omitempty"`
	CheckIDs                 []int64        `json:"checkIds"`
	Notes                    []IncidentNote `json:"notes"`
	DurationSeconds          float64        `json:"durationSeconds"`
	TimeToAcknowledgeSeconds *float64       `json:"timeToAcknowledgeSeconds,omitempty"`
	TimeToResolveSeconds     *float64       `json:"timeToResolveSeconds,omitempty"`
}
