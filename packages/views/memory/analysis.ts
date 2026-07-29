import type { MemoryRecallSample } from "@multica/core/memory";

export type MemoryProvider = "hindsight" | "mem0";
export type EvidenceState = "pass" | "fail" | "unknown";
export type CorrectnessState = "correct" | "partial" | "incorrect" | "unknown";
export type WriteStatus = "success" | "retry" | "backlog" | "failed" | "missing" | "unknown";

export interface ProviderObservation {
  provider: MemoryProvider;
  sampleId: string;
  sampledAt: string;
  query: string;
  writeStatus: WriteStatus;
  attemptCount: number | null;
  replicationLagMs: number | null;
  latencyMs: number | null;
  tokenCount: number | null;
  costUsd: number | null;
  storageBytes: number | null;
  resultIds: string[];
  expectedMemoryId: string | null;
  matchedRank: number | null;
  correctness: CorrectnessState;
  confidence: number | null;
  stale: EvidenceState;
  correction: EvidenceState;
  deletion: EvidenceState;
  provenance: Record<string, unknown>;
}

export interface PairedMemorySample {
  correlationId: string;
  query: string;
  source: "live" | "fixture";
  hindsight: ProviderObservation;
  mem0: ProviderObservation;
  overlap: number;
  rankDelta: number | null;
}

export interface ProviderSummary {
  provider: MemoryProvider;
  sampleSize: number;
  writeSuccess: number;
  retryBacklog: number;
  missingDeliveries: number;
  correctnessKnown: number;
  correctnessCorrect: number;
  correctnessRate: number | null;
  correctnessCi: [number, number] | null;
  stalePass: number;
  correctionPass: number;
  deletionPass: number;
  p50LatencyMs: number | null;
  p95LatencyMs: number | null;
  p50ReplicationLagMs: number | null;
  p95ReplicationLagMs: number | null;
  totalTokens: number;
  totalCostUsd: number;
  storageBytes: number;
}

export interface RedactedProvenanceSummary {
  fields: Record<string, string | number | boolean | null>;
  omittedFieldCount: number;
}

export interface MemoryComparisonModel {
  samples: PairedMemorySample[];
  source: "live" | "fixture";
  sampleSize: number;
  coverage: {
    pairedSamples: number;
    liveSamples: number;
    unpairedLiveSamples: number;
  };
  providers: Record<MemoryProvider, ProviderSummary>;
  overlapAverage: number | null;
}

const PROVIDERS: MemoryProvider[] = ["hindsight", "mem0"];


const PROVENANCE_ALLOWLIST = new Set([
  "answer_correct",
  "confidence",
  "correction_result",
  "cost_usd",
  "delivery_lag_ms",
  "delivery_status",
  "deletion_result",
  "expected_id",
  "expected_memory_id",
  "expected_rank",
  "latency_ms",
  "matched_rank",
  "rank",
  "read_latency_ms",
  "recall_latency_ms",
  "redacted_source",
  "replication_lag_ms",
  "stale_result",
  "status",
  "storage_bytes",
  "token_count",
  "tokens",
  "total_tokens",
  "write_status",
]);

export function summarizeRedactedProvenance(
  provenance: Record<string, unknown>,
): RedactedProvenanceSummary {
  const fields: Record<string, string | number | boolean | null> = {};
  let omittedFieldCount = 0;

  for (const [key, value] of Object.entries(provenance)) {
    if (!PROVENANCE_ALLOWLIST.has(key)) {
      omittedFieldCount += countLeafFields(value);
      continue;
    }
    fields[key] = summarizeAllowedValue(value);
  }

  return { fields, omittedFieldCount };
}

function summarizeAllowedValue(value: unknown): string | number | boolean | null {
  if (value == null) return null;
  if (typeof value === "number" || typeof value === "boolean") return value;
  if (typeof value === "string") return value.length > 240 ? `${value.slice(0, 240)}...` : value;
  return "[structured value hidden]";
}

function countLeafFields(value: unknown): number {
  if (Array.isArray(value)) return value.reduce((acc, item) => acc + countLeafFields(item), 0) || 1;
  if (!value || typeof value !== "object") return 1;
  const entries = Object.values(value as Record<string, unknown>);
  return entries.reduce<number>((acc, item) => acc + countLeafFields(item), 0) || 1;
}

const FIXTURE_TIME = "2026-07-29T00:00:00Z";

