import { describe, expect, it } from "vitest";
import type { MemoryRecallSample } from "@multica/core/types";
import { buildHindsightBoardData } from "./hindsight-memory-page";

function sample(overrides: Partial<MemoryRecallSample> = {}): MemoryRecallSample {
  return {
    id: "sample-1",
    workspace_id: "ws-1",
    provider: "hindsight",
    read_mode: "dual",
    recall_correlation_id: "corr-1",
    query: "Find provider isolation evidence",
    results: [{ score: 0.8 }, { similarity: 0.9 }],
    provenance: { latency_ms: 120, tokens: 900, cost_usd: 0.02, source: "recall" },
    sampled_at: "2026-07-29T12:00:00Z",
    ...overrides,
  };
}

describe("buildHindsightBoardData", () => {
  it("derives live Hindsight board metrics without exposing result payload text", () => {
    const board = buildHindsightBoardData(
      [sample(), sample({ id: "sample-2", provenance: { latency_ms: 240, error: "timeout" } })],
      { workspace_id: "ws-1", enabled: true, primary_provider: "hindsight", read_mode: "dual", provider_settings: { storage_mb: 42 } },
    );

    expect(board.source).toBe("live");
    expect(board.sampleCount).toBe(2);
    expect(board.qualityScore).toBeCloseTo(0.85);
    expect(board.averageLatencyMs).toBe(180);
    expect(board.recallP95Ms).toBe(240);
    expect(board.tokens).toBe(900);
    expect(board.costUsd).toBe(0.02);
    expect(board.errorCount).toBe(1);
    expect(board.bankHealth).toBe("degraded");
    expect(board.storageMb).toBe(42);
    expect(board.samples[0]?.resultCount).toBe(2);
  });

  it("returns a seeded baseline when no Hindsight samples are present", () => {
    const board = buildHindsightBoardData([sample({ provider: "mem0" })], null);

    expect(board.source).toBe("seeded");
    expect(board.sampleCount).toBeGreaterThan(0);
    expect(board.samples.length).toBeGreaterThan(0);
  });
});
