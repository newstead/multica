import type { MemoryConfigResponse, MemoryMem0BoardDelivery, MemoryMem0BoardResponse, MemoryRecallSample } from "@multica/core/types";

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

const OUTCOME_ORDER: Record<MemoryOutcomeRow["operation"], number> = {
  add: 0,
  search: 1,
  update: 2,
  delete: 3,
  history: 4,
  unknown: 5,
};

function deliveryOperation(delivery: MemoryMem0BoardDelivery): MemoryOutcomeRow["operation"] {
  switch (delivery.event_type) {
    case "retain":
      return "add";
    case "update":
      return "update";
    case "delete":
    case "invalidate":
      return "delete";
    default:
      return "unknown";
  }
}

function deliveryStatus(delivery: MemoryMem0BoardDelivery): MemoryAuditRow["status"] {
  if (delivery.status === "delivered") return "ok";
  if (delivery.status === "retry" || delivery.status === "terminal_failed" || delivery.error) {
    return "error";
  }
  return "unknown";
}

function dayLabel(date: string): string {
  const d = new Date(`${date}T00:00:00`);
  return `${d.getMonth() + 1}/${d.getDate()}`;
}

function avg(values: number[]): number | null {
  if (values.length === 0) return null;
  return values.reduce((sum, value) => sum + value, 0) / values.length;
}

function round(value: number | null): number | null {
  return value == null ? null : Math.round(value * 100) / 100;
}

function recallAuditRow(sample: MemoryRecallSample): MemoryAuditRow {
  return {
    id: sample.id,
    sampledAt: sample.sampled_at,
    query: sample.query,
    readMode: sample.read_mode,
    resultCount: sample.results.length,
    latencyMs: null,
    tokens: null,
    costUsd: null,
    status: "ok",
    correlationId: sample.recall_correlation_id,
    issueId: sample.issue_id ?? null,
    taskId: sample.task_id ?? null,
  };
}

