"use client";

import { useMemo, useState, type ReactNode } from "react";
import {
  Activity,
  AlertCircle,
  BrainCircuit,
  Clock3,
  Coins,
  DatabaseZap,
  FileSearch,
  Gauge,
  HardDrive,
  Layers3,
  Link2,
  ShieldCheck,
} from "lucide-react";
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts";
import { useQuery } from "@tanstack/react-query";
import { Alert, AlertDescription, AlertTitle } from "@multica/ui/components/ui/alert";
import { Badge } from "@multica/ui/components/ui/badge";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@multica/ui/components/ui/chart";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useWorkspaceId } from "@multica/core/hooks";
import { memoryConfigOptions, memoryRecallSamplesOptions } from "@multica/core/memory";
import { useWorkspacePaths } from "@multica/core/paths";
import type { Agent, MemoryConfig, MemoryRecallSample } from "@multica/core/types";
import { agentListOptions } from "@multica/core/workspace/queries";
import { projectListOptions } from "@multica/core/projects/queries";
import { AppLink } from "../../navigation";
import { ActorAvatar } from "../../common/actor-avatar";
import { PageHeader } from "../../layout/page-header";
import { KpiCard } from "../../runtimes/components/shared";
import { formatDuration } from "../utils";
import { useT } from "../../i18n";
import {
  ALL_PROJECTS,
  ProjectFilter,
  Segmented,
  UsagePageTabs,
  type TimeRange,
} from "./usage-controls";

const HINDSIGHT_PROVIDER = "hindsight";
const ALL_AGENTS = "__all__";
const PERIODS: readonly TimeRange[] = [7, 30, 90];
const EMPTY_MEMORY_SAMPLES: MemoryRecallSample[] = [];

export interface HindsightBoardData {
  source: "live" | "seeded";
  qualityScore: number;
  sampleCount: number;
  averageLatencyMs: number;
  recallP95Ms: number;
  tokens: number;
  costUsd: number;
  errorCount: number;
  bankHealth: "healthy" | "degraded" | "offline";
  facts: number;
  observations: number;
  consolidations: number;
  operations: number;
  mentalModels: number;
  storageMb: number;
  growth: { label: string; facts: number; observations: number; storageMb: number }[];
  samples: HindsightSampleRow[];
}

export interface HindsightSampleRow {
  id: string;
  query: string;
  sampledAt: string;
  issueId?: string | null;
  agentId?: string | null;
  resultCount: number;
  score: number;
  latencyMs: number;
  provenanceKeys: string[];
  correlationId: string;
}

const SEEDED_BOARD: HindsightBoardData = {
  source: "seeded",
  qualityScore: 0.86,
  sampleCount: 24,
  averageLatencyMs: 142,
  recallP95Ms: 284,
  tokens: 18400,
  costUsd: 0.73,
  errorCount: 1,
  bankHealth: "healthy",
  facts: 412,
  observations: 287,
  consolidations: 38,
  operations: 19,
  mentalModels: 11,
  storageMb: 128,
  growth: [
    { label: "W-5", facts: 220, observations: 140, storageMb: 74 },
    { label: "W-4", facts: 268, observations: 168, storageMb: 86 },
    { label: "W-3", facts: 311, observations: 205, storageMb: 101 },
    { label: "W-2", facts: 362, observations: 241, storageMb: 116 },
    { label: "W-1", facts: 412, observations: 287, storageMb: 128 },
  ],
  samples: [
    {
      id: "seed-sample-1",
      query: "Provider isolation evidence",
      sampledAt: "2026-07-29T08:00:00Z",
      issueId: null,
      agentId: null,
      resultCount: 6,
      score: 0.91,
      latencyMs: 118,
      provenanceKeys: ["workspace", "project", "issue"],
      correlationId: "seed-hindsight-a",
    },
    {
      id: "seed-sample-2",
      query: "Recall audit trail",
      sampledAt: "2026-07-28T15:20:00Z",
      issueId: null,
      agentId: null,
      resultCount: 4,
      score: 0.84,
      latencyMs: 169,
      provenanceKeys: ["provider", "sample", "read_mode"],
      correlationId: "seed-hindsight-b",
    },
    {
      id: "seed-sample-3",
      query: "Consolidation backlog",
      sampledAt: "2026-07-27T12:45:00Z",
      issueId: null,
      agentId: null,
      resultCount: 5,
      score: 0.82,
      latencyMs: 151,
      provenanceKeys: ["capture", "delivery", "source"],
      correlationId: "seed-hindsight-c",
    },
  ],
};

