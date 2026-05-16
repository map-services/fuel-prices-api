package models

import "time"

type Snapshot struct {
	Scope             string  `json:"scope"`
	PostcodeArea      *string `json:"postcode_area,omitempty"`
	FuelType          string  `json:"fuel_type"`
	LowestPrice       float64 `json:"lowest_price"`
	AveragePrice      float64 `json:"average_price"`
	HighestPrice      float64 `json:"highest_price"`
	StandardDeviation float64 `json:"standard_deviation"`
	SampleSize        int     `json:"sample_size"`
}

type Distribution struct {
	Scope        string      `json:"scope"`
	PostcodeArea *string     `json:"postcode_area,omitempty"`
	FuelType     string      `json:"fuel_type"`
	Buckets      map[int]int `json:"buckets"`
}

type SnapshotStatistics struct {
	Snapshot    []Snapshot `json:"snapshot,omitempty"`
	LastUpdated *time.Time `json:"last_updated,omitempty"`
}

type SnapshotResponse struct {
	SnapshotStatistics
	Attribution []string `json:"attribution"`
}

type DistributionStatistics struct {
	Distribution []Distribution `json:"distribution,omitempty"`
	LastUpdated  *time.Time     `json:"last_updated,omitempty"`
}

type DistributionResponse struct {
	DistributionStatistics
	Attribution []string `json:"attribution"`
}
