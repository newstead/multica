import { describe, expect, it } from "vitest";
import type { MemoryConfigResponse, MemoryRecallSample } from "@multica/core/types";
import { buildMem0BoardSummary, filterMem0Samples } from "./mem0-board-data";

const config: MemoryConfigResponse = {
  workspace_id: "ws-1",
  enabled: true,
  primary_provider: "hindsight",
  shadow_provider: "mem0",
  read_mode: "dual",
  provider_settings: {},
};

function sample(partial: Partial<MemoryRecallSample>): MemoryRecallSample {
  return {
    id: "sample-1",
    workspace_id: "ws-1",
    provider: "mem0",
    read_mode: "dual",
    recall_correlation_id: "corr-1",
    query: "scoped memory",
    results: [{ id: "memory-1" }],
    provenance: {},
    sampled_at: "2026-07-29T10:00:00Z",
    ...partial,
  };
}

describe("mem0 board data", () => {
  it("derives health, latency, cost, counts, outcomes, and audit rows from seeded mem0 samples", () => {
    const summary = buildMem0BoardSummary(config, [
      sample({
        id: "search-ok",
        provenance: { operation: "search", latency_ms: 120, total_tokens: 44, cost_usd: 0.002, memory_count: 8, entity_count: 3, storage_bytes: 2048, status: "ok" },
      }),
      sample({
        id: "add-ok",
        query: "",
        provenance: { operation: "retain", add_latency_ms: 80, total_tokens: 20, cost_usd: 0.001, status: "success" },
      }),
      sample({
        id: "delete-error",
        query: "",
        provenance: { operation: "delete", error_message: "not found" },
        results: [],
      }),
    ]);

    expect(summary.configured).toBe(true);
    expect(summary.health).toBe("degraded");
    expect(summary.avgSearchLatencyMs).toBe(120);
    expect(summary.avgAddLatencyMs).toBe(80);
    expect(summary.tokens).toBe(64);
    expect(summary.costUsd).toBe(0.003);
    expect(summary.memoryCount).toBe(8);
    expect(summary.entityCount).toBe(3);
    expect(summary.storageBytes).toBe(2048);
    expect(summary.outcomes).toEqual([
      { operation: "search", ok: 1, error: 0 },
      { operation: "add", ok: 1, error: 0 },
      { operation: "delete", ok: 0, error: 1 },
    ]);
    expect(summary.auditRows).toHaveLength(3);
  });

  it("filters to mem0, selected project, time range, and text query", () => {
    const now = new Date();
    const recent = now.toISOString();
    const old = new Date(now.getTime() - 10 * 24 * 60 * 60 * 1000).toISOString();
    const rows = filterMem0Samples([
      sample({ id: "keep", project_id: "project-1", query: "Need mem0 recall", sampled_at: recent }),
      sample({ id: "wrong-provider", provider: "hindsight", project_id: "project-1", query: "Need mem0 recall", sampled_at: recent }),
      sample({ id: "wrong-project", project_id: "project-2", query: "Need mem0 recall", sampled_at: recent }),
      sample({ id: "too-old", project_id: "project-1", query: "Need mem0 recall", sampled_at: old }),
    ], { days: 7, projectId: "project-1", query: "recall" });

    expect(rows.map((row) => row.id)).toEqual(["keep"]);
  });
});
