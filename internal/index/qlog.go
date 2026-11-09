package index

import (
	"sync"
	"time"
)

// QueryLog tracks query performance metrics in a thread-safe manner.
//
// Records every query's latency and result count, and computes
// aggregate statistics: total queries, average latency, and QPS
// (queries per second since the logger started).
type QueryLog struct {
	mu           sync.Mutex
	totalQueries int64
	totalLatency time.Duration // sum of all query latencies
	startTime    time.Time     // when logging started (for QPS calculation)
	recent       []QueryRecord // rolling window of last 100 queries
}

// QueryRecord is a single logged query event.
type QueryRecord struct {
	Query   string        `json:"query"`
	Latency time.Duration `json:"latency_ns"` // in nanoseconds
	Results int           `json:"results"`
	Time    time.Time     `json:"time"`
}

// QueryLogStats is the JSON-serializable stats output.
type QueryLogStats struct {
	TotalQueries  int64          `json:"total_queries"`
	AvgLatencyMs  float64        `json:"avg_latency_ms"`
	QPS           float64        `json:"qps"`
	UptimeSeconds float64        `json:"uptime_seconds"`
	Recent        []QueryRecord  `json:"recent"` // last N queries for debugging
}

// NewQueryLog creates a new QueryLog with the start time set to now.
func NewQueryLog() *QueryLog {
	return &QueryLog{
		startTime: time.Now(),
		recent:    make([]QueryRecord, 0, 100),
	}
}

// Record logs a single query event.
//
// Thread-safe — called from HTTP handlers that may serve concurrent requests.
func (ql *QueryLog) Record(query string, latency time.Duration, resultCount int) {
	ql.mu.Lock()
	defer ql.mu.Unlock()

	ql.totalQueries++
	ql.totalLatency += latency

	record := QueryRecord{
		Query:   query,
		Latency: latency,
		Results: resultCount,
		Time:    time.Now(),
	}

	// Rolling window: keep last 100 queries.
	if len(ql.recent) >= 100 {
		// Shift left by 1 (drop oldest).
		copy(ql.recent, ql.recent[1:])
		ql.recent[len(ql.recent)-1] = record
	} else {
		ql.recent = append(ql.recent, record)
	}
}

// Stats returns the current aggregate query statistics.
func (ql *QueryLog) Stats() QueryLogStats {
	ql.mu.Lock()
	defer ql.mu.Unlock()

	uptime := time.Since(ql.startTime).Seconds()

	avgLatencyMs := 0.0
	if ql.totalQueries > 0 {
		avgLatencyMs = float64(ql.totalLatency.Microseconds()) / float64(ql.totalQueries) / 1000.0
	}

	qps := 0.0
	if uptime > 0 {
		qps = float64(ql.totalQueries) / uptime
	}

	// Copy recent slice to avoid data races.
	recentCopy := make([]QueryRecord, len(ql.recent))
	copy(recentCopy, ql.recent)

	return QueryLogStats{
		TotalQueries:  ql.totalQueries,
		AvgLatencyMs:  avgLatencyMs,
		QPS:           qps,
		UptimeSeconds: uptime,
		Recent:        recentCopy,
	}
}
