"use client";

import { useMemo, useState, type ReactNode } from "react";
import { BarChart3 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import {
  createColumnHelper,
  getCoreRowModel,
  useReactTable,
} from "@tanstack/react-table";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Pie,
  PieChart,
  XAxis,
  YAxis,
} from "recharts";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { DataTable } from "@multica/ui/components/ui/data-table";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@multica/ui/components/ui/chart";
import {
  CompactNumberFlow,
  CurrencyNumberFlow,
  NumberFlow,
} from "@multica/ui/components/ui/number-flow";
import { useWorkspaceId } from "@multica/core/hooks";
import type {
  Agent,
  DashboardAgentCode,
  DashboardAgentSessions,
  DashboardUsageByAgent,
} from "@multica/core/types";
import { agentListOptions } from "@multica/core/workspace/queries";
import { projectListOptions } from "@multica/core/projects/queries";
import {
  dashboardAgentCodeOptions,
  dashboardAgentSessionsOptions,
  dashboardUsageByAgentOptions,
  dashboardUsageDailyOptions,
} from "@multica/core/dashboard";
import { useCustomPricingStore } from "@multica/core/runtimes/custom-pricing-store";
import { useViewingTimezone } from "../../common/use-viewing-timezone";
import { ActorAvatar } from "../../common/actor-avatar";
import { PageHeader } from "../../layout/page-header";
import { KpiCard } from "../../runtimes/components/shared";
import { DailyTokensChart } from "../../runtimes/components/charts";
import { formatTokens } from "../../runtimes/utils";
import { useT } from "../../i18n";
import {
  ALL_PROJECTS,
  ProjectFilter,
  Segmented,
  TIME_RANGES,
  UsagePageTabs,
  type TimeRange,
} from "./usage-controls";
import {
  aggregateDailyTokens,
  computeDailyTotals,
  formatDuration,
} from "../utils";

const EMPTY_DAILY: import("@multica/core/types").DashboardUsageDaily[] = [];
const EMPTY_BY_AGENT: DashboardUsageByAgent[] = [];
const EMPTY_SESSIONS: DashboardAgentSessions[] = [];
const EMPTY_CODE: DashboardAgentCode[] = [];
const EMPTY_AGENTS: Agent[] = [];

const RANGE_OPTIONS = TIME_RANGES.map((r) => ({ label: r.label, value: r.days }));

const PIE_COLORS = [
  "var(--chart-1)",
  "var(--chart-2)",
  "var(--chart-3)",
  "var(--chart-4)",
  "var(--chart-5)",
  "var(--primary)",
  "var(--muted-foreground)",
  "var(--accent-foreground)",
  "var(--secondary-foreground)",
  "var(--foreground)",
  "var(--border)",
];

interface AgentLookup {
  agentMap: Map<string, Agent>;
  nameForAgent: (agentId: string) => string;
}

