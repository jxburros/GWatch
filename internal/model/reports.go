package model

import "time"

type ReportDefinition struct {
	IncludeLatencyCharts bool      `json:"includeLatencyCharts"`
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	Enabled              bool      `json:"enabled"`
	Groups               []string  `json:"groups"`
	Tags                 []string  `json:"tags"`
	Period               string    `json:"period"`
	Recipients           []string  `json:"recipients"`
	TargetAvailability   float64   `json:"targetAvailability"`
	CreatedAt            time.Time `json:"createdAt"`
}
