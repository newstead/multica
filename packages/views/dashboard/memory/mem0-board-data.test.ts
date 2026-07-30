import { describe, expect, it } from "vitest";
import type {
  MemoryConfigResponse,
  MemoryMem0BoardDelivery,
  MemoryMem0BoardResponse,
  MemoryRecallSample,
} from "@multica/core/types";
import { buildMem0BoardSummary, filterMem0Board } from "./mem0-board-data";

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

function delivery(partial: Partial<MemoryMem0BoardDelivery>): MemoryMem0BoardDelivery {
  return {
    id: "delivery-1",
    workspace_id: "ws-1",
    memory_event_id: "event-1",
    event_type: "retain",
    provider: "mem0",
    status: "delivered",
    attempt_count: 1,
    delivery_lag_ms: 85,
    event_created_at: "2026-07-29T09:59:58Z",
    delivery_created_at: "2026-07-29T09:59:58Z",
    last_attempt_at: "2026-07-29T10:00:00Z",
    updated_at: "2026-07-29T10:00:00Z",
    ...partial,
  };
}

function board(partial: Partial<MemoryMem0BoardResponse>): MemoryMem0BoardResponse {
  return {
    health: { provider: "mem0", ok: true },
    deliveries: [],
    recall_samples: [],
    ...partial,
  };
}

describe("mem0 board data", () => {
  it("derives health, recall, write outcomes, and audit rows from real board rows", () => {
    const summary = buildMem0BoardSummary(config, board({
      deliveries: [
        delivery({ id: "add-ok", event_type: "retain", delivery_lag_ms: 80 }),
        delivery({ id: "update-ok", event_type: "update", delivery_lag_ms: 120 }),
        delivery({
          id: "delete-error",
          event_type: "delete",
          status: "terminal_failed",
        }),
      ],
      recall_samples: [sample({ id: "search-ok" })],
    }));

    expect(summary.configured).toBe(true);
    expect(summary.health).toBe("healthy");
    expect(summary.searchCount).toBe(1);
    expect(summary.addCount).toBe(1);
    expect(summary.avgSearchLatencyMs).toBeNull();
    expect(summary.avgAddLatencyMs).toBe(80);
    expect(summary.tokens).toBeNull();
    expect(summary.costUsd).toBeNull();
    expect(summary.memoryCount).toBeNull();
    expect(summary.entityCount).toBeNull();
    expect(summary.storageBytes).toBeNull();
    expect(summary.outcomes).toEqual([
      { operation: "add", ok: 1, error: 0 },
      { operation: "search", ok: 1, error: 0 },
      { operation: "update", ok: 1, error: 0 },
      { operation: "delete", ok: 0, error: 1 },
    ]);
    expect(summary.auditRows).toHaveLength(4);
    expect(summary.auditRows.find((row) => row.id === "delete-error")?.status).toBe("error");
  });

  it("does not count pending, skipped, or unknown delivery statuses as ok outcomes", () => {
    const summary = buildMem0BoardSummary(config, board({
      deliveries: [
        delivery({ id: "queued-add", event_type: "retain", status: "queued" }),
        delivery({ id: "delivering-update", event_type: "update", status: "delivering" }),
        delivery({ id: "skipped-delete", event_type: "delete", status: "skipped" }),
        delivery({ id: "unknown-write", event_type: "retain", status: "provider_paused" }),
        delivery({ id: "retry-add", event_type: "retain", status: "retry" }),
      ],
    }));

    expect(summary.outcomes).toEqual([{ operation: "add", ok: 0, error: 1 }]);
    expect(summary.auditRows).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ id: "queued-add", status: "unknown" }),
        expect.objectContaining({ id: "delivering-update", status: "unknown" }),
        expect.objectContaining({ id: "skipped-delete", status: "unknown" }),
        expect.objectContaining({ id: "unknown-write", status: "unknown" }),
        expect.objectContaining({ id: "retry-add", status: "error" }),
      ]),
    );
  });

  it("derives history success and failure outcomes from delivery audit rows", () => {
    const summary = buildMem0BoardSummary(config, board({
      deliveries: [
        delivery({ id: "history-ok", event_type: "history", status: "delivered" }),
        delivery({ id: "history-failed", event_type: "history", status: "terminal_failed" }),
      ],
    }));

    expect(summary.outcomes).toEqual([{ operation: "history", ok: 1, error: 1 }]);
    expect(summary.auditRows).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ id: "history-ok", query: "history", status: "ok" }),
        expect.objectContaining({ id: "history-failed", query: "history", status: "error" }),
      ]),
    );
  });

  it("filters board rows to mem0, selected project, time range, and text query", () => {
    const now = new Date();
    const recent = now.toISOString();
    const old = new Date(now.getTime() - 10 * 24 * 60 * 60 * 1000).toISOString();
    const filtered = filterMem0Board(board({
      deliveries: [
        delivery({ id: "keep-recall-delivery", project_id: "project-1", last_attempt_at: recent }),
        delivery({ id: "wrong-provider", provider: "hindsight", project_id: "project-1", last_attempt_at: recent }),
        delivery({ id: "wrong-project", project_id: "project-2", last_attempt_at: recent }),
        delivery({ id: "too-old", project_id: "project-1", last_attempt_at: old }),
      ],
      recall_samples: [
        sample({ id: "keep-sample", project_id: "project-1", query: "Need mem0 recall", sampled_at: recent }),
        sample({ id: "old-sample", project_id: "project-1", query: "Need mem0 recall", sampled_at: old }),
      ],
    }), { days: 7, projectId: "project-1", query: "recall" });

    expect(filtered.deliveries.map((row) => row.id)).toEqual(["keep-recall-delivery"]);
    expect(filtered.recall_samples.map((row) => row.id)).toEqual(["keep-sample"]);
  });
});
