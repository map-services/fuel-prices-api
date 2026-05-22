package metrics

import (
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type ClientFetchMetrics struct {
	ResponseLatency    *prometheus.HistogramVec
	ResponseStatusCode *prometheus.CounterVec
	ItemsFetchedTotal  *prometheus.CounterVec
	ItemsDroppedTotal  *prometheus.CounterVec
}

func NewClientFetchMetrics(reg prometheus.Registerer) *ClientFetchMetrics {
	m := &ClientFetchMetrics{
		ResponseLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "fuel_prices_govuk_api_http_response_latency_seconds",
				Help:    "GOV.UK fuel finder client API HTTP response latency in seconds.",
				Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, math.Inf(1)},
			},
			[]string{"path", "method"},
		),
		ResponseStatusCode: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "fuel_prices_govuk_api_http_response_status_codes_total",
				Help: "GOV.UK fuel finder client API total number of HTTP responses by status code.",
			},
			[]string{"path", "method", "status_code"},
		),
		ItemsFetchedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "fuel_prices_govuk_api_items_fetched_total",
				Help: "GOV.UK fuel finder client API total number of items fetched from the upstream API.",
			},
			[]string{"path"},
		),
		ItemsDroppedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "fuel_prices_govuk_api_items_dropped_total",
				Help: "GOV.UK fuel finder client API total number of items dropped from the upstream API.",
			},
			[]string{"path"},
		),
	}

	RegisterOrPanic(reg,
		m.ResponseLatency,
		m.ResponseStatusCode,
		m.ItemsFetchedTotal,
		m.ItemsDroppedTotal,
	)

	return m
}

func (m *ClientFetchMetrics) RecordHttpCall(start time.Time, method, endpoint string, resp *http.Response, err error) {
	if m != nil {
		u, parseErr := url.Parse(endpoint)
		path := u.Path
		if parseErr != nil {
			slog.Error("failed to parse endpoint URL for metrics", "endpoint", endpoint, "error", parseErr)
			path = "invalid_url"
		}
		m.ResponseLatency.WithLabelValues(path, method).Observe(time.Since(start).Seconds())
		if err == nil {
			m.ResponseStatusCode.WithLabelValues(path, method, strconv.Itoa(resp.StatusCode)).Inc()
		}
	}
}

func (m *ClientFetchMetrics) RecordFetchedItems(path string, count int, dropped int) {
	if m == nil {
		return
	}

	if count > 0 {
		m.ItemsFetchedTotal.WithLabelValues(path).Add(float64(count))
	}

	if dropped > 0 {
		m.ItemsDroppedTotal.WithLabelValues(path).Add(float64(dropped))
	}
}