export const DETERMINISTIC_MEMORY_FIXTURES: MemoryRecallSample[] = [
  fixture("fix-pair-1-h", "fix-pair-1", "hindsight", "Which owner approved deleting stale pricing memory?", ["approval-17", "pricing-note"], {
    write_status: "success",
    attempt_count: 1,
    replication_lag_ms: 420,
    latency_ms: 84,
    tokens: 740,
    cost_usd: 0.0014,
    storage_bytes: 1512,
    expected_memory_id: "approval-17",
    answer_correct: true,
    confidence: 0.88,
    stale_result: "pass",
    correction_result: "pass",
    deletion_result: "pass",
    redacted_source: "issue:ROL-10 comment:[redacted]",
  }),
  fixture("fix-pair-1-m", "fix-pair-1", "mem0", "Which owner approved deleting stale pricing memory?", ["approval-17", "old-pricing-note"], {
    write_status: "retry",
    attempt_count: 2,
    replication_lag_ms: 1330,
    latency_ms: 112,
    tokens: 810,
    cost_usd: 0.0016,
    storage_bytes: 1776,
    expected_memory_id: "approval-17",
    answer_correct: true,
    confidence: 0.81,
    stale_result: "fail",
    correction_result: "pass",
    deletion_result: "pass",
    redacted_source: "issue:ROL-10 comment:[redacted]",
  }),
  fixture("fix-pair-2-h", "fix-pair-2", "hindsight", "Recall the corrected deploy region.", ["region-correction", "deploy-plan"], {
    write_status: "success",
    attempt_count: 1,
    replication_lag_ms: 510,
    latency_ms: 95,
    tokens: 690,
    cost_usd: 0.0012,
    storage_bytes: 1308,
    expected_memory_id: "region-correction",
    answer_correct: true,
    confidence: 0.84,
    stale_result: "pass",
    correction_result: "pass",
    deletion_result: "pass",
    redacted_source: "project:MultMemory resource:[redacted]",
  }),
  fixture("fix-pair-2-m", "fix-pair-2", "mem0", "Recall the corrected deploy region.", ["old-region", "region-correction"], {
    write_status: "success",
    attempt_count: 1,
    replication_lag_ms: 790,
    latency_ms: 139,
    tokens: 760,
    cost_usd: 0.0015,
    storage_bytes: 1620,
    expected_memory_id: "region-correction",
    answer_correct: "partial",
    confidence: 0.63,
    stale_result: "fail",
    correction_result: "pass",
    deletion_result: "pass",
    redacted_source: "project:MultMemory resource:[redacted]",
  }),
  fixture("fix-pair-3-h", "fix-pair-3", "hindsight", "Should native hidden CLI memory be enabled?", ["native-memory-disabled"], {
    write_status: "success",
    attempt_count: 1,
    replication_lag_ms: 380,
    latency_ms: 71,
    tokens: 540,
    cost_usd: 0.001,
    storage_bytes: 1016,
    expected_memory_id: "native-memory-disabled",
    answer_correct: true,
    confidence: 0.93,
    stale_result: "pass",
    correction_result: "pass",
    deletion_result: "pass",
    redacted_source: "workspace policy:[redacted]",
  }),
  fixture("fix-pair-3-m", "fix-pair-3", "mem0", "Should native hidden CLI memory be enabled?", [], {
    write_status: "backlog",
    attempt_count: 0,
    replication_lag_ms: 0,
    latency_ms: 0,
    tokens: 0,
    cost_usd: 0,
    storage_bytes: 0,
    expected_memory_id: "native-memory-disabled",
    answer_correct: false,
    confidence: 0.22,
    stale_result: "unknown",
    correction_result: "unknown",
    deletion_result: "fail",
    redacted_source: "workspace policy:[redacted]",
  }),
];

function fixture(
  id: string,
  correlationId: string,
  provider: MemoryProvider,
  query: string,
  resultIds: string[],
  provenance: Record<string, unknown>,
): MemoryRecallSample {
  return {
    id,
    workspace_id: "fixture-workspace",
    provider,
    read_mode: "dual",
    recall_correlation_id: correlationId,
    query,
    sampled_at: FIXTURE_TIME,
    results: resultIds.map((memory_id, rank) => ({ memory_id, rank: rank + 1 })),
    provenance,
  };
}

export function buildMemoryComparisonModel(samples: MemoryRecallSample[]): MemoryComparisonModel {
  const livePairs = pairSamples(samples, "live");
  const fixturePairs = pairSamples(DETERMINISTIC_MEMORY_FIXTURES, "fixture");
  const pairs = livePairs.length > 0 ? livePairs : fixturePairs;
  const unpairedLiveSamples = countUnpairedLiveSamples(samples);
  const providers = {
    hindsight: summarizeProvider("hindsight", pairs),
    mem0: summarizeProvider("mem0", pairs),
  };
  return {
    samples: pairs,
    source: livePairs.length > 0 ? "live" : "fixture",
    sampleSize: pairs.length,
    coverage: {
      pairedSamples: livePairs.length,
      liveSamples: samples.length,
      unpairedLiveSamples,
    },
    providers,
    overlapAverage: average(pairs.map((pair) => pair.overlap)),
  };
}

