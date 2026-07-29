import type { MemoryConfigResponse, MemoryRecallSample } from "@multica/core/types";

export interface MemoryMetricPoint {
  date: string;
  label: string;
  searchCount: number;
  addCount: number;
  errorCount: number;
  avgSearchLatencyMs: number;
  avgAddLatencyMs: number;
  storageBytes: number | null;
}

export interface MemoryOutcomeRow {
  operation: "add" | "search" | "update" | "delete" | "history" | "unknown";
  ok: number;
  error: number;
}

export interface MemoryAuditRow {
  id: string;
  sampledAt: string;
  query: string;
  readMode: string;
  resultCount: number;
  latencyMs: number | null;
  tokens: number | null;
  costUsd: number | null;
  status: "ok" | "error" | "unknown";
  correlationId: string;
  issueId: string | null;
  taskId: string | null;
}

export interface Mem0BoardSummary {
  configured: boolean;
  readMode: string;
  primaryProvider: string;
  shadowProvider: string | null;
  health: "healthy" | "degraded" | "unknown" | "disabled";
  sampleCount: number;
  searchCount: number;
  addCount: number;
  errorCount: number;
  avgSearchLatencyMs: number | null;
  avgAddLatencyMs: number | null;
  tokens: number | null;
  costUsd: number | null;
  memoryCount: number | null;
  entityCount: number | null;
  storageBytes: number | null;
  points: MemoryMetricPoint[];
  outcomes: MemoryOutcomeRow[];
  auditRows: MemoryAuditRow[];
}

const OPERATION_KEYS = ["operation", "op", "event_type", "action", "type"] as const;
const STATUS_KEYS = ["status", "outcome", "result_status"] as const;