export function AgentsUsagePage() {
  const { t, i18n } = useT("usage");
  const wsId = useWorkspaceId();
  const viewTZ = useViewingTimezone();
  const locales = i18n.resolvedLanguage ?? i18n.language;
  const [days, setDays] = useState<TimeRange>(30);
  const [projectValue, setProjectValue] = useState<string>(ALL_PROJECTS);

  useCustomPricingStore((s) => s.pricings);

  const { data: projects = [] } = useQuery(projectListOptions(wsId));
  const agentsQuery = useQuery(agentListOptions(wsId));
  const agents = agentsQuery.data ?? EMPTY_AGENTS;

  const projectId = useMemo(() => {
    if (projectValue === ALL_PROJECTS) return null;
    return projects.some((p) => p.id === projectValue) ? projectValue : null;
  }, [projectValue, projects]);

  const dailyQuery = useQuery(
    dashboardUsageDailyOptions(wsId, days, projectId, viewTZ),
  );
  const byAgentQuery = useQuery(
    dashboardUsageByAgentOptions(wsId, days, projectId, viewTZ),
  );
  const sessionsQuery = useQuery(
    dashboardAgentSessionsOptions(wsId, days, projectId, viewTZ),
  );
  const codeQuery = useQuery(
    dashboardAgentCodeOptions(wsId, days, projectId, viewTZ),
  );

  const dailyUsage = dailyQuery.data ?? EMPTY_DAILY;
  const byAgentUsage = byAgentQuery.data ?? EMPTY_BY_AGENT;
  const sessions = sessionsQuery.data ?? EMPTY_SESSIONS;
  const codeRows = codeQuery.data ?? EMPTY_CODE;

  const isLoading =
    dailyQuery.isLoading ||
    byAgentQuery.isLoading ||
    sessionsQuery.isLoading ||
    codeQuery.isLoading;
  const hasNoData =
    !isLoading &&
    dailyUsage.length === 0 &&
    byAgentUsage.length === 0 &&
    sessions.length === 0 &&
    codeRows.length === 0;

  const lookup = useMemo<AgentLookup>(() => {
    const agentMap = new Map(agents.map((agent) => [agent.id, agent] as const));
    return {
      agentMap,
      nameForAgent: (agentId) =>
        agentMap.get(agentId)?.name ?? t(($) => $.agents.unknown_agent),
    };
  }, [agents, t]);

  const dailyTokens = useMemo(() => aggregateDailyTokens(dailyUsage), [dailyUsage]);
  const totals = useMemo(() => computeDailyTotals(dailyUsage), [dailyUsage]);
  const sessionTotals = useMemo(() => computeSessionTotals(sessions), [sessions]);
  const codeTotals = useMemo(() => computeCodeTotals(codeRows), [codeRows]);
  const cacheHitRatio = computeCacheHitRatio(totals.cacheRead, totals.input);
  const tokensPerLoc = codeTotals.locChanged > 0
    ? (totals.input + totals.output + totals.cacheRead) / codeTotals.locChanged
    : 0;
  const chartConfig = useMemo(
    () => ({
      modelMix: {
        tokens: {
          label: t(($) => $.agents.chart.tokens),
          color: "var(--chart-1)",
        },
      } satisfies ChartConfig,
      sessions: {
        queueP50: {
          label: t(($) => $.agents.chart.queue_p50),
          color: "var(--chart-3)",
        },
        queueP95: {
          label: t(($) => $.agents.chart.queue_p95),
          color: "var(--chart-2)",
        },
        runP50: {
          label: t(($) => $.agents.chart.run_p50),
          color: "var(--chart-4)",
        },
        runP95: {
          label: t(($) => $.agents.chart.run_p95),
          color: "var(--chart-1)",
        },
      } satisfies ChartConfig,
    }),
    [t],
  );

  const modelMix = useMemo(
    () =>
      buildModelMix(byAgentUsage, {
        other: t(($) => $.agents.model_mix.other),
        unknownProvider: t(($) => $.agents.model_mix.unknown_provider),
        unknownModel: t(($) => $.agents.model_mix.unknown_model),
      }),
    [byAgentUsage, t],
  );
  const sessionChartRows = useMemo(
    () => buildSessionChartRows(sessions, lookup.nameForAgent),
    [sessions, lookup],
  );
  const failures = useMemo(
    () =>
      buildFailureRows(
        sessions,
        lookup.nameForAgent,
        t(($) => $.agents.failures.unknown_reason),
      ),
    [sessions, lookup, t],
  );
  const codeTableRows = useMemo(
    () => buildCodeTableRows(codeRows, lookup),
    [codeRows, lookup],
  );
  const bottlenecks = useMemo(
    () => buildBottlenecks({
      sessions,
      codeRows,
      byAgentUsage,
      lookup,
      labels: {
        highQueue: t(($) => $.agents.bottlenecks.reason_high_queue),
        longRuntime: t(($) => $.agents.bottlenecks.reason_long_runtime),
        highFailure: t(($) => $.agents.bottlenecks.reason_high_failure),
        highTokensLoc: t(($) => $.agents.bottlenecks.reason_high_tokens_loc),
      },
    }),
    [sessions, codeRows, byAgentUsage, lookup, t],
  );

  return (
    <div className="flex h-full flex-col">
      <PageHeader className="h-auto min-h-12 flex-wrap justify-between gap-y-1.5 px-5 py-1.5 sm:py-0">
        <div className="flex min-w-0 items-center gap-2">
          <BarChart3 className="h-4 w-4 shrink-0 text-muted-foreground" />
          <h1 className="truncate text-sm font-medium">{t(($) => $.title)}</h1>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <UsagePageTabs value="agents" />
          <ProjectFilter
            projects={projects}
            value={projectValue}
            onChange={setProjectValue}
          />
          <Segmented
            label={t(($) => $.filter.period_label)}
            value={days}
            onChange={setDays}
            options={RANGE_OPTIONS}
          />
        </div>
      </PageHeader>

      <div className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-6xl space-y-5 p-6">
          <p className="text-xs text-muted-foreground">
            {t(($) => $.agents.subtitle)}
          </p>

          {isLoading ? (
            <AgentsSkeleton />
          ) : hasNoData ? (
            <AgentsEmpty />
          ) : (
            <>
              <div className="grid grid-cols-1 divide-y rounded-lg border bg-card sm:grid-cols-2 sm:divide-x sm:divide-y-0 xl:grid-cols-6">
                <KpiCard
                  label={t(($) => $.agents.kpi.completed_label, { days })}
                  value={<NumberFlow value={sessionTotals.completed} locales={locales} />}
                />
                <KpiCard
                  label={t(($) => $.agents.kpi.failed_label, { days })}
                  value={<NumberFlow value={sessionTotals.failed} locales={locales} />}
                  hint={t(($) => $.agents.kpi.failed_hint, {
                    rate: formatPercent(sessionTotals.failureRate),
                  })}
                />
                <KpiCard
                  label={t(($) => $.agents.kpi.cost_label, { days })}
                  value={<CurrencyNumberFlow value={totals.cost} locales={locales} />}
                />
                <KpiCard
                  label={t(($) => $.agents.kpi.cache_label, { days })}
                  value={
                    <NumberFlow
                      value={cacheHitRatio * 100}
                      suffix="%"
                      locales={locales}
                      format={{ maximumFractionDigits: 0 }}
                    />
                  }
                  hint={t(($) => $.agents.kpi.cache_hint, {
                    tokens: formatTokens(totals.cacheRead),
                  })}
                />
                <KpiCard
                  label={t(($) => $.agents.kpi.tokens_loc_label, { days })}
                  value={<CompactNumberFlow value={tokensPerLoc} locales={locales} />}
                />
                <KpiCard
                  label={t(($) => $.agents.kpi.loc_label, { days })}
                  value={<CompactNumberFlow value={codeTotals.locChanged} locales={locales} />}
                  hint={t(($) => $.agents.kpi.loc_hint, {
                    additions: formatSigned(codeTotals.additions),
                    deletions: formatSigned(-codeTotals.deletions),
                  })}
                />
              </div>

              <div className="grid gap-5 lg:grid-cols-[minmax(0,1.35fr)_minmax(320px,0.65fr)]">
                <section className="rounded-lg border bg-card p-4">
                  <SectionHeader
                    title={t(($) => $.agents.tokens.title)}
                    subtitle={t(($) => $.agents.tokens.subtitle)}
                  />
                  {dailyTokens.length > 0 ? (
                    <DailyTokensChart data={dailyTokens} />
                  ) : (
                    <SectionEmpty>{t(($) => $.daily.no_data)}</SectionEmpty>
                  )}
                </section>
                <section className="rounded-lg border bg-card p-4">
                  <SectionHeader
                    title={t(($) => $.agents.model_mix.title)}
                    subtitle={t(($) => $.agents.model_mix.subtitle)}
                  />
                  <ModelMixChart data={modelMix} config={chartConfig.modelMix} />
                </section>
              </div>

              <div className="grid gap-5 lg:grid-cols-[minmax(0,1.4fr)_minmax(300px,0.6fr)]">
                <section className="rounded-lg border bg-card p-4">
                  <SectionHeader
                    title={t(($) => $.agents.sessions.title)}
                    subtitle={t(($) => $.agents.sessions.subtitle)}
                  />
                  <SessionsChart
                    data={sessionChartRows}
                    config={chartConfig.sessions}
                    lessThanMinuteLabel={t(($) => $.duration.less_than_minute)}
                  />
                </section>
                <section className="rounded-lg border bg-card p-4">
                  <SectionHeader
                    title={t(($) => $.agents.failures.title)}
                    subtitle={t(($) => $.agents.failures.subtitle)}
                  />
                  <FailureList rows={failures} />
                </section>
              </div>

              <section className="rounded-lg border bg-card p-4">
                <SectionHeader
                  title={t(($) => $.agents.code.title)}
                  subtitle={t(($) => $.agents.code.subtitle)}
                />
                <CodeTable rows={codeTableRows} />
              </section>

              <section className="rounded-lg border bg-card p-4">
                <SectionHeader
                  title={t(($) => $.agents.bottlenecks.title)}
                  subtitle={t(($) => $.agents.bottlenecks.subtitle)}
                />
                <BottlenecksPanel rows={bottlenecks} />
              </section>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function SectionHeader({ title, subtitle }: { title: string; subtitle: string }) {
  return (
    <div className="mb-3 flex min-w-0 items-start justify-between gap-3">
      <div className="min-w-0">
        <h2 className="truncate text-sm font-medium">{title}</h2>
        <p className="mt-1 text-xs text-muted-foreground">{subtitle}</p>
      </div>
    </div>
  );
}

function SectionEmpty({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-48 items-center justify-center rounded-md border border-dashed text-xs text-muted-foreground">
      {children}
    </div>
  );
}

function ModelMixChart({
  data,
  config,
}: {
  data: ModelMixRow[];
  config: ChartConfig;
}) {
  const { t } = useT("usage");
  if (data.length === 0) return <SectionEmpty>{t(($) => $.agents.model_mix.no_data)}</SectionEmpty>;

  return (
    <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(160px,0.8fr)]">
      <ChartContainer config={config} className="aspect-square min-h-56 w-full">
        <PieChart>
          <ChartTooltip
            content={
              <ChartTooltipContent
                formatter={(value, name) =>
                  typeof value === "number"
                    ? `${formatTokens(value)} ${name}`
                    : `${value} ${name}`
                }
              />
            }
          />
          <Pie
            data={data}
            dataKey="tokens"
            nameKey="label"
            innerRadius="52%"
            outerRadius="78%"
            paddingAngle={1}
          >
            {data.map((entry, index) => (
              <Cell key={entry.label} fill={PIE_COLORS[index % PIE_COLORS.length]} />
            ))}
          </Pie>
        </PieChart>
      </ChartContainer>
      <div className="min-w-0 space-y-2 self-center">
        {data.map((row, index) => (
          <div key={row.label} className="flex min-w-0 items-center justify-between gap-3 text-xs">
            <div className="flex min-w-0 items-center gap-2">
              <span
                className="h-2.5 w-2.5 shrink-0 rounded-sm"
                style={{ backgroundColor: PIE_COLORS[index % PIE_COLORS.length] }}
              />
              <span className="truncate">{row.label}</span>
            </div>
            <span className="shrink-0 font-mono tabular-nums text-muted-foreground">
              {formatTokens(row.tokens)}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

function SessionsChart({
  data,
  config,
  lessThanMinuteLabel,
}: {
  data: SessionChartRow[];
  config: ChartConfig;
  lessThanMinuteLabel: string;
}) {
  const { t } = useT("usage");
  if (data.length === 0) return <SectionEmpty>{t(($) => $.agents.sessions.no_data)}</SectionEmpty>;

  return (
    <ChartContainer config={config} className="aspect-[3/1] min-h-64 w-full">
      <BarChart data={data} margin={{ left: 0, right: 0, top: 4, bottom: 0 }}>
        <CartesianGrid vertical={false} />
        <XAxis dataKey="agentName" tickLine={false} axisLine={false} tickMargin={8} interval="preserveStartEnd" />
        <YAxis
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          width={52}
          tickFormatter={(value: number) => formatDuration(value, lessThanMinuteLabel)}
        />
        <ChartTooltip
          content={
            <ChartTooltipContent
              formatter={(value, name) =>
                typeof value === "number"
                  ? `${formatDuration(value, lessThanMinuteLabel)} ${name}`
                  : `${value} ${name}`
              }
            />
          }
        />
        <Bar dataKey="queueP50" fill="var(--color-queueP50)" radius={[2, 2, 0, 0]} />
        <Bar dataKey="queueP95" fill="var(--color-queueP95)" radius={[2, 2, 0, 0]} />
        <Bar dataKey="runP50" fill="var(--color-runP50)" radius={[2, 2, 0, 0]} />
        <Bar dataKey="runP95" fill="var(--color-runP95)" radius={[2, 2, 0, 0]} />
      </BarChart>
    </ChartContainer>
  );
}

function FailureList({ rows }: { rows: FailureRow[] }) {
  const { t } = useT("usage");
  if (rows.length === 0) return <SectionEmpty>{t(($) => $.agents.failures.no_data)}</SectionEmpty>;

  return (
    <div className="space-y-2">
      {rows.slice(0, 8).map((row) => (
        <div key={`${row.agentId}:${row.reason}`} className="flex items-center justify-between gap-3 rounded-md border bg-background px-3 py-2 text-xs">
          <div className="min-w-0">
            <div className="truncate font-medium">{row.agentName}</div>
            <div className="truncate text-muted-foreground">{row.reason}</div>
          </div>
          <span className="shrink-0 font-mono tabular-nums">{row.count}</span>
        </div>
      ))}
    </div>
  );
}

const codeColumn = createColumnHelper<CodeTableRow>();

function CodeTable({ rows }: { rows: CodeTableRow[] }) {
  const { t } = useT("usage");
  const columns = useMemo(
    () => [
      codeColumn.accessor("agentName", {
        header: t(($) => $.agents.code.header_agent),
        size: 230,
        cell: ({ row }) => (
          <div className="flex min-w-0 items-center gap-2">
            <ActorAvatar actorType="agent" actorId={row.original.agentId} size="sm" />
            <span className="truncate">{row.original.agentName}</span>
          </div>
        ),
      }),
      codeColumn.accessor("taskDiff", {
        header: t(($) => $.agents.code.header_task_diff),
        size: 130,
        cell: ({ row }) => <DiffText additions={row.original.additions} deletions={row.original.deletions} />,
      }),
      codeColumn.accessor("filesChanged", {
        header: t(($) => $.agents.code.header_files),
        size: 90,
        cell: ({ getValue }) => <Mono>{getValue()}</Mono>,
      }),
      codeColumn.accessor("taskCount", {
        header: t(($) => $.agents.code.header_tasks),
        size: 90,
        cell: ({ getValue }) => <Mono>{getValue()}</Mono>,
      }),
      codeColumn.accessor("prDiff", {
        header: t(($) => $.agents.code.header_pr_diff),
        size: 130,
        cell: ({ row }) => <DiffText additions={row.original.prAdditions} deletions={row.original.prDeletions} />,
      }),
      codeColumn.accessor("prChangedFiles", {
        header: t(($) => $.agents.code.header_pr_files),
        size: 90,
        cell: ({ getValue }) => <Mono>{getValue()}</Mono>,
      }),
      codeColumn.accessor("pullRequestCount", {
        header: t(($) => $.agents.code.header_prs),
        size: 90,
        cell: ({ getValue }) => <Mono>{getValue()}</Mono>,
      }),
    ],
    [t],
  );
  const table = useReactTable({ data: rows, columns, getCoreRowModel: getCoreRowModel() });

  return (
    <DataTable
      table={table}
      emptyMessage={t(($) => $.agents.code.no_data)}
      className="max-h-[420px] rounded-md border"
    />
  );
}

function DiffText({ additions, deletions }: { additions: number; deletions: number }) {
  return (
    <span className="inline-flex gap-2 font-mono text-xs tabular-nums">
      <span className="text-emerald-600 dark:text-emerald-400">{formatSigned(additions)}</span>
      <span className="text-rose-600 dark:text-rose-400">{formatSigned(-deletions)}</span>
    </span>
  );
}

function Mono({ children }: { children: ReactNode }) {
  return <span className="font-mono text-xs tabular-nums">{children}</span>;
}

function BottlenecksPanel({ rows }: { rows: BottleneckRow[] }) {
  const { t } = useT("usage");
  if (rows.length === 0) return <SectionEmpty>{t(($) => $.agents.bottlenecks.no_data)}</SectionEmpty>;

  return (
    <div className="grid gap-3 md:grid-cols-2">
      {rows.map((row) => (
        <div key={row.agentId} className="rounded-lg border bg-background p-3">
          <div className="flex items-start justify-between gap-3">
            <div className="flex min-w-0 items-center gap-2">
              <ActorAvatar actorType="agent" actorId={row.agentId} size="sm" />
              <div className="min-w-0">
                <div className="truncate text-sm font-medium">{row.agentName}</div>
                <div className="text-xs text-muted-foreground">
                  {t(($) => $.agents.bottlenecks.score, { score: row.score.toFixed(2) })}
                </div>
              </div>
            </div>
            <div className="shrink-0 text-right text-xs text-muted-foreground">
              <div>{formatPercent(row.failureRate)}</div>
              <div>{t(($) => $.agents.bottlenecks.tokens_per_loc, { tokens: formatTokens(Math.round(row.tokensPerLoc)) })}</div>
            </div>
          </div>
          <div className="mt-3 flex flex-wrap gap-1.5">
            {row.reasons.map((reason) => (
              <span key={reason} className="rounded-md bg-muted px-2 py-1 text-xs text-muted-foreground">
                {reason}
              </span>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

function AgentsSkeleton() {
  return (
    <div className="space-y-5">
      <div className="grid grid-cols-1 gap-px overflow-hidden rounded-lg border bg-border sm:grid-cols-2 xl:grid-cols-6">
        {Array.from({ length: 6 }).map((_, i) => (
          <div key={i} className="bg-card p-4"><Skeleton className="h-12 w-full" /></div>
        ))}
      </div>
      <Skeleton className="h-72 w-full" />
      <Skeleton className="h-72 w-full" />
      <Skeleton className="h-96 w-full" />
    </div>
  );
}

function AgentsEmpty() {
  const { t } = useT("usage");
  return (
    <div className="rounded-lg border bg-card p-10 text-center">
      <h2 className="text-sm font-medium">{t(($) => $.agents.empty.title)}</h2>
      <p className="mx-auto mt-2 max-w-md text-sm text-muted-foreground">
        {t(($) => $.agents.empty.body)}
      </p>
    </div>
  );
}

interface ModelMixRow { label: string; tokens: number }
interface SessionChartRow { agentId: string; agentName: string; queueP50: number; queueP95: number; runP50: number; runP95: number }
interface FailureRow { agentId: string; agentName: string; reason: string; count: number }
interface CodeTableRow {
  agentId: string;
  agentName: string;
  additions: number;
  deletions: number;
  filesChanged: number;
  taskCount: number;
  taskDiff: number;
  prAdditions: number;
  prDeletions: number;
  prChangedFiles: number;
  pullRequestCount: number;
  prDiff: number;
}
interface BottleneckRow {
  agentId: string;
  agentName: string;
  score: number;
  reasons: string[];
  failureRate: number;
  tokensPerLoc: number;
}

function computeSessionTotals(rows: DashboardAgentSessions[]) {
  const totals = rows.reduce(
    (acc, row) => ({
      tasks: acc.tasks + row.task_count,
      completed: acc.completed + row.completed_count,
      failed: acc.failed + row.failed_count,
    }),
    { tasks: 0, completed: 0, failed: 0 },
  );
  return { ...totals, failureRate: totals.tasks > 0 ? totals.failed / totals.tasks : 0 };
}

function computeCodeTotals(rows: DashboardAgentCode[]) {
  return rows.reduce(
    (acc, row) => ({
      additions: acc.additions + row.additions,
      deletions: acc.deletions + row.deletions,
      locChanged: acc.locChanged + row.additions + row.deletions,
    }),
    { additions: 0, deletions: 0, locChanged: 0 },
  );
}

function computeCacheHitRatio(cacheRead: number, input: number) {
  const denom = cacheRead + input;
  return denom > 0 ? cacheRead / denom : 0;
}

function buildModelMix(
  rows: DashboardUsageByAgent[],
  labels: { other: string; unknownProvider: string; unknownModel: string },
): ModelMixRow[] {
  const grouped = new Map<string, number>();
  for (const row of rows) {
    const provider = row.provider || labels.unknownProvider;
    const model = row.model || labels.unknownModel;
    const key = `${provider}/${model}`;
    grouped.set(
      key,
      (grouped.get(key) ?? 0) +
        row.input_tokens +
        row.output_tokens +
        row.cache_read_tokens +
        row.cache_write_tokens,
    );
  }
  const sorted = Array.from(grouped.entries())
    .map(([label, tokens]) => ({ label, tokens }))
    .filter((row) => row.tokens > 0)
    .toSorted((a, b) => b.tokens - a.tokens);
  const top = sorted.slice(0, 10);
  const other = sorted.slice(10).reduce((sum, row) => sum + row.tokens, 0);
  return other > 0 ? [...top, { label: labels.other, tokens: other }] : top;
}

function buildSessionChartRows(rows: DashboardAgentSessions[], nameForAgent: (agentId: string) => string): SessionChartRow[] {
  return rows
    .map((row) => ({
      agentId: row.agent_id,
      agentName: nameForAgent(row.agent_id),
      queueP50: row.queue_wait_p50_seconds,
      queueP95: row.queue_wait_p95_seconds,
      runP50: row.run_duration_p50_seconds,
      runP95: row.run_duration_p95_seconds,
    }))
    .toSorted((a, b) => b.queueP95 + b.runP95 - (a.queueP95 + a.runP95))
    .slice(0, 12);
}

function buildFailureRows(
  rows: DashboardAgentSessions[],
  nameForAgent: (agentId: string) => string,
  unknownReasonLabel: string,
): FailureRow[] {
  return rows.flatMap((row) =>
    row.failure_reasons.map((reason) => ({
      agentId: row.agent_id,
      agentName: nameForAgent(row.agent_id),
      reason: reason.failure_reason || unknownReasonLabel,
      count: reason.count,
    })),
  ).toSorted((a, b) => b.count - a.count);
}

function buildCodeTableRows(rows: DashboardAgentCode[], lookup: AgentLookup): CodeTableRow[] {
  return rows.map((row) => ({
    agentId: row.agent_id,
    agentName: lookup.nameForAgent(row.agent_id),
    additions: row.additions,
    deletions: row.deletions,
    filesChanged: row.files_changed,
    taskCount: row.task_count,
    taskDiff: row.additions - row.deletions,
    prAdditions: row.pr_additions,
    prDeletions: row.pr_deletions,
    prChangedFiles: row.pr_changed_files,
    pullRequestCount: row.pull_request_count,
    prDiff: row.pr_additions - row.pr_deletions,
  })).toSorted((a, b) =>
    b.prAdditions + b.prDeletions - (a.prAdditions + a.prDeletions) ||
    b.additions + b.deletions - (a.additions + a.deletions),
  );
}

function buildBottlenecks({
  sessions,
  codeRows,
  byAgentUsage,
  lookup,
  labels,
}: {
  sessions: DashboardAgentSessions[];
  codeRows: DashboardAgentCode[];
  byAgentUsage: DashboardUsageByAgent[];
  lookup: AgentLookup;
  labels: { highQueue: string; longRuntime: string; highFailure: string; highTokensLoc: string };
}): BottleneckRow[] {
  const codeByAgent = new Map(codeRows.map((row) => [row.agent_id, row] as const));
  const tokensByAgent = new Map<string, number>();
  for (const row of byAgentUsage) {
    tokensByAgent.set(
      row.agent_id,
      (tokensByAgent.get(row.agent_id) ?? 0) +
        row.input_tokens +
        row.output_tokens +
        row.cache_read_tokens,
    );
  }

  return sessions.flatMap((row) => {
    const reasons: string[] = [];
    let score = 0;
    if (row.queue_wait_p95_seconds >= 300) {
      score += row.queue_wait_p95_seconds / 300;
      reasons.push(labels.highQueue);
    }
    if (row.run_duration_p95_seconds >= 900) {
      score += row.run_duration_p95_seconds / 900;
      reasons.push(labels.longRuntime);
    }
    const failureRate = row.task_count > 0 ? row.failed_count / row.task_count : 0;
    if (failureRate >= 0.2 && row.task_count >= 3) {
      score += failureRate * 5;
      reasons.push(labels.highFailure);
    }
    const code = codeByAgent.get(row.agent_id);
    const loc = (code?.additions ?? 0) + (code?.deletions ?? 0);
    const tokensPerLoc = loc > 0 ? (tokensByAgent.get(row.agent_id) ?? 0) / loc : 0;
    if (tokensPerLoc >= 20_000 && loc > 0) {
      score += Math.min(tokensPerLoc / 20_000, 5);
      reasons.push(labels.highTokensLoc);
    }
    if (reasons.length === 0) return [];
    return [{
      agentId: row.agent_id,
      agentName: lookup.nameForAgent(row.agent_id),
      score: Math.round(score * 100) / 100,
      reasons,
      failureRate,
      tokensPerLoc,
    }];
  }).toSorted((a, b) => b.score - a.score).slice(0, 20);
}

function formatPercent(value: number) {
  return `${Math.round(value * 100)}%`;
}

function formatSigned(value: number) {
  return value >= 0 ? `+${value.toLocaleString()}` : value.toLocaleString();
}
