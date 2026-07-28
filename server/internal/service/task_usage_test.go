package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestTaskUsageCostUSDTicksUsesEstimateOnlyForPricedModels(t *testing.T) {
	stored := taskUsageCostUSDTicks("gpt-5.4", 1_000_000, 2_000_000, 3_000_000, 4_000_000, 0)
	want, ok := obsmetrics.EstimateLLMUsageCostTicks("gpt-5.4", 1_000_000, 2_000_000, 3_000_000, 4_000_000)
	if !ok {
		t.Fatal("test model did not resolve to a price")
	}
	if !stored.Valid || stored.Int64 != want {
		t.Fatalf("estimated cost = %+v, want valid %d", stored, want)
	}

	stored = taskUsageCostUSDTicks("Free Model!!", 7, 0, 0, 0, 0)
	if stored.Valid {
		t.Fatalf("unpriced model cost = %+v, want NULL", stored)
	}

	stored = taskUsageCostUSDTicks("gpt-5.4", 1_000_000, 2_000_000, 0, 0, 42)
	if !stored.Valid || stored.Int64 != 42 {
		t.Fatalf("provider cost = %+v, want authoritative 42", stored)
	}
}

func TestRecordLLMUsageUnpricedModelStillFeedsUnpricedMetric(t *testing.T) {
	metrics := obsmetrics.NewBusinessMetrics()
	registry := prometheus.NewRegistry()
	registry.MustRegister(metrics.Collectors()...)

	metrics.RecordLLMUsage("issue", "local", "custom-provider", "Free Model!!", 7, 0, 0, 0, 13, 0)

	if err := testutil.GatherAndCompare(registry, strings.NewReader(`
# HELP multica_llm_unpriced_tokens_total Total LLM tokens for model aliases without a fixed TSR price.
# TYPE multica_llm_unpriced_tokens_total counter
multica_llm_unpriced_tokens_total{model_alias="free_model_",provider="other",token_type="input"} 7
multica_llm_unpriced_tokens_total{model_alias="free_model_",provider="other",token_type="reasoning"} 13
`), "multica_llm_unpriced_tokens_total"); err != nil {
		t.Fatalf("unpriced metrics mismatch: %v", err)
	}
}

func TestCaptureTaskUsagePersistsEstimatedCostAndLeavesUnpricedNull(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	queries := db.New(pool)
	metrics := obsmetrics.NewBusinessMetrics()
	svc := NewTaskService(queries, pool, nil, nil)
	svc.Metrics = metrics

	registry := prometheus.NewRegistry()
	registry.MustRegister(metrics.Collectors()...)

	taskID, _, _ := dispatchedCommentTaskFixture(t, ctx, pool)
	task, err := queries.GetAgentTask(ctx, pgtype.UUID{Bytes: mustParseUUIDBytes(t, taskID), Valid: true})
	if err != nil {
		t.Fatalf("load task: %v", err)
	}

	if err := svc.CaptureTaskUsage(ctx, task, "codex", "gpt-5.4", 1_000_000, 2_000_000, 3_000_000, 4_000_000, 5_000_000, 0); err != nil {
		t.Fatalf("capture priced usage: %v", err)
	}
	wantCost, ok := obsmetrics.EstimateLLMUsageCostTicks("gpt-5.4", 1_000_000, 2_000_000, 3_000_000, 4_000_000)
	if !ok {
		t.Fatal("test model did not resolve to a price")
	}
	var storedCost pgtype.Int8
	if err := pool.QueryRow(ctx, `
		SELECT cost_usd_ticks
		FROM task_usage
		WHERE task_id = $1 AND provider = 'codex' AND model = 'gpt-5.4'
	`, taskID).Scan(&storedCost); err != nil {
		t.Fatalf("read priced cost: %v", err)
	}
	if !storedCost.Valid || storedCost.Int64 != wantCost {
		t.Fatalf("stored priced cost = %+v, want valid %d", storedCost, wantCost)
	}

	if err := svc.CaptureTaskUsage(ctx, task, "custom-provider", "Free Model!!", 7, 0, 0, 0, 13, 0); err != nil {
		t.Fatalf("capture unpriced usage: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT cost_usd_ticks
		FROM task_usage
		WHERE task_id = $1 AND provider = 'custom-provider' AND model = 'Free Model!!'
	`, taskID).Scan(&storedCost); err != nil {
		t.Fatalf("read unpriced cost: %v", err)
	}
	if storedCost.Valid {
		t.Fatalf("stored unpriced cost = %+v, want NULL", storedCost)
	}

	if err := testutil.GatherAndCompare(registry, strings.NewReader(`
# HELP multica_llm_unpriced_tokens_total Total LLM tokens for model aliases without a fixed TSR price.
# TYPE multica_llm_unpriced_tokens_total counter
multica_llm_unpriced_tokens_total{model_alias="free_model_",provider="other",token_type="input"} 7
multica_llm_unpriced_tokens_total{model_alias="free_model_",provider="other",token_type="reasoning"} 13
`), "multica_llm_unpriced_tokens_total"); err != nil {
		t.Fatalf("unpriced metrics mismatch: %v", err)
	}
}

func mustParseUUIDBytes(t *testing.T, s string) [16]byte {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(s); err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return id.Bytes
}