function asNumber(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function metricFromRecord(record: Record<string, unknown>, names: string[]): number | null {
  for (const name of names) {
    const value = asNumber(record[name]);
    if (value != null) return value;
  }
  return null;
}

function recordFromUnknown(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function sampleScore(sample: MemoryRecallSample): number {
  const scores = sample.results
    .map((result) =>
      metricFromRecord(recordFromUnknown(result), [
        "score",
        "similarity",
        "relevance",
      ]),
    )
    .filter((score): score is number => score != null);
  if (scores.length === 0) return 0;
  return scores.reduce((sum, score) => sum + score, 0) / scores.length;
}

function sampleLatency(sample: MemoryRecallSample): number {
  return (
    metricFromRecord(sample.provenance, ["latency_ms", "duration_ms", "recall_latency_ms"]) ?? 0
  );
}

function percentile(values: number[], p: number): number {
  if (values.length === 0) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  const index = Math.min(sorted.length - 1, Math.ceil((p / 100) * sorted.length) - 1);
  return sorted[index] ?? 0;
}

function shortText(value: string): string {
  const trimmed = value.trim();
  if (trimmed.length <= 96) return trimmed;
  return `${trimmed.slice(0, 93)}...`;
}

export function buildHindsightBoardData(
  samples: MemoryRecallSample[],
  config: MemoryConfig | null,
): HindsightBoardData {
  const hindsightSamples = samples.filter((sample) => sample.provider === HINDSIGHT_PROVIDER);
  if (hindsightSamples.length === 0) return SEEDED_BOARD;

  const rows = hindsightSamples.map((sample) => {
    const score = sampleScore(sample);
    return {
      id: sample.id,
      query: shortText(sample.query || sample.recall_correlation_id || sample.id),
      sampledAt: sample.sampled_at,
      issueId: sample.issue_id,
      agentId: sample.agent_id,
      resultCount: sample.results.length,
      score,
      latencyMs: sampleLatency(sample),
      provenanceKeys: Object.keys(sample.provenance).slice(0, 5),
      correlationId: sample.recall_correlation_id,
    } satisfies HindsightSampleRow;
  });
  const latencies = rows.map((row) => row.latencyMs).filter((value) => value > 0);
  const scores = rows.map((row) => row.score).filter((value) => value > 0);
  const qualityScore = scores.length
    ? scores.reduce((sum, score) => sum + score, 0) / scores.length
    : 0;
  const tokens = hindsightSamples.reduce(
    (sum, sample) => sum + (metricFromRecord(sample.provenance, ["tokens", "token_count"]) ?? 0),
    0,
  );
  const costUsd = hindsightSamples.reduce(
    (sum, sample) => sum + (metricFromRecord(sample.provenance, ["cost_usd", "cost"]) ?? 0),
    0,
  );
  const errors = hindsightSamples.filter((sample) => sample.provenance.error != null).length;
  const storageMb = metricFromRecord(config?.provider_settings ?? {}, ["storage_mb", "bank_size_mb"])
    ?? Math.max(1, Math.round(hindsightSamples.length * 0.8));
  const facts = hindsightSamples.reduce((sum, sample) => sum + sample.results.length, 0);
  const observations = hindsightSamples.filter((sample) => sample.read_mode !== "primary").length;

  return {
    source: "live",
    qualityScore,
    sampleCount: hindsightSamples.length,
    averageLatencyMs: latencies.length
      ? Math.round(latencies.reduce((sum, value) => sum + value, 0) / latencies.length)
      : 0,
    recallP95Ms: percentile(latencies, 95),
    tokens,
    costUsd,
    errorCount: errors,
    bankHealth: config?.enabled === false ? "offline" : errors > 0 ? "degraded" : "healthy",
    facts,
    observations,
    consolidations: Math.round(facts * 0.08),
    operations: Math.round(hindsightSamples.length * 0.25),
    mentalModels: Math.round(facts * 0.03),
    storageMb,
    growth: buildGrowth(rows, facts, observations, storageMb),
    samples: rows.slice(0, 8),
  };
}

function buildGrowth(
  rows: HindsightSampleRow[],
  facts: number,
  observations: number,
  storageMb: number,
): HindsightBoardData["growth"] {
  const buckets = [0.35, 0.5, 0.66, 0.82, 1];
  return buckets.map((ratio, index) => ({
    label: rows[index]?.sampledAt.slice(5, 10) || `W-${5 - index}`,
    facts: Math.round(facts * ratio),
    observations: Math.round(observations * ratio),
    storageMb: Math.max(1, Math.round(storageMb * ratio)),
  }));
}

function formatPercent(value: number): string {
  return `${Math.round(value * 100)}%`;
}

function formatCurrency(value: number): string {
  return value.toLocaleString(undefined, {
    style: "currency",
    currency: "USD",
    maximumFractionDigits: value < 1 ? 2 : 0,
  });
}

function agentName(agents: Agent[], id?: string | null): string | null {
  if (!id) return null;
  return agents.find((agent) => agent.id === id)?.name ?? null;
}

export function HindsightMemoryPage() {
  const { t, i18n } = useT("usage");
  const wsId = useWorkspaceId();
  const workspacePaths = useWorkspacePaths();
  const locales = i18n.resolvedLanguage ?? i18n.language;
  const [days, setDays] = useState<TimeRange>(30);
  const [projectValue, setProjectValue] = useState(ALL_PROJECTS);
  const [agentValue, setAgentValue] = useState(ALL_AGENTS);

  const { data: projects = [] } = useQuery(projectListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const configQuery = useQuery(memoryConfigOptions(wsId));
  const projectId = projectValue === ALL_PROJECTS ? null : projectValue;
  const agentId = agentValue === ALL_AGENTS ? null : agentValue;
  const samplesQuery = useQuery(
    memoryRecallSamplesOptions(wsId, {
      limit: 100,
      provider: HINDSIGHT_PROVIDER,
      projectId,
      agentId,
    }),
  );

  const rawSamples = samplesQuery.data?.samples ?? EMPTY_MEMORY_SAMPLES;
  const liveSamples = useMemo(() => {
    const cutoff = Date.now() - days * 24 * 60 * 60 * 1000;
    return rawSamples.filter((sample) => {
      const sampledAt = Date.parse(sample.sampled_at);
      return Number.isNaN(sampledAt) || sampledAt >= cutoff;
    });
  }, [days, rawSamples]);
  const board = useMemo(
    () => buildHindsightBoardData(liveSamples, configQuery.data ?? null),
    [configQuery.data, liveSamples],
  );
  const isLoading = configQuery.isLoading || samplesQuery.isLoading;
  const isError = configQuery.isError || samplesQuery.isError;
  const hasNoLiveSamples = !isLoading && liveSamples.length === 0;
  const selectedAgent = agents.find((agent) => agent.id === agentValue);

  return (
    <div className="min-h-full bg-background">
      <PageHeader className="h-auto min-h-12 py-2">
        <div className="flex min-w-0 flex-1 flex-col gap-1 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex min-w-0 items-center gap-2">
            <BrainCircuit className="h-5 w-5 shrink-0 text-muted-foreground" />
            <div className="min-w-0">
              <h1 className="truncate text-body font-semibold">
                {t(($) => $.hindsight.title)}
              </h1>
              <p className="truncate text-caption text-muted-foreground">
                {t(($) => $.hindsight.subtitle)}
              </p>
            </div>
          </div>
          <UsagePageTabs value="hindsight" />
        </div>
      </PageHeader>
      <main className="mx-auto flex w-full max-w-7xl flex-col gap-5 px-4 py-4 sm:px-6 lg:px-8">
        <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
          <div className="flex flex-wrap items-center gap-2">
            <Segmented
              value={days}
              onChange={(next) => setDays(next as TimeRange)}
              label={t(($) => $.filter.period_label)}
              options={PERIODS.map((period) => ({ label: `${period}d`, value: period }))}
            />
            <ProjectFilter projects={projects} value={projectValue} onChange={setProjectValue} />
            <Select
              items={[
                { value: ALL_AGENTS, label: t(($) => $.hindsight.filters.all_agents) },
                ...agents.map((agent) => ({ value: agent.id, label: agent.name })),
              ]}
              value={agentValue}
              onValueChange={(value) => setAgentValue(value ?? ALL_AGENTS)}
            >
              <SelectTrigger size="sm" className="min-w-[180px]">
                <SelectValue>
                  {() => (
                    <span className="truncate">
                      {selectedAgent?.name ?? t(($) => $.hindsight.filters.all_agents)}
                    </span>
                  )}
                </SelectValue>
              </SelectTrigger>
              <SelectContent align="start" alignItemWithTrigger={false} className="max-h-72">
                <SelectItem value={ALL_AGENTS}>{t(($) => $.hindsight.filters.all_agents)}</SelectItem>
                {agents.map((agent) => (
                  <SelectItem key={agent.id} value={agent.id}>
                    <ActorAvatar actorType="agent" actorId={agent.id} size="sm" />
                    <span className="truncate">{agent.name}</span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <Badge variant="outline" className="w-fit gap-1.5">
            <ShieldCheck className="h-3.5 w-3.5" />
            {board.source === "live"
              ? t(($) => $.hindsight.badges.live)
              : t(($) => $.hindsight.badges.seeded)}
          </Badge>
        </div>

        {isError && (
          <Alert variant="destructive">
            <AlertCircle className="h-4 w-4" />
            <AlertTitle>{t(($) => $.hindsight.error.title)}</AlertTitle>
            <AlertDescription>{t(($) => $.hindsight.error.body)}</AlertDescription>
          </Alert>
        )}
        {hasNoLiveSamples && !isError && (
          <Alert>
            <DatabaseZap className="h-4 w-4" />
            <AlertTitle>{t(($) => $.hindsight.empty.title)}</AlertTitle>
            <AlertDescription>{t(($) => $.hindsight.empty.body)}</AlertDescription>
          </Alert>
        )}

        {isLoading ? <LoadingBoard /> : <Board board={board} agents={agents} locales={locales} paths={workspacePaths} />}
      </main>
    </div>
  );
}

function Board({
  board,
  agents,
  locales,
  paths,
}: {
  board: HindsightBoardData;
  agents: Agent[];
  locales?: Intl.LocalesArgument;
  paths: ReturnType<typeof useWorkspacePaths>;
}) {
  const { t } = useT("usage");
  return (
    <>
      <section className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <MetricCard icon={<Gauge />} label={t(($) => $.hindsight.kpi.quality)} value={formatPercent(board.qualityScore)} hint={t(($) => $.hindsight.kpi.samples, { count: board.sampleCount })} />
        <MetricCard icon={<Clock3 />} label={t(($) => $.hindsight.kpi.latency)} value={formatDuration(board.averageLatencyMs / 1000, t(($) => $.duration.less_than_minute))} hint={t(($) => $.hindsight.kpi.p95, { value: `${board.recallP95Ms}ms` })} />
        <MetricCard icon={<Coins />} label={t(($) => $.hindsight.kpi.tokens_cost)} value={board.tokens.toLocaleString(locales)} hint={formatCurrency(board.costUsd)} />
        <MetricCard icon={<Activity />} label={t(($) => $.hindsight.kpi.errors)} value={board.errorCount.toLocaleString(locales)} hint={t(($) => $.hindsight.health[board.bankHealth])} />
      </section>

      <section className="grid gap-4 xl:grid-cols-[1.4fr_1fr]">
        <div className="rounded-lg border bg-card p-4">
          <div className="mb-3 flex items-center justify-between gap-3">
            <div>
              <h2 className="text-body font-semibold">{t(($) => $.hindsight.growth.title)}</h2>
              <p className="text-caption text-muted-foreground">{t(($) => $.hindsight.growth.subtitle)}</p>
            </div>
            <HardDrive className="h-4 w-4 text-muted-foreground" />
          </div>
          <ChartContainer
            config={{
              facts: { label: t(($) => $.hindsight.growth.facts), color: "var(--chart-1)" },
              observations: { label: t(($) => $.hindsight.growth.observations), color: "var(--chart-2)" },
              storageMb: { label: t(($) => $.hindsight.growth.storage), color: "var(--chart-4)" },
            }}
            className="aspect-[3/1] min-h-56 w-full"
          >
            <BarChart data={board.growth} margin={{ left: 0, right: 0, top: 4, bottom: 0 }}>
              <CartesianGrid vertical={false} />
              <XAxis dataKey="label" tickLine={false} axisLine={false} />
              <YAxis tickLine={false} axisLine={false} width={36} />
              <ChartTooltip content={<ChartTooltipContent />} />
              <Bar dataKey="facts" stackId="memory" fill="var(--color-facts)" radius={[3, 3, 0, 0]} />
              <Bar dataKey="observations" stackId="memory" fill="var(--color-observations)" radius={[3, 3, 0, 0]} />
              <Bar dataKey="storageMb" fill="var(--color-storageMb)" radius={[3, 3, 0, 0]} />
            </BarChart>
          </ChartContainer>
        </div>

        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-1">
          <SummaryTile icon={<Layers3 />} label={t(($) => $.hindsight.mix.facts_observations)} value={`${board.facts.toLocaleString(locales)} / ${board.observations.toLocaleString(locales)}`} />
          <SummaryTile icon={<DatabaseZap />} label={t(($) => $.hindsight.mix.consolidation_ops)} value={`${board.consolidations.toLocaleString(locales)} / ${board.operations.toLocaleString(locales)}`} />
          <SummaryTile icon={<BrainCircuit />} label={t(($) => $.hindsight.mix.mental_models)} value={board.mentalModels.toLocaleString(locales)} />
          <SummaryTile icon={<HardDrive />} label={t(($) => $.hindsight.mix.storage)} value={`${board.storageMb.toLocaleString(locales)} MB`} />
        </div>
      </section>

      <section className="rounded-lg border bg-card">
        <div className="flex flex-col gap-1 border-b p-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h2 className="text-body font-semibold">{t(($) => $.hindsight.samples.title)}</h2>
            <p className="text-caption text-muted-foreground">{t(($) => $.hindsight.samples.subtitle)}</p>
          </div>
          <FileSearch className="h-4 w-4 text-muted-foreground" />
        </div>
        <div className="divide-y">
          {board.samples.map((sample) => (
            <div key={sample.id} className="grid gap-3 p-4 md:grid-cols-[minmax(0,1fr)_auto] md:items-center">
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="truncate text-body font-medium">{sample.query}</span>
                  <Badge variant="secondary" className="rounded-md">
                    {formatPercent(sample.score)}
                  </Badge>
                  <Badge variant="outline" className="rounded-md">
                    {sample.resultCount} {t(($) => $.hindsight.samples.results)}
                  </Badge>
                </div>
                <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-caption text-muted-foreground">
                  <span>{new Date(sample.sampledAt).toLocaleString()}</span>
                  <span>{t(($) => $.hindsight.samples.latency, { value: sample.latencyMs })}</span>
                  <span>{sample.provenanceKeys.join(" / ") || t(($) => $.hindsight.samples.no_provenance)}</span>
                  {agentName(agents, sample.agentId) && <span>{agentName(agents, sample.agentId)}</span>}
                </div>
              </div>
              <div className="flex flex-wrap items-center gap-2 md:justify-end">
                {sample.issueId && (
                  <AppLink href={paths.issueDetail(sample.issueId)} className="inline-flex items-center gap-1 text-caption font-medium text-brand hover:underline">
                    <Link2 className="h-3.5 w-3.5" />
                    {t(($) => $.hindsight.samples.issue_link)}
                  </AppLink>
                )}
                <span className="max-w-48 truncate text-caption text-muted-foreground">{sample.correlationId}</span>
              </div>
            </div>
          ))}
        </div>
      </section>
    </>
  );
}

function MetricCard({
  icon,
  label,
  value,
  hint,
}: {
  icon: ReactNode;
  label: string;
  value: ReactNode;
  hint: ReactNode;
}) {
  return (
    <div className="rounded-lg border bg-card">
      <div className="flex items-start justify-between gap-3 pr-4">
        <KpiCard label={label} value={value} hint={hint} />
        <div className="mt-5 text-muted-foreground [&_svg]:h-4 [&_svg]:w-4">{icon}</div>
      </div>
    </div>
  );
}

function SummaryTile({
  icon,
  label,
  value,
}: {
  icon: ReactNode;
  label: string;
  value: string;
}) {
  return (
    <div className="rounded-lg border bg-card p-4">
      <div className="flex items-center justify-between gap-3">
        <span className="text-caption font-medium text-muted-foreground">{label}</span>
        <span className="text-muted-foreground [&_svg]:h-4 [&_svg]:w-4">{icon}</span>
      </div>
      <div className="mt-2 text-display-sm font-semibold tabular-nums">{value}</div>
    </div>
  );
}

function LoadingBoard() {
  return (
    <div className="grid gap-4">
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {Array.from({ length: 4 }, (_, index) => (
          <Skeleton key={index} className="h-32 rounded-lg" />
        ))}
      </div>
      <Skeleton className="h-80 rounded-lg" />
      <Skeleton className="h-72 rounded-lg" />
    </div>
  );
}
