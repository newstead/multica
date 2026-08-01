package metrics

import "github.com/prometheus/client_golang/prometheus"

// MemoryMetrics is the bounded, provider-neutral MultMemory metric contract.
// It intentionally has no scope or content inputs: steady-state telemetry is
// emitted only with scope_hash=global, so raw identifiers cannot reach a
// scrape through this collector.
type MemoryMetrics struct {
	requests       *prometheus.CounterVec
	dualWriteLag   *prometheus.HistogramVec
	pairedRecall   *prometheus.CounterVec
	feedback       *prometheus.CounterVec
	tokens         *prometheus.CounterVec
	cost           *prometheus.CounterVec
	storage        *prometheus.GaugeVec
	providerHealth *prometheus.GaugeVec
}

func NewMemoryMetrics() *MemoryMetrics {
	labels := []string{"provider", "mode", "scope_hash", "operation", "result", "model"}
	m := &MemoryMetrics{
		requests:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "multmemory_requests_total", Help: "MultMemory gateway requests after policy checks."}, labels),
		dualWriteLag:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "multmemory_dual_write_lag_seconds", Help: "Delta between dual-write provider completion.", Buckets: []float64{0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}}, labels),
		pairedRecall:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "multmemory_paired_recall_total", Help: "Aggregate primary/shadow recall comparisons."}, append(labels, "comparison")),
		feedback:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "multmemory_recall_feedback_total", Help: "Aggregate recall feedback."}, append(labels, "outcome")),
		tokens:         prometheus.NewCounterVec(prometheus.CounterOpts{Name: "multmemory_tokens_total", Help: "MultMemory provider token usage."}, append(labels, "token_type")),
		cost:           prometheus.NewCounterVec(prometheus.CounterOpts{Name: "multmemory_cost_usd_total", Help: "MultMemory estimated provider cost."}, labels),
		storage:        prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "multmemory_storage_bytes", Help: "Provider storage measurement when configured."}, []string{"provider", "mode", "scope_hash", "operation", "result"}),
		providerHealth: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "multmemory_provider_health", Help: "Provider health; one is healthy."}, []string{"provider", "mode", "scope_hash", "operation", "result"}),
	}
	// Pre-initialize only static, global labels. This makes source readiness
	// auditable without manufacturing identifiers or dynamically growing series.
	for _, provider := range []string{"hindsight", "mem0"} {
		m.requests.WithLabelValues(provider, "dual_write", "global", "capture", "partial", "none")
		m.storage.WithLabelValues(provider, "dual_write", "global", "backup", "ok")
		m.providerHealth.WithLabelValues(provider, "dual_write", "global", "health", "error")
	}
	m.dualWriteLag.WithLabelValues("dual", "dual_write", "global", "upsert", "ok", "none")
	for _, comparison := range []string{"match", "divergent", "incomparable"} {
		m.pairedRecall.WithLabelValues("gateway", "shadow", "global", "recall", "ok", "none", comparison)
	}
	for _, outcome := range []string{"useful", "not_useful"} {
		m.feedback.WithLabelValues("gateway", "shadow", "global", "recall", "ok", "none", outcome)
	}
	for _, tokenType := range []string{"input", "output"} {
		m.tokens.WithLabelValues("gateway", "shadow", "global", "recall", "ok", "none", tokenType)
	}
	m.cost.WithLabelValues("gateway", "shadow", "global", "recall", "ok", "none")
	return m
}

func (m *MemoryMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{m.requests, m.dualWriteLag, m.pairedRecall, m.feedback, m.tokens, m.cost, m.storage, m.providerHealth}
}

func (m *MemoryMetrics) RecordRequest(provider, mode, operation, result string) {
	if m == nil {
		return
	}
	m.requests.WithLabelValues(provider, mode, "global", operation, result, "none").Inc()
}
