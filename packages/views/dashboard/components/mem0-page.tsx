"use client";

import { useMemo, useState, type ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import { Activity, AlertTriangle, Database, Gauge, Search, ShieldCheck } from "lucide-react";
import { Bar, BarChart, CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts";
import { useQuery } from "@tanstack/react-query";
import { Badge } from "@multica/ui/components/ui/badge";
import { Input } from "@multica/ui/components/ui/input";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@multica/ui/components/ui/chart";
import { useWorkspaceId } from "@multica/core/hooks";
import { memoryConfigOptions, memoryMem0BoardOptions } from "@multica/core/memory";
import { useCurrentMember } from "@multica/core/permissions";
import { projectListOptions } from "@multica/core/projects/queries";
import { PageHeader } from "../../layout/page-header";
import { useT } from "../../i18n";
import {
  ALL_PROJECTS,
  DEFAULT_DAYS_BY_DIM,
  ProjectFilter,
  Segmented,
  UsagePageTabs,
  rangesForDim,
  type Dim,
  type TimeRange,
} from "./usage-controls";
import { buildMem0BoardSummary, filterMem0Board } from "../memory/mem0-board-data";

const activityChartConfig = {
  searchCount: { label: "Search", color: "var(--chart-1)" },
  addCount: { label: "Add", color: "var(--chart-2)" },
  errorCount: { label: "Errors", color: "var(--destructive)" },
} satisfies ChartConfig;

const latencyChartConfig = {
  avgAddLatencyMs: { label: "Write delivery lag", color: "var(--chart-2)" },
} satisfies ChartConfig;

const storageChartConfig = {
  storageBytes: { label: "Storage", color: "var(--chart-3)" },
} satisfies ChartConfig;

export function Mem0UsagePage() {
  const { t, i18n } = useT("usage");
  const wsId = useWorkspaceId();
  const locales = i18n.resolvedLanguage ?? i18n.language;
  const [dim, setDim] = useState<Dim>("daily");
  const [days, setDays] = useState<TimeRange>(30);
  const [projectValue, setProjectValue] = useState<string>(ALL_PROJECTS);
  const [query, setQuery] = useState("");

  const { role, isLoading: roleLoading } = useCurrentMember(wsId);
  const canManageMemory = role === "owner" || role === "admin";
  const { data: projects = [] } = useQuery(projectListOptions(wsId));
  const configQuery = useQuery(memoryConfigOptions(wsId));

  const allowedRanges = rangesForDim(dim);
  const handleDimChange = (next: Dim) => {
    setDim(next);
    const stillAllowed = rangesForDim(next).some((range) => range.days === days);
    if (!stillAllowed) setDays(DEFAULT_DAYS_BY_DIM[next]);
  };

  const projectId = useMemo(() => {
    if (projectValue === ALL_PROJECTS) return null;
    return projects.some((project) => project.id === projectValue) ? projectValue : null;
  }, [projectValue, projects]);
  const boardQuery = useQuery(memoryMem0BoardOptions(wsId, { limit: 500, project_id: projectId }));

  const filteredBoard = useMemo(
    () =>
      filterMem0Board(boardQuery.data, {
        days,
        projectId,
        query,
      }),
    [boardQuery.data, days, projectId, query],
  );

  const summary = useMemo(
    () => buildMem0BoardSummary(configQuery.data, filteredBoard),
    [configQuery.data, filteredBoard],
  );

  const isLoading = configQuery.isLoading || boardQuery.isLoading || roleLoading;
  const hasError = configQuery.isError || boardQuery.isError;
  const points = dim === "weekly" ? weeklyPoints(summary.points) : summary.points;
  const storagePoints = points.filter((point) => point.storageBytes != null);

  return (
    <div className="flex min-w-0 flex-1 flex-col overflow-auto">
      <PageHeader className="h-auto min-h-12 flex-wrap justify-between gap-y-1.5 px-5 py-1.5 sm:py-0">
        <div className="flex min-w-0 items-center gap-2">
          <Database className="h-4 w-4 shrink-0 text-muted-foreground" />
          <h1 className="truncate text-body font-medium">{t(($) => $.memory.mem0.title)}</h1>
        </div>
        <UsagePageTabs value="mem0" />
      </PageHeader>
      <div className="mx-auto flex w-full max-w-7xl flex-col gap-5 px-4 py-4 sm:px-6 lg:px-8">
        <p className="text-caption text-muted-foreground">{t(($) => $.memory.mem0.subtitle)}</p>
        <div className="flex flex-wrap items-center gap-2">
          <ProjectFilter projects={projects} value={projectValue} onChange={setProjectValue} />
          <Segmented
            value={dim}
            onChange={handleDimChange}
            label={t(($) => $.dim.label)}
            options={[
              { label: t(($) => $.dim.daily), value: "daily" },
              { label: t(($) => $.dim.weekly), value: "weekly" },
            ]}
          />
          <Segmented
            value={days}
            onChange={(next) => setDays(next as TimeRange)}
            label={t(($) => $.filter.period_label)}
            options={allowedRanges.map((range) => ({ label: range.label, value: range.days }))}
          />
          <div className="relative min-w-52 flex-1 sm:max-w-72">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder={t(($) => $.memory.mem0.search_placeholder)}
              className="h-8 pl-8 text-body"
            />
          </div>
        </div>

        {hasError ? (
          <Panel className="border-destructive/30 bg-destructive/5">
            <div className="flex items-center gap-2 text-body font-medium text-destructive">
              <AlertTriangle className="h-4 w-4" />
              {t(($) => $.memory.mem0.error_title)}
            </div>
            <p className="mt-1 text-body text-muted-foreground">
              {t(($) => $.memory.mem0.error_body)}
            </p>
          </Panel>
        ) : null}

        {isLoading ? <LoadingState /> : (
          <>
            <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
              <MetricCard
                icon={Gauge}
                label={t(($) => $.memory.mem0.kpi.search_latency)}
                value={formatMs(summary.avgSearchLatencyMs)}
                hint={t(($) => $.memory.mem0.kpi.searches, { count: summary.searchCount })}
              />
              <MetricCard
                icon={Activity}
                label={t(($) => $.memory.mem0.kpi.add_latency)}
                value={formatMs(summary.avgAddLatencyMs)}
                hint={t(($) => $.memory.mem0.kpi.adds, { count: summary.addCount })}
              />
              <MetricCard
                icon={Database}
                label={t(($) => $.memory.mem0.kpi.tokens_cost)}
                value={summary.tokens == null ? "-" : formatNumber(summary.tokens, locales)}
                hint={summary.costUsd == null ? t(($) => $.memory.mem0.unknown) : formatCurrency(summary.costUsd, locales)}
              />
              <MetricCard
                icon={ShieldCheck}
                label={t(($) => $.memory.mem0.kpi.health)}
                value={t(($) => $.memory.mem0.health[summary.health])}
                hint={t(($) => $.memory.mem0.kpi.errors, { count: summary.errorCount })}
              />
            </div>

            <div className="grid gap-3 lg:grid-cols-[minmax(0,1.5fr)_minmax(280px,0.7fr)]">
              <Panel>
                <SectionHeader
                  title={t(($) => $.memory.mem0.activity_title)}
                  subtitle={t(($) => $.memory.mem0.activity_subtitle)}
                />
                {points.length === 0 ? <EmptyPanel text={t(($) => $.memory.mem0.empty_window)} /> : <ActivityChart data={points} />}
              </Panel>
              <Panel>
                <SectionHeader
                  title={t(($) => $.memory.mem0.config_title)}
                  subtitle={canManageMemory ? t(($) => $.memory.mem0.config_admin) : t(($) => $.memory.mem0.config_readonly)}
                />
                <div className="mt-4 grid gap-2 text-body">
                  <ConfigRow label={t(($) => $.memory.mem0.config.enabled)} value={summary.configured ? t(($) => $.memory.mem0.yes) : t(($) => $.memory.mem0.no)} />
                  <ConfigRow label={t(($) => $.memory.mem0.config.primary)} value={summary.primaryProvider || "-"} />
                  <ConfigRow label={t(($) => $.memory.mem0.config.shadow)} value={summary.shadowProvider || "-"} />
                  <ConfigRow label={t(($) => $.memory.mem0.config.read_mode)} value={summary.readMode} />
                  <ConfigRow label={t(($) => $.memory.mem0.config.memories)} value={summary.memoryCount == null ? "-" : formatNumber(summary.memoryCount, locales)} />
                  <ConfigRow label={t(($) => $.memory.mem0.config.entities)} value={summary.entityCount == null ? "-" : formatNumber(summary.entityCount, locales)} />
                </div>
              </Panel>
            </div>

            <div className="grid gap-3 lg:grid-cols-2">
              <Panel>
                <SectionHeader
                  title={t(($) => $.memory.mem0.latency_title)}
                  subtitle={t(($) => $.memory.mem0.latency_subtitle)}
                />
                {points.length === 0 ? <EmptyPanel text={t(($) => $.memory.mem0.empty_window)} /> : <LatencyChart data={points} />}
              </Panel>
              <Panel>
                <SectionHeader
                  title={t(($) => $.memory.mem0.storage_title)}
                  subtitle={t(($) => $.memory.mem0.storage_subtitle, { size: formatBytes(summary.storageBytes) })}
                />
                {storagePoints.length === 0 ? <EmptyPanel text={t(($) => $.memory.mem0.no_storage)} /> : <StorageChart data={storagePoints} />}
              </Panel>
            </div>

            <div className="grid gap-3 lg:grid-cols-[360px_minmax(0,1fr)]">
              <Panel>
                <SectionHeader
                  title={t(($) => $.memory.mem0.outcomes_title)}
                  subtitle={t(($) => $.memory.mem0.outcomes_subtitle)}
                />
                <div className="mt-4 divide-y">
                  {summary.outcomes.length === 0 ? (
                    <EmptyPanel text={t(($) => $.memory.mem0.no_outcomes)} />
                  ) : summary.outcomes.map((row) => (
                    <div key={row.operation} className="flex items-center justify-between gap-3 py-2 text-body">
                      <span className="capitalize">{t(($) => $.memory.mem0.operation[row.operation])}</span>
                      <span className="tabular-nums text-muted-foreground">
                        {t(($) => $.memory.mem0.outcome_counts, { ok: row.ok, error: row.error })}
                      </span>
                    </div>
                  ))}
                </div>
              </Panel>
              <Panel>
                <SectionHeader
                  title={t(($) => $.memory.mem0.audit_title)}
                  subtitle={t(($) => $.memory.mem0.audit_subtitle)}
                />
                <AuditTable rows={summary.auditRows} locales={locales} />
              </Panel>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function MetricCard({
  icon: Icon,
  label,
  value,
  hint,
}: {
  icon: LucideIcon;
  label: string;
  value: string;
  hint: string;
}) {
  return (
    <Panel className="p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="text-micro font-medium uppercase tracking-wider text-muted-foreground">{label}</div>
          <div className="mt-2 truncate text-display-sm font-semibold tabular-nums">{value}</div>
          <div className="mt-1 truncate text-caption text-muted-foreground">{hint}</div>
        </div>
        <div className="rounded-md bg-muted p-2 text-muted-foreground">
          <Icon className="h-4 w-4" />
        </div>
      </div>
    </Panel>
  );
}

function Panel({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <section className={`rounded-lg border bg-card p-4 ${className}`}>{children}</section>;
}

function SectionHeader({ title, subtitle }: { title: string; subtitle: string }) {
  return (
    <div>
      <div className="text-body font-semibold">{title}</div>
      <div className="mt-1 text-caption text-muted-foreground">{subtitle}</div>
    </div>
  );
}

function EmptyPanel({ text }: { text: string }) {
  return <div className="mt-4 rounded-md border border-dashed py-8 text-center text-body text-muted-foreground">{text}</div>;
}

function ConfigRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="text-muted-foreground">{label}</span>
      <span className="truncate font-medium">{value}</span>
    </div>
  );
}

function ActivityChart({ data }: { data: ReturnType<typeof weeklyPoints> }) {
  return (
    <ChartContainer config={activityChartConfig} className="mt-4 aspect-[3/1] min-h-64 w-full">
      <BarChart data={data}>
        <CartesianGrid vertical={false} />
        <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} />
        <YAxis tickLine={false} axisLine={false} width={32} />
        <ChartTooltip content={<ChartTooltipContent />} />
        <Bar dataKey="searchCount" stackId="a" fill="var(--color-searchCount)" radius={[3, 3, 0, 0]} />
        <Bar dataKey="addCount" stackId="a" fill="var(--color-addCount)" radius={[3, 3, 0, 0]} />
        <Bar dataKey="errorCount" fill="var(--color-errorCount)" radius={3} />
      </BarChart>
    </ChartContainer>
  );
}

function LatencyChart({ data }: { data: ReturnType<typeof weeklyPoints> }) {
  return (
    <ChartContainer config={latencyChartConfig} className="mt-4 aspect-[3/1] min-h-64 w-full">
      <LineChart data={data}>
        <CartesianGrid vertical={false} />
        <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} />
        <YAxis tickLine={false} axisLine={false} width={44} tickFormatter={(value) => `${value}ms`} />
        <ChartTooltip content={<ChartTooltipContent />} />
        <Line type="monotone" dataKey="avgAddLatencyMs" stroke="var(--color-avgAddLatencyMs)" strokeWidth={2} dot={false} />
      </LineChart>
    </ChartContainer>
  );
}

function StorageChart({ data }: { data: ReturnType<typeof weeklyPoints> }) {
  return (
    <ChartContainer config={storageChartConfig} className="mt-4 aspect-[3/1] min-h-64 w-full">
      <LineChart data={data}>
        <CartesianGrid vertical={false} />
        <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} />
        <YAxis tickLine={false} axisLine={false} width={52} tickFormatter={(value) => formatBytes(Number(value))} />
        <ChartTooltip content={<ChartTooltipContent />} />
        <Line type="monotone" dataKey="storageBytes" stroke="var(--color-storageBytes)" strokeWidth={2} dot={false} />
      </LineChart>
    </ChartContainer>
  );
}

function AuditTable({ rows, locales }: { rows: ReturnType<typeof buildMem0BoardSummary>["auditRows"]; locales?: Intl.LocalesArgument }) {
  const { t } = useT("usage");
  if (rows.length === 0) return <EmptyPanel text={t(($) => $.memory.mem0.no_audit)} />;
  return (
    <div className="mt-4 overflow-x-auto">
      <table className="w-full min-w-[760px] text-left text-body">
        <thead className="border-b text-caption text-muted-foreground">
          <tr>
            <th className="py-2 pr-4 font-medium">{t(($) => $.memory.mem0.audit.time)}</th>
            <th className="py-2 pr-4 font-medium">{t(($) => $.memory.mem0.audit.query)}</th>
            <th className="py-2 pr-4 font-medium">{t(($) => $.memory.mem0.audit.results)}</th>
            <th className="py-2 pr-4 font-medium">{t(($) => $.memory.mem0.audit.latency)}</th>
            <th className="py-2 pr-4 font-medium">{t(($) => $.memory.mem0.audit.tokens)}</th>
            <th className="py-2 pr-4 font-medium">{t(($) => $.memory.mem0.audit.status)}</th>
            <th className="py-2 font-medium">{t(($) => $.memory.mem0.audit.correlation)}</th>
          </tr>
        </thead>
        <tbody className="divide-y">
          {rows.map((row) => (
            <tr key={row.id}>
              <td className="py-2 pr-4 text-caption text-muted-foreground">{formatDateTime(row.sampledAt, locales)}</td>
              <td className="max-w-64 py-2 pr-4"><span className="block truncate">{row.query || "-"}</span></td>
              <td className="py-2 pr-4 tabular-nums">{row.resultCount}</td>
              <td className="py-2 pr-4 tabular-nums">{formatMs(row.latencyMs)}</td>
              <td className="py-2 pr-4 tabular-nums">{row.tokens == null ? "-" : formatNumber(row.tokens, locales)}</td>
              <td className="py-2 pr-4"><StatusBadge status={row.status} /></td>
              <td className="max-w-44 py-2 font-mono text-caption text-muted-foreground"><span className="block truncate">{row.correlationId || row.id}</span></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function StatusBadge({ status }: { status: "ok" | "error" | "unknown" }) {
  const { t } = useT("usage");
  const className = status === "ok"
    ? "bg-success/10 text-success"
    : status === "error"
      ? "bg-destructive/10 text-destructive"
      : "bg-muted text-muted-foreground";
  return <Badge variant="secondary" className={className}>{t(($) => $.memory.mem0.status[status])}</Badge>;
}

function LoadingState() {
  return (
    <div className="grid gap-3">
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        {Array.from({ length: 4 }, (_, index) => <Skeleton key={index} className="h-32 rounded-lg" />)}
      </div>
      <Skeleton className="h-80 rounded-lg" />
      <div className="grid gap-3 lg:grid-cols-2">
        <Skeleton className="h-80 rounded-lg" />
        <Skeleton className="h-80 rounded-lg" />
      </div>
    </div>
  );
}

function weeklyPoints(points: ReturnType<typeof buildMem0BoardSummary>["points"]) {
  if (points.length === 0) return points;
  const buckets = new Map<string, { label: string; searchCount: number; addCount: number; errorCount: number; searchLatencyTotal: number; searchLatencyCount: number; addLatencyTotal: number; addLatencyCount: number; storageBytes: number | null }>();
  for (const point of points) {
    const date = new Date(`${point.date}T00:00:00`);
    const start = new Date(date);
    start.setDate(date.getDate() - date.getDay());
    const key = start.toISOString().slice(0, 10);
    const bucket = buckets.get(key) ?? { label: dayLabel(key), searchCount: 0, addCount: 0, errorCount: 0, searchLatencyTotal: 0, searchLatencyCount: 0, addLatencyTotal: 0, addLatencyCount: 0, storageBytes: null };
    bucket.searchCount += point.searchCount;
    bucket.addCount += point.addCount;
    bucket.errorCount += point.errorCount;
    if (point.avgSearchLatencyMs > 0) {
      bucket.searchLatencyTotal += point.avgSearchLatencyMs;
      bucket.searchLatencyCount += 1;
    }
    if (point.avgAddLatencyMs > 0) {
      bucket.addLatencyTotal += point.avgAddLatencyMs;
      bucket.addLatencyCount += 1;
    }
    if (point.storageBytes != null) bucket.storageBytes = point.storageBytes;
    buckets.set(key, bucket);
  }
  return Array.from(buckets.entries()).toSorted(([a], [b]) => a.localeCompare(b)).map(([date, bucket]) => ({
    date,
    label: bucket.label,
    searchCount: bucket.searchCount,
    addCount: bucket.addCount,
    errorCount: bucket.errorCount,
    avgSearchLatencyMs: bucket.searchLatencyCount > 0 ? Math.round(bucket.searchLatencyTotal / bucket.searchLatencyCount) : 0,
    avgAddLatencyMs: bucket.addLatencyCount > 0 ? Math.round(bucket.addLatencyTotal / bucket.addLatencyCount) : 0,
    storageBytes: bucket.storageBytes,
  }));
}

function dayLabel(date: string): string {
  const d = new Date(`${date}T00:00:00`);
  return `${d.getMonth() + 1}/${d.getDate()}`;
}

function formatNumber(value: number, locales?: Intl.LocalesArgument): string {
  return new Intl.NumberFormat(locales, { maximumFractionDigits: 0 }).format(value);
}

function formatCurrency(value: number, locales?: Intl.LocalesArgument): string {
  return new Intl.NumberFormat(locales, { style: "currency", currency: "USD", maximumFractionDigits: 4 }).format(value);
}

function formatMs(value: number | null): string {
  if (value == null) return "-";
  if (value >= 1000) return `${(value / 1000).toFixed(1)}s`;
  return `${Math.round(value)}ms`;
}

function formatBytes(value: number | null): string {
  if (value == null) return "-";
  const units = ["B", "KB", "MB", "GB"] as const;
  let n = value;
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i += 1;
  }
  return `${n >= 10 ? Math.round(n) : Math.round(n * 10) / 10}${units[i]}`;
}

function formatDateTime(value: string, locales?: Intl.LocalesArgument): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value || "-";
  return new Intl.DateTimeFormat(locales, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(date);
}