function deliveryAuditRow(delivery: MemoryMem0BoardDelivery): MemoryAuditRow {
  return {
    id: delivery.id,
    sampledAt: delivery.last_attempt_at || delivery.updated_at || delivery.delivery_created_at,
    query: delivery.event_type,
    readMode: "write",
    resultCount: delivery.provider_memory_id ? 1 : 0,
    latencyMs: delivery.delivery_lag_ms > 0 ? round(delivery.delivery_lag_ms) : null,
    tokens: null,
    costUsd: null,
    status: deliveryStatus(delivery),
    correlationId: delivery.provider_memory_id ?? delivery.memory_event_id,
    issueId: delivery.issue_id ?? null,
    taskId: delivery.task_id ?? null,
  };
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

export function filterMem0Deliveries(
  deliveries: MemoryMem0BoardDelivery[],
  filters: { days: number; projectId: string | null; query: string },
): MemoryMem0BoardDelivery[] {
  const cutoff = Date.now() - filters.days * 24 * 60 * 60 * 1000;
  const query = filters.query.trim().toLowerCase();
  return deliveries
    .filter((delivery) => delivery.provider.toLowerCase() === "mem0")
    .filter((delivery) => !filters.projectId || delivery.project_id === filters.projectId)
    .filter((delivery) => {
      const sampledAt = delivery.last_attempt_at || delivery.updated_at || delivery.delivery_created_at;
      const ts = Date.parse(sampledAt);
      return !Number.isFinite(ts) || ts >= cutoff;
    })
    .filter((delivery) => {
      if (!query) return true;
      return (
        delivery.event_type.toLowerCase().includes(query) ||
        delivery.status.toLowerCase().includes(query) ||
        delivery.id.toLowerCase().includes(query) ||
        delivery.memory_event_id.toLowerCase().includes(query) ||
        (delivery.provider_memory_id?.toLowerCase().includes(query) ?? false) ||
        (delivery.error?.toLowerCase().includes(query) ?? false)
      );
    })
    .toSorted((a, b) => {
      const aTime = a.last_attempt_at || a.updated_at || a.delivery_created_at;
      const bTime = b.last_attempt_at || b.updated_at || b.delivery_created_at;
      return bTime.localeCompare(aTime);
    });
}

export function filterMem0Board(
  board: MemoryMem0BoardResponse | null | undefined,
  filters: { days: number; projectId: string | null; query: string },
): MemoryMem0BoardResponse {
  return {
    health: board?.health ?? null,
    health_error: board?.health_error,
    deliveries: filterMem0Deliveries(board?.deliveries ?? [], filters),
    recall_samples: filterMem0Samples(board?.recall_samples ?? [], filters),
  };
}

export function buildMem0BoardSummary(
  config: MemoryConfigResponse | null | undefined,
  board: MemoryMem0BoardResponse | null | undefined,
): Mem0BoardSummary {
  const samples = board?.recall_samples ?? [];
  const deliveries = board?.deliveries ?? [];
  const sortedSamples = samples.toSorted((a, b) => b.sampled_at.localeCompare(a.sampled_at));
  const sortedDeliveries = deliveries.toSorted((a, b) => {
    const aTime = a.last_attempt_at || a.updated_at || a.delivery_created_at;
    const bTime = b.last_attempt_at || b.updated_at || b.delivery_created_at;
    return bTime.localeCompare(aTime);
  });
  const configured = !!config?.enabled && (
    config.primary_provider?.toLowerCase() === "mem0" ||
    config.shadow_provider?.toLowerCase() === "mem0"
  );

  const auditRows: MemoryAuditRow[] = [
    ...sortedSamples.map(recallAuditRow),
    ...sortedDeliveries.map(deliveryAuditRow),
  ]
    .toSorted((a, b) => b.sampledAt.localeCompare(a.sampledAt))
    .slice(0, 12);

  const outcomes = new Map<MemoryOutcomeRow["operation"], MemoryOutcomeRow>();
  const bumpOutcome = (operation: MemoryOutcomeRow["operation"], ok: boolean) => {
    const row = outcomes.get(operation) ?? { operation, ok: 0, error: 0 };
    if (ok) row.ok += 1;
    else row.error += 1;
    outcomes.set(operation, row);
  };

  const byDate = new Map<string, { search: number; add: number; error: number; searchLatencies: number[]; addLatencies: number[]; storageBytes: number | null }>();
  const ensureBucket = (date: string) => {
    const bucket = byDate.get(date) ?? {
      search: 0,
      add: 0,
      error: 0,
      searchLatencies: [],
      addLatencies: [],
      storageBytes: null,
    };
    byDate.set(date, bucket);
    return bucket;
  };

  for (const sample of sortedSamples) {
    const date = sample.sampled_at.slice(0, 10) || "unknown";
    const bucket = ensureBucket(date);
    bucket.search += 1;
    bumpOutcome("search", true);
  }

  for (const delivery of sortedDeliveries) {
    const operation = deliveryOperation(delivery);
    const status = deliveryStatus(delivery);
    bumpOutcome(operation, status !== "error");
    const sampledAt = delivery.last_attempt_at || delivery.updated_at || delivery.delivery_created_at;
    const date = sampledAt.slice(0, 10) || "unknown";
    const bucket = ensureBucket(date);
    if (operation === "add") bucket.add += 1;
    if (status === "error") bucket.error += 1;
    if (operation === "add" && delivery.delivery_lag_ms > 0) {
      bucket.addLatencies.push(delivery.delivery_lag_ms);
    }
  }

  const addLatencies = sortedDeliveries
    .filter((delivery) => deliveryOperation(delivery) === "add" && delivery.delivery_lag_ms > 0)
    .map((delivery) => delivery.delivery_lag_ms);
  const errorCount = sortedDeliveries.filter((delivery) => deliveryStatus(delivery) === "error").length;
  const health = !configured
    ? "disabled"
    : board?.health?.ok
      ? "healthy"
      : board?.health || board?.health_error || errorCount > 0
        ? "degraded"
        : "unknown";

  return {
    configured,
    readMode: config?.read_mode ?? "primary",
    primaryProvider: config?.primary_provider ?? "",
    shadowProvider: config?.shadow_provider ?? null,
    health,
    sampleCount: sortedSamples.length + sortedDeliveries.length,
    searchCount: sortedSamples.length,
    addCount: sortedDeliveries.filter((delivery) => deliveryOperation(delivery) === "add" && deliveryStatus(delivery) === "ok").length,
    errorCount,
    avgSearchLatencyMs: null,
    avgAddLatencyMs: round(avg(addLatencies)),
    tokens: null,
    costUsd: null,
    memoryCount: null,
    entityCount: null,
    storageBytes: null,
    points: Array.from(byDate.entries())
      .toSorted(([a], [b]) => a.localeCompare(b))
      .map(([date, bucket]) => ({
        date,
        label: date === "unknown" ? "?" : dayLabel(date),
        searchCount: bucket.search,
        addCount: bucket.add,
        errorCount: bucket.error,
        avgSearchLatencyMs: 0,
        avgAddLatencyMs: round(avg(bucket.addLatencies)) ?? 0,
        storageBytes: bucket.storageBytes,
      })),
    outcomes: Array.from(outcomes.values()).toSorted((a, b) => {
      const countDelta = (b.ok + b.error) - (a.ok + a.error);
      if (countDelta !== 0) return countDelta;
      return OUTCOME_ORDER[a.operation] - OUTCOME_ORDER[b.operation];
    }),
    auditRows,
  };
}