function pairSamples(
  samples: MemoryRecallSample[],
  source: "live" | "fixture",
): PairedMemorySample[] {
  const groups = new Map<string, Partial<Record<MemoryProvider, MemoryRecallSample>>>();
  for (const sample of samples) {
    const provider = normalizeProvider(sample.provider);
    if (!provider) continue;
    const key = sample.recall_correlation_id || sample.query;
    if (!key) continue;
    const group = groups.get(key) ?? {};
    group[provider] = sample;
    groups.set(key, group);
  }

  const pairs: PairedMemorySample[] = [];
  for (const [correlationId, group] of groups) {
    if (!group.hindsight || !group.mem0) continue;
    const hindsight = toObservation(group.hindsight, "hindsight");
    const mem0 = toObservation(group.mem0, "mem0");
    pairs.push({
      correlationId,
      query: group.hindsight.query || group.mem0.query,
      source,
      hindsight,
      mem0,
      overlap: jaccard(hindsight.resultIds, mem0.resultIds),
      rankDelta: rankDelta(hindsight.matchedRank, mem0.matchedRank),
    });
  }
  return pairs.sort((a, b) => a.correlationId.localeCompare(b.correlationId));
}

function countUnpairedLiveSamples(samples: MemoryRecallSample[]): number {
  const groups = new Map<string, Set<MemoryProvider>>();
  for (const sample of samples) {
    const provider = normalizeProvider(sample.provider);
    if (!provider) continue;
    const key = sample.recall_correlation_id || sample.query;
    if (!key) continue;
    const set = groups.get(key) ?? new Set<MemoryProvider>();
    set.add(provider);
    groups.set(key, set);
  }
  let total = 0;
  for (const set of groups.values()) {
    if (!PROVIDERS.every((provider) => set.has(provider))) total += set.size;
  }
  return total;
}

function toObservation(sample: MemoryRecallSample, provider: MemoryProvider): ProviderObservation {
  const provenance = sample.provenance ?? {};
  const resultIds = resultMemoryIds(sample.results);
  const expectedMemoryId = stringValue(provenance, ["expected_memory_id", "expectedMemoryId", "expected_id"]);
  const matchedRank = numberValue(provenance, ["matched_rank", "rank", "expected_rank"]) ??
    (expectedMemoryId ? rankFor(resultIds, expectedMemoryId) : null);
  return {
    provider,
    sampleId: sample.id,
    sampledAt: sample.sampled_at,
    query: sample.query,
    writeStatus: writeStatus(provenance),
    attemptCount: numberValue(provenance, ["attempt_count", "attempts", "delivery_attempts"]),
    replicationLagMs: numberValue(provenance, ["replication_lag_ms", "delivery_lag_ms", "lag_ms"]),
    latencyMs: numberValue(provenance, ["latency_ms", "recall_latency_ms", "read_latency_ms"]),
    tokenCount: numberValue(provenance, ["tokens", "token_count", "total_tokens"]),
    costUsd: numberValue(provenance, ["cost_usd", "cost", "usd"]),
    storageBytes: numberValue(provenance, ["storage_bytes", "bytes", "provider_storage_bytes"]),
    resultIds,
    expectedMemoryId,
    matchedRank,
    correctness: correctness(provenance),
    confidence: numberValue(provenance, ["confidence", "answer_confidence"]),
    stale: evidenceState(provenance, ["stale_result", "stale", "stale_check"]),
    correction: evidenceState(provenance, ["correction_result", "correction", "correction_check"]),
    deletion: evidenceState(provenance, ["deletion_result", "deletion", "delete_check"]),
    provenance,
  };
}

function summarizeProvider(provider: MemoryProvider, pairs: PairedMemorySample[]): ProviderSummary {
  const observations = pairs.map((pair) => pair[provider]);
  const correctnessKnown = observations.filter((obs) => obs.correctness !== "unknown").length;
  const correctnessCorrect = observations.filter((obs) => obs.correctness === "correct").length;
  return {
    provider,
    sampleSize: observations.length,
    writeSuccess: observations.filter((obs) => obs.writeStatus === "success").length,
    retryBacklog: observations.filter((obs) => obs.writeStatus === "retry" || obs.writeStatus === "backlog").length,
    missingDeliveries: observations.filter((obs) => obs.writeStatus === "missing" || obs.writeStatus === "failed").length,
    correctnessKnown,
    correctnessCorrect,
    correctnessRate: correctnessKnown > 0 ? correctnessCorrect / correctnessKnown : null,
    correctnessCi: correctnessKnown > 0 ? wilson(correctnessCorrect, correctnessKnown) : null,
    stalePass: observations.filter((obs) => obs.stale === "pass").length,
    correctionPass: observations.filter((obs) => obs.correction === "pass").length,
    deletionPass: observations.filter((obs) => obs.deletion === "pass").length,
    p50LatencyMs: percentile(observations.map((obs) => obs.latencyMs), 0.5),
    p95LatencyMs: percentile(observations.map((obs) => obs.latencyMs), 0.95),
    p50ReplicationLagMs: percentile(observations.map((obs) => obs.replicationLagMs), 0.5),
    p95ReplicationLagMs: percentile(observations.map((obs) => obs.replicationLagMs), 0.95),
    totalTokens: sum(observations.map((obs) => obs.tokenCount)),
    totalCostUsd: sum(observations.map((obs) => obs.costUsd)),
    storageBytes: sum(observations.map((obs) => obs.storageBytes)),
  };
}

