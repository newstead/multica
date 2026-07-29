import { describe, expect, it } from "vitest";
import type { MemoryRecallSample } from "@multica/core/memory";
import { buildMemoryComparisonModel, summarizeRedactedProvenance } from "./analysis";

function sample(
  id: string,
  correlationId: string,
  provider: "hindsight" | "mem0",
  results: string[],
  provenance: Record<string, unknown> = {},
): MemoryRecallSample {
  return {
    id,
    workspace_id: "ws",
    provider,
    read_mode: "dual",
    recall_correlation_id: correlationId,
    query: `query ${correlationId}`,
    results,
    provenance,
    sampled_at: "2026-07-29T00:00:00Z",
  };
}

describe("buildMemoryComparisonModel", () => {
  it("uses deterministic paired fixtures when live data has no pairs", () => {
    const model = buildMemoryComparisonModel([]);

    expect(model.source).toBe("fixture");
    expect(model.sampleSize).toBe(3);
    expect(model.providers.hindsight.sampleSize).toBe(3);
    expect(model.providers.mem0.retryBacklog).toBe(2);
  });

  it("uses paired live samples only and counts unpaired coverage gaps", () => {
    const model = buildMemoryComparisonModel([
      sample("h1", "pair-1", "hindsight", ["a", "b"], { answer_correct: true, write_status: "success" }),
      sample("m1", "pair-1", "mem0", ["b", "c"], { answer_correct: false, write_status: "failed" }),
      sample("h2", "orphan", "hindsight", ["x"]),
    ]);

    expect(model.source).toBe("live");
    expect(model.sampleSize).toBe(1);
    expect(model.coverage.liveSamples).toBe(3);
    expect(model.coverage.unpairedLiveSamples).toBe(1);
    expect(model.overlapAverage).toBeCloseTo(1 / 3);
    expect(model.providers.hindsight.correctnessRate).toBe(1);
    expect(model.providers.mem0.missingDeliveries).toBe(1);
  });

  it("derives matched rank from expected memory ids", () => {
    const model = buildMemoryComparisonModel([
      sample("h1", "pair-1", "hindsight", ["target", "other"], { expected_memory_id: "target" }),
      sample("m1", "pair-1", "mem0", ["other", "target"], { expected_memory_id: "target" }),
    ]);

    expect(model.samples[0]?.hindsight.matchedRank).toBe(1);
    expect(model.samples[0]?.mem0.matchedRank).toBe(2);
    expect(model.samples[0]?.rankDelta).toBe(1);
  });
});


describe("summarizeRedactedProvenance", () => {
  it("keeps allowed metric fields and omits nested sensitive variants", () => {
    const summary = summarizeRedactedProvenance({
      expected_memory_id: "safe-id",
      latency_ms: 42,
      redacted_source: "issue:ROL-76 comment:[redacted]",
      authorization: "Bearer live-token",
      headers: {
        cookie: "session=secret-cookie",
        Authorization: "Bearer nested-token",
        "x-api-key": "api-secret",
      },
      credentials: {
        private_key: "private-secret",
        accessKeyId: "access-secret",
      },
    });
    const serialized = JSON.stringify(summary);

    expect(summary.fields).toMatchObject({
      expected_memory_id: "safe-id",
      latency_ms: 42,
      redacted_source: "issue:ROL-76 comment:[redacted]",
    });
    expect(summary.omittedFieldCount).toBeGreaterThan(0);
    expect(serialized).not.toContain("live-token");
    expect(serialized).not.toContain("secret-cookie");
    expect(serialized).not.toContain("nested-token");
    expect(serialized).not.toContain("api-secret");
    expect(serialized).not.toContain("private-secret");
    expect(serialized).not.toContain("access-secret");
  });

  it("does not serialize structured values even for allowlisted keys", () => {
    const summary = summarizeRedactedProvenance({
      expected_memory_id: { value: "hidden" },
      answer_correct: true,
    });

    expect(summary.fields.expected_memory_id).toBe("[structured value hidden]");
    expect(JSON.stringify(summary)).not.toContain("\"value\"");
  });
});
