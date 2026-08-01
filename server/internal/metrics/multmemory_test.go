package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMemoryMetricsExposeBoundedAggregateContract(t *testing.T) {
	m := NewMemoryMetrics()
	reg := prometheus.NewRegistry()
	reg.MustRegister(m.Collectors()...)
	m.RecordRequest("hindsight", "dual_write", "capture", "partial")
	m.RecordRequest("hindsight", "dual_write", "capture", "ok")
	if got := testutil.ToFloat64(m.requests.WithLabelValues("hindsight", "dual_write", "global", "capture", "ok", "none")); got != 1 {
		t.Fatalf("recorded request counter = %v, want 1", got)
	}
	m.ObserveDualWriteLag(0.125)
	m.RecordRecallComparison("match")
	m.RecordRecallComparison("divergent")
	m.RecordRecallFeedback("useful")
	m.RecordRecallFeedback("not_useful")
	m.RecordRecallTokens(5, 8)
	m.RecordRecallCost(0.02)
	m.SetStorageBytes("hindsight", 1024)
	m.SetStorageBytes("mem0", 2048)
	m.RecordHealth("hindsight", true)
	m.RecordHealth("mem0", false)
	if got := testutil.ToFloat64(m.providerHealth.WithLabelValues("hindsight", "dual_write", "global", "health", "ok")); got != 1 {
		t.Fatalf("healthy availability = %v, want 1", got)
	}
	m.RecordHealth("hindsight", false)
	if got := testutil.ToFloat64(m.providerHealth.WithLabelValues("hindsight", "dual_write", "global", "health", "ok")); got != 0 {
		t.Fatalf("unhealthy availability = %v, want 0", got)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"multmemory_requests_total": false, "multmemory_dual_write_lag_seconds": false,
		"multmemory_paired_recall_total": false, "multmemory_recall_feedback_total": false,
		"multmemory_tokens_total": false, "multmemory_cost_usd_total": false,
		"multmemory_storage_bytes": false, "multmemory_provider_health": false,
	}
	for _, family := range families {
		if _, ok := want[family.GetName()]; !ok {
			continue
		}
		want[family.GetName()] = true
		for _, metric := range family.Metric {
			for _, pair := range metric.Label {
				value := pair.GetValue()
				if strings.Contains(value, "-") || strings.Contains(value, "http") || strings.Contains(value, "secret") {
					t.Fatalf("%s contains unsafe label value %q", family.GetName(), value)
				}
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing metric family %s", name)
		}
	}
}
