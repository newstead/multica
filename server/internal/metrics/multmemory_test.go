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
	if got := testutil.ToFloat64(m.requests.WithLabelValues("hindsight", "dual_write", "global", "capture", "partial", "none")); got != 0 {
		t.Fatalf("preinitialized request counter = %v, want 0", got)
	}
	m.RecordRequest("hindsight", "dual_write", "capture", "ok")
	if got := testutil.ToFloat64(m.requests.WithLabelValues("hindsight", "dual_write", "global", "capture", "ok", "none")); got != 1 {
		t.Fatalf("recorded request counter = %v, want 1", got)
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