function normalizeProvider(provider: string): MemoryProvider | null {
  const normalized = provider.trim().toLowerCase();
  return normalized === "hindsight" || normalized === "mem0" ? normalized : null;
}

function resultMemoryIds(results: unknown[]): string[] {
  const ids: string[] = [];
  for (const result of results) {
    if (typeof result === "string") {
      ids.push(result);
      continue;
    }
    if (!result || typeof result !== "object") continue;
    const row = result as Record<string, unknown>;
    const id = row.memory_id ?? row.memoryId ?? row.id ?? row.provider_memory_id;
    if (typeof id === "string" && id.trim()) ids.push(id.trim());
  }
  return ids;
}

function rankFor(ids: string[], expected: string): number | null {
  const index = ids.indexOf(expected);
  return index >= 0 ? index + 1 : null;
}

function rankDelta(a: number | null, b: number | null): number | null {
  if (a == null || b == null) return null;
  return b - a;
}

function jaccard(a: string[], b: string[]): number {
  const left = new Set(a);
  const right = new Set(b);
  if (left.size === 0 && right.size === 0) return 0;
  let intersection = 0;
  for (const id of left) {
    if (right.has(id)) intersection += 1;
  }
  return intersection / new Set([...left, ...right]).size;
}

function writeStatus(provenance: Record<string, unknown>): WriteStatus {
  const raw = stringValue(provenance, ["write_status", "delivery_status", "status"]);
  if (raw === "success" || raw === "retry" || raw === "backlog" || raw === "failed" || raw === "missing") {
    return raw;
  }
  return "unknown";
}

function correctness(provenance: Record<string, unknown>): CorrectnessState {
  const raw = provenance.answer_correct ?? provenance.correct ?? provenance.correctness;
  if (raw === true) return "correct";
  if (raw === false) return "incorrect";
  if (raw === "correct" || raw === "partial" || raw === "incorrect") return raw;
  return "unknown";
}

function evidenceState(provenance: Record<string, unknown>, keys: string[]): EvidenceState {
  const raw = keys.map((key) => provenance[key]).find((value) => value != null);
  if (raw === true || raw === "pass" || raw === "passed") return "pass";
  if (raw === false || raw === "fail" || raw === "failed") return "fail";
  return "unknown";
}

function stringValue(provenance: Record<string, unknown>, keys: string[]): string | null {
  for (const key of keys) {
    const value = provenance[key];
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return null;
}

function numberValue(provenance: Record<string, unknown>, keys: string[]): number | null {
  for (const key of keys) {
    const value = provenance[key];
    if (typeof value === "number" && Number.isFinite(value)) return value;
    if (typeof value === "string" && value.trim() !== "") {
      const parsed = Number(value);
      if (Number.isFinite(parsed)) return parsed;
    }
  }
  return null;
}

function percentile(values: Array<number | null>, p: number): number | null {
  const sorted = values.filter((value): value is number => value != null && Number.isFinite(value)).sort((a, b) => a - b);
  if (sorted.length === 0) return null;
  const index = Math.min(sorted.length - 1, Math.ceil(sorted.length * p) - 1);
  return sorted[index] ?? null;
}

function average(values: number[]): number | null {
  if (values.length === 0) return null;
  return values.reduce((acc, value) => acc + value, 0) / values.length;
}

function sum(values: Array<number | null>): number {
  return values.reduce<number>((acc, value) => acc + (value ?? 0), 0);
}

function wilson(successes: number, n: number): [number, number] {
  const z = 1.96;
  const phat = successes / n;
  const denom = 1 + (z * z) / n;
  const centre = phat + (z * z) / (2 * n);
  const margin = z * Math.sqrt((phat * (1 - phat) + (z * z) / (4 * n)) / n);
  return [Math.max(0, (centre - margin) / denom), Math.min(1, (centre + margin) / denom)];
}
