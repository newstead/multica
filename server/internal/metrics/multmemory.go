package metrics

import (
	"math"
	"sync"

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
	cost           *providerCumulativeCost
	storage        *prometheus.GaugeVec
	providerHealth *prometheus.GaugeVec
}

func NewMemoryMetrics() *MemoryMetrics {
	labels := []string{"provider", "mode", "scope_hash", "operation", "result", "model"}
	m := &MemoryMetrics{
		requests:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "multmemory_requests_total", Help: "MultMemory gateway requests after policy checks."}, labels),
		dualWriteLag: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "multmemory_dual_write_lag_seconds", Help: "Delta between dual-write provider completion.", Buckets: []float64{0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}}, labels),
		pairedRecall: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "multmemory_paired_recall_total", Help: "Aggregate primary/shadow recall comparisons."}, append(labels, "comparison")),
		feedback:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "multmemory_recall_feedback_total", Help: "Aggregate recall feedback."}, append(labels, "outcome")),
		tokens:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "multmemory_tokens_total", Help: "MultMemory provider token usage."}, append(labels, "token_type")),
		// Cost is a provider-owned cumulative counter. The source publishes the
		// absolute total, rather than an inferred local delta, so restarts do not
		// create or lose variable-cost evidence.
		cost:           newProviderCumulativeCost(),
		storage:        prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "multmemory_storage_bytes", Help: "Provider storage measurement when configured."}, []string{"provider", "mode", "scope_hash", "operation", "result"}),
		providerHealth: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "multmemory_provider_health", Help: "Provider health; one is healthy."}, []string{"provider", "mode", "scope_hash", "operation", "result"}),
	}
	// These event families have real update paths but may legitimately have no
	// observations in a healthy interval. Registering their bounded zero series
	// lets preflight distinguish that case from an absent exporter. Storage and
	// provider cost remain intentionally unregistered until their private,
	// provider-reported aggregate source succeeds.
	m.initializeReadinessSeries()
	return m
}

func (m *MemoryMetrics) initializeReadinessSeries() {
	if m == nil {
		return
	}
	m.dualWriteLag.WithLabelValues("dual", "dual_write", "global", "upsert", "ok", "none")
	m.requests.WithLabelValues("hindsight", "dual_write", "global", "capture", "partial", "none")
	m.requests.WithLabelValues("mem0", "dual_write", "global", "capture", "partial", "none")
	for _, comparison := range []string{"match", "divergent"} {
		m.pairedRecall.WithLabelValues("gateway", "shadow", "global", "recall", "ok", "none", comparison)
	}
	for _, outcome := range []string{"useful", "not_useful"} {
		m.feedback.WithLabelValues("gateway", "shadow", "global", "recall", "ok", "none", outcome)
	}
	for _, tokenType := range []string{"input", "output"} {
		m.tokens.WithLabelValues("gateway", "shadow", "global", "recall", "ok", "none", tokenType)
	}
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

// RecordDualWritePartial records one terminal, correlated dual-write outcome.
// Individual provider retries never call this method.
func (m *MemoryMetrics) RecordDualWritePartial() {
	if m == nil {
		return
	}
	m.requests.WithLabelValues("dual", "dual_write", "global", "capture", "partial", "none").Inc()
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

// SetProviderCostUSDTotal records the total returned by an approved private
// aggregate source. It must never be set from prompt sizes or local estimates.
func (m *MemoryMetrics) SetProviderCostUSDTotal(provider string, usd float64) {
	if m == nil || !memoryProvider(provider) || !finiteNonNegative(usd) {
		return
	}
	m.cost.Set(provider, usd)
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

// providerCumulativeCost exports provider-owned totals as Prometheus counters
// while retaining the source's absolute value. CounterVec only supports Add,
// which would turn a service restart into an invented delta; this collector
// deliberately uses Set semantics and CounterValue exposition instead.
type providerCumulativeCost struct {
	desc   *prometheus.Desc
	mu     sync.RWMutex
	values map[string]float64
}

func newProviderCumulativeCost() *providerCumulativeCost {
	return &providerCumulativeCost{
		desc: prometheus.NewDesc(
			"multmemory_cost_usd_total",
			"Provider-reported cumulative variable USD cost.",
			[]string{"provider", "mode", "scope_hash", "operation", "result", "model"},
			nil,
		),
		values: map[string]float64{},
	}
}

func (c *providerCumulativeCost) Set(provider string, usd float64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.values[provider] = usd
	c.mu.Unlock()
}

func (c *providerCumulativeCost) Describe(ch chan<- *prometheus.Desc) {
	if c != nil {
		ch <- c.desc
	}
}

func (c *providerCumulativeCost) Collect(ch chan<- prometheus.Metric) {
	if c == nil {
		return
	}
	c.mu.RLock()
	values := make(map[string]float64, len(c.values))
	for provider, usd := range c.values {
		values[provider] = usd
	}
	c.mu.RUnlock()
	for provider, usd := range values {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.CounterValue, usd, provider, "dual_write", "global", "recall", "ok", "none")
	}
}
