package metrics

import (
	"math"

	"github.com/prometheus/client_golang/prometheus"
)

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
	return m
}

func (m *MemoryMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{m.requests, m.dualWriteLag, m.pairedRecall, m.feedback, m.tokens, m.cost, m.storage, m.providerHealth}
}

func (m *MemoryMetrics) RecordRequest(provider, mode, operation, result string) {
	if m == nil || !memoryProvider(provider) || !memoryMode(mode) || !memoryOperation(operation) || !memoryResult(result) {
		return
	}
	m.requests.WithLabelValues(provider, mode, "global", operation, result, "none").Inc()
}

// ObserveDualWriteLag records a completed dual-write delivery. It is called
// only after the durable delivery row has been updated successfully.
func (m *MemoryMetrics) ObserveDualWriteLag(seconds float64) {
	if m == nil || !finiteNonNegative(seconds) {
		return
	}
	m.dualWriteLag.WithLabelValues("dual", "dual_write", "global", "upsert", "ok", "none").Observe(seconds)
}

// RecordRecallComparison records a completed provider-neutral paired recall.
func (m *MemoryMetrics) RecordRecallComparison(comparison string) {
	if m == nil || (comparison != "match" && comparison != "divergent" && comparison != "incomparable") {
		return
	}
	m.pairedRecall.WithLabelValues("gateway", "shadow", "global", "recall", "ok", "none", comparison).Inc()
}

// RecordRecallFeedback records an explicit bounded feedback selection.
func (m *MemoryMetrics) RecordRecallFeedback(outcome string) {
	if m == nil || (outcome != "useful" && outcome != "not_useful") {
		return
	}
	m.feedback.WithLabelValues("gateway", "shadow", "global", "recall", "ok", "none", outcome).Inc()
}

// RecordRecallTokens records the payload-token estimates calculated at a
// completed recall boundary. It never accepts a model or request identifier.
func (m *MemoryMetrics) RecordRecallTokens(input, output int) {
	if m == nil || input < 0 || output < 0 {
		return
	}
	m.tokens.WithLabelValues("gateway", "shadow", "global", "recall", "ok", "none", "input").Add(float64(input))
	m.tokens.WithLabelValues("gateway", "shadow", "global", "recall", "ok", "none", "output").Add(float64(output))
}

// RecordRecallCost records an explicitly computed variable provider cost. A
// caller must not use this to manufacture a zero when no cost source exists.
func (m *MemoryMetrics) RecordRecallCost(usd float64) {
	if m == nil || !finiteNonNegative(usd) {
		return
	}
	m.cost.WithLabelValues("gateway", "shadow", "global", "recall", "ok", "none").Add(usd)
}

// SetStorageBytes records a provider-reported aggregate storage measurement.
// It must never be populated from an envelope length or other surrogate.
func (m *MemoryMetrics) SetStorageBytes(provider string, bytes float64) {
	if m == nil || !memoryProvider(provider) || !finiteNonNegative(bytes) {
		return
	}
	m.storage.WithLabelValues(provider, "dual_write", "global", "backup", "ok").Set(bytes)
}

// RecordHealth records the current provider availability with two bounded
// result series. The collector reads result=ok: 1 means healthy, 0 unhealthy.
func (m *MemoryMetrics) RecordHealth(provider string, healthy bool) {
	if m == nil || !memoryProvider(provider) {
		return
	}
	ok := 0.0
	if healthy {
		ok = 1
	}
	m.providerHealth.WithLabelValues(provider, "dual_write", "global", "health", "ok").Set(ok)
	m.providerHealth.WithLabelValues(provider, "dual_write", "global", "health", "error").Set(1 - ok)
}

func memoryProvider(provider string) bool {
	return provider == "hindsight" || provider == "mem0"
}

func memoryMode(mode string) bool {
	return mode == "primary" || mode == "dual_write" || mode == "shadow"
}

func memoryOperation(operation string) bool {
	return operation == "capture" || operation == "upsert" || operation == "recall" || operation == "backup" || operation == "health"
}

func memoryResult(result string) bool {
	return result == "ok" || result == "partial" || result == "error"
}

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}