function dayLabel(date: string): string {
  const d = new Date(`${date}T00:00:00`);
  return `${d.getMonth() + 1}/${d.getDate()}`;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function walk(value: unknown): unknown[] {
  if (Array.isArray(value)) return value.flatMap(walk);
  if (!isRecord(value)) return [value];
  return [value, ...Object.values(value).flatMap(walk)];
}

function firstNumber(...values: unknown[]): number | null {
  for (const value of values.flatMap(walk)) {
    if (typeof value === "number" && Number.isFinite(value)) return value;
    if (typeof value === "string" && value.trim() !== "") {
      const parsed = Number(value);
      if (Number.isFinite(parsed)) return parsed;
    }
  }
  return null;
}

function valueForKeys(sample: MemoryRecallSample, keys: readonly string[]): unknown[] {
  const values: unknown[] = [];
  const visit = (value: unknown) => {
    if (Array.isArray(value)) {
      value.forEach(visit);
      return;
    }
    if (!isRecord(value)) return;
    for (const key of keys) {
      if (key in value) values.push(value[key]);
    }
    Object.values(value).forEach(visit);
  };
  visit(sample.provenance);
  visit(sample.results);
  return values;
}

function firstTextForKeys(sample: MemoryRecallSample, keys: readonly string[]): string | null {
  for (const value of valueForKeys(sample, keys).flatMap(walk)) {
    if (typeof value === "string" && value.trim() !== "") return value.trim();
  }
  return null;
}

function num(sample: MemoryRecallSample, keys: readonly string[]): number | null {
  return firstNumber(...valueForKeys(sample, keys));
}

function operationOf(sample: MemoryRecallSample): MemoryOutcomeRow["operation"] {
  const raw = firstTextForKeys(sample, OPERATION_KEYS)?.toLowerCase() ?? "";
  if (raw.includes("delete") || raw.includes("invalidate")) return "delete";
  if (raw.includes("update")) return "update";
  if (raw.includes("history")) return "history";
  if (raw.includes("add") || raw.includes("retain") || raw.includes("create")) return "add";
  if (raw.includes("search") || raw.includes("recall") || sample.query) return "search";
  return "unknown";
}

function statusOf(sample: MemoryRecallSample): MemoryAuditRow["status"] {
  const raw = firstTextForKeys(sample, STATUS_KEYS)?.toLowerCase() ?? "";
  const errorText = firstTextForKeys(sample, ["error", "errors", "error_message"]);
  if (raw.includes("error") || raw.includes("fail") || errorText) return "error";
  if (raw.includes("ok") || raw.includes("success") || raw.includes("delivered")) return "ok";
  return sample.results.length > 0 ? "ok" : "unknown";
}

function avg(values: number[]): number | null {
  if (values.length === 0) return null;
  return values.reduce((sum, value) => sum + value, 0) / values.length;
}

function round(value: number | null): number | null {
  return value == null ? null : Math.round(value * 100) / 100;
}

function roundCost(value: number | null): number | null {
  return value == null ? null : Math.round(value * 10000) / 10000;
}

function latestNumber(samples: MemoryRecallSample[], keys: readonly string[]): number | null {
  for (const sample of samples) {
    const value = num(sample, keys);
    if (value != null) return value;
  }
  return null;
}

export function filterMem0Samples(
  samples: MemoryRecallSample[],
  filters: { days: number; projectId: string | null; query: string },
): MemoryRecallSample[] {
  const cutoff = Date.now() - filters.days * 24 * 60 * 60 * 1000;
  const query = filters.query.trim().toLowerCase();
  return samples
    .filter((sample) => sample.provider.toLowerCase() === "mem0")
    .filter((sample) => !filters.projectId || sample.project_id === filters.projectId)
    .filter((sample) => {
      const ts = Date.parse(sample.sampled_at);
      return !Number.isFinite(ts) || ts >= cutoff;
    })
    .filter((sample) => {
      if (!query) return true;
      return (
        sample.query.toLowerCase().includes(query) ||
        sample.recall_correlation_id.toLowerCase().includes(query) ||
        sample.id.toLowerCase().includes(query)
      );
    })
    .toSorted((a, b) => b.sampled_at.localeCompare(a.sampled_at));
}

export function buildMem0BoardSummary(
  config: MemoryConfigResponse | null | undefined,
  samples: MemoryRecallSample[],
): Mem0BoardSummary {
  const sorted = samples.toSorted((a, b) => b.sampled_at.localeCompare(a.sampled_at));
  const configured = !!config?.enabled && (
    config.primary_provider?.toLowerCase() === "mem0" ||
    config.shadow_provider?.toLowerCase() === "mem0"
  );
  const auditRows: MemoryAuditRow[] = sorted.slice(0, 12).map((sample) => {
    const latencyMs = num(sample, ["latency_ms", "search_latency_ms", "duration_ms", "elapsed_ms"]);
    return {
      id: sample.id,
      sampledAt: sample.sampled_at,
      query: sample.query,
      readMode: sample.read_mode,
      resultCount: sample.results.length,
      latencyMs: round(latencyMs),
      tokens: round(num(sample, ["tokens", "total_tokens", "token_count"])),
      costUsd: roundCost(num(sample, ["cost_usd", "usd", "cost"])),
      status: statusOf(sample),
      correlationId: sample.recall_correlation_id,
      issueId: sample.issue_id ?? null,
      taskId: sample.task_id ?? null,
    };
  });

  const outcomes = new Map<MemoryOutcomeRow["operation"], MemoryOutcomeRow>();
  const bumpOutcome = (operation: MemoryOutcomeRow["operation"], ok: boolean) => {
    const row = outcomes.get(operation) ?? { operation, ok: 0, error: 0 };
    if (ok) row.ok += 1;
    else row.error += 1;
    outcomes.set(operation, row);
  };

  const byDate = new Map<string, { search: number; add: number; error: number; searchLatencies: number[]; addLatencies: number[]; storageBytes: number | null }>();
  for (const sample of sorted) {
    const operation = operationOf(sample);
    const status = statusOf(sample);
    bumpOutcome(operation, status !== "error");

    const date = sample.sampled_at.slice(0, 10) || "unknown";
    const bucket = byDate.get(date) ?? {
      search: 0,
      add: 0,
      error: 0,
      searchLatencies: [],
      addLatencies: [],
      storageBytes: null,
    };
    if (operation === "add") bucket.add += 1;
    if (operation === "search") bucket.search += 1;
    if (status === "error") bucket.error += 1;
    const searchLatency = num(sample, ["search_latency_ms", "latency_ms", "duration_ms", "elapsed_ms"]);
    const addLatency = num(sample, ["add_latency_ms", "retain_latency_ms", "write_latency_ms"]);
    if (searchLatency != null) bucket.searchLatencies.push(searchLatency);
    if (addLatency != null) bucket.addLatencies.push(addLatency);
    const storage = num(sample, ["storage_bytes", "db_bytes", "disk_bytes", "size_bytes"]);
    if (storage != null) bucket.storageBytes = storage;
    byDate.set(date, bucket);
  }

  const searchLatencies = sorted
    .map((sample) => num(sample, ["search_latency_ms", "latency_ms", "duration_ms", "elapsed_ms"]))
    .filter((value): value is number => value != null);
  const addLatencies = sorted
    .map((sample) => num(sample, ["add_latency_ms", "retain_latency_ms", "write_latency_ms"]))
    .filter((value): value is number => value != null);
  const errorCount = sorted.filter((sample) => statusOf(sample) === "error").length;
  const tokens = sorted
    .map((sample) => num(sample, ["tokens", "total_tokens", "token_count"]))
    .filter((value): value is number => value != null)
    .reduce((sum, value) => sum + value, 0);
  const costUsd = sorted
    .map((sample) => num(sample, ["cost_usd", "usd", "cost"]))
    .filter((value): value is number => value != null)
    .reduce((sum, value) => sum + value, 0);
  const health = !configured
    ? "disabled"
    : errorCount > 0
      ? "degraded"
      : sorted.length > 0
        ? "healthy"
        : "unknown";

  return {
    configured,
    readMode: config?.read_mode ?? "primary",
    primaryProvider: config?.primary_provider ?? "",
    shadowProvider: config?.shadow_provider ?? null,
    health,
    sampleCount: sorted.length,
    searchCount: outcomes.get("search")?.ok ?? 0,
    addCount: outcomes.get("add")?.ok ?? 0,
    errorCount,
    avgSearchLatencyMs: round(avg(searchLatencies)),
    avgAddLatencyMs: round(avg(addLatencies)),
    tokens: tokens > 0 ? round(tokens) : null,
    costUsd: costUsd > 0 ? roundCost(costUsd) : null,
    memoryCount: round(latestNumber(sorted, ["memory_count", "memories_count", "total_memories"])),
    entityCount: round(latestNumber(sorted, ["entity_count", "entities_count", "total_entities"])),
    storageBytes: round(latestNumber(sorted, ["storage_bytes", "db_bytes", "disk_bytes", "size_bytes"])),
    points: Array.from(byDate.entries())
      .toSorted(([a], [b]) => a.localeCompare(b))
      .map(([date, bucket]) => ({
        date,
        label: date === "unknown" ? "?" : dayLabel(date),
        searchCount: bucket.search,
        addCount: bucket.add,
        errorCount: bucket.error,
        avgSearchLatencyMs: round(avg(bucket.searchLatencies)) ?? 0,
        avgAddLatencyMs: round(avg(bucket.addLatencies)) ?? 0,
        storageBytes: bucket.storageBytes,
      })),
    outcomes: Array.from(outcomes.values()).toSorted((a, b) =>
      (b.ok + b.error) - (a.ok + a.error),
    ),
    auditRows,
  };
}
