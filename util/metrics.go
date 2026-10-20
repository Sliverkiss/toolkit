package util

import (
	"sync"
	"time"
)

// Metrics is a lightweight in-process counter set. Port of app/util/metrics.py.
type Metrics struct {
	mu              sync.Mutex
	startedAt       float64
	RequestsTotal   int
	BySurface       map[string]int
	ByFCMode        map[string]int
	ByStatus        map[string]int
	PromptFCRetries int
	Errors          int
}

// NewMetrics creates a metrics instance with the current start time.
func NewMetrics() *Metrics {
	return &Metrics{
		startedAt: float64(time.Now().UnixNano()) / 1e9,
		BySurface: map[string]int{},
		ByFCMode:  map[string]int{},
		ByStatus:  map[string]int{},
	}
}

// GLOBALMETRICS mirrors the Python module-level GLOBAL_METRICS.
var GLOBALMETRICS = NewMetrics()

// RecordRequest records one request outcome.
func (m *Metrics) RecordRequest(surface, fcMode string, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RequestsTotal++
	m.BySurface[surface]++
	m.ByFCMode[fcMode]++
	if ok {
		m.ByStatus["ok"]++
	} else {
		m.ByStatus["error"]++
		m.Errors++
	}
}

// RecordRetry records one prompt-FC parse retry.
func (m *Metrics) RecordRetry() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PromptFCRetries++
}

// Snapshot returns a copy of the counters plus uptime.
func (m *Metrics) Snapshot() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := float64(time.Now().UnixNano()) / 1e9
	return map[string]any{
		"uptime_seconds":    int(now - m.startedAt),
		"requests_total":    m.RequestsTotal,
		"errors":            m.Errors,
		"prompt_fc_retries": m.PromptFCRetries,
		"by_surface":        copyMap(m.BySurface),
		"by_fc_mode":        copyMap(m.ByFCMode),
		"by_status":         copyMap(m.ByStatus),
	}
}

func copyMap(src map[string]int) map[string]int {
	out := make(map[string]int, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
