"use client";

import { useMemo, useState, type ReactNode } from "react";
import { AlertTriangle, Database, GitCompareArrows, ShieldCheck } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts";
import { memoryConfigOptions, memoryRecallSamplesOptions, type MemoryRecallSample } from "@multica/core/memory";
import { MemoryAuditBoard } from "./memory-audit-board";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@multica/ui/components/ui/chart";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { PageHeader } from "../layout/page-header";
import { useT } from "../i18n";
import {
  buildMemoryComparisonModel,
  summarizeRedactedProvenance,
  type MemoryComparisonModel,
  type MemoryProvider,
  type PairedMemorySample,
  type ProviderObservation,
  type ProviderSummary,
} from "./analysis";

type MemoryTab = "comparison" | "audit" | MemoryProvider;

const TABS: MemoryTab[] = ["comparison", "hindsight", "mem0", "audit"];
const EMPTY_SAMPLES: MemoryRecallSample[] = [];


type MemoryT = (key: string, options?: Record<string, unknown>) => string;

function useMemoryT(): MemoryT {
  const { t } = useT("memory");
  return t as MemoryT;
}

export function MemoryPage() {
  const { t: rawT, i18n } = useT("memory");
  const t = rawT as MemoryT;
  const locale = i18n.resolvedLanguage ?? i18n.language;
  const wsId = useWorkspaceId();
  const [tab, setTab] = useState<MemoryTab>("comparison");

  const configQuery = useQuery(memoryConfigOptions(wsId));
  const samplesQuery = useQuery(memoryRecallSamplesOptions(wsId, { limit: 500 }));
  const samples = samplesQuery.data?.samples ?? EMPTY_SAMPLES;
  const model = useMemo(() => buildMemoryComparisonModel(samples), [samples]);
  const hasConfigError = configQuery.isError;
  const hasSamplesError = samplesQuery.isError;

  const isLoading = configQuery.isLoading || samplesQuery.isLoading;
  const config = configQuery.data;

  return (
    <div className="flex h-full min-h-0 flex-col bg-background text-foreground">
      <PageHeader>
        <div className="flex min-w-0 flex-1 items-center gap-2">
          <Database className="size-4 shrink-0 text-muted-foreground" />
          <h1 className="truncate text-body font-semibold">{t("page.title")}</h1>
          <span className="hidden rounded-sm bg-muted px-1.5 py-0.5 text-micro text-muted-foreground sm:inline">
            {hasConfigError ? t("status.unavailable") : config?.enabled ? t("status.enabled") : t("status.disabled")}
          </span>
        </div>
      </PageHeader>

      <main className="min-h-0 flex-1 overflow-auto">
        <div className="mx-auto flex w-full max-w-7xl flex-col gap-4 px-4 py-4">
          <section className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_20rem]">
            <div className="rounded-md border bg-card p-4">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0 space-y-1">
                  <p className="text-caption font-medium uppercase text-muted-foreground">{t("page.eyebrow")}</p>
                  <h2 className="text-title-lg font-semibold tracking-normal">{t("page.heading")}</h2>
                  <p className="max-w-3xl text-body text-muted-foreground">{t("page.description")}</p>
                </div>
                <div className="flex rounded-md border bg-muted/30 p-0.5" role="tablist" aria-label={t("tabs.label")}>
                  {TABS.map((key) => (
                    <button
                      key={key}
                      type="button"
                      role="tab"
                      aria-selected={tab === key}
                      className={cn(
                        "h-8 whitespace-nowrap rounded-sm px-3 text-caption font-medium text-muted-foreground transition-colors",
                        tab === key && "bg-background text-foreground shadow-sm",
                      )}
                      onClick={() => setTab(key)}
                    >
                      {t(`tabs.${key}`)}
                    </button>
                  ))}
                </div>
              </div>
            </div>

            <div className="rounded-md border bg-card p-4">
              {isLoading ? (
                <div className="space-y-2">
                  <Skeleton className="h-4 w-28" />
                  <Skeleton className="h-6 w-full" />
                  <Skeleton className="h-6 w-3/4" />
                </div>
              ) : hasConfigError ? (
                <UnavailableState title={t("errors.config_title")} description={t("errors.config_description")} />
              ) : (
                <div className="space-y-3 text-body">
                  <StatusLine label={t("config.primary")} value={config?.primary_provider || t("common.unknown")} />
                  <StatusLine label={t("config.shadow")} value={config?.shadow_provider || t("common.none")} />
                  <StatusLine label={t("config.read_mode")} value={config?.read_mode || t("common.unknown")} />
                </div>
              )}
            </div>
          </section>

          {hasSamplesError ? (
            <UnavailableState
              title={t("errors.samples_title")}
              description={t("errors.samples_description")}
              variant="section"
            />
          ) : (
            <>
              <CoverageBanner model={model} />
              {tab === "audit" ? (
                <MemoryAuditBoard />
              ) : tab === "comparison" ? (
                <ComparisonBoard model={model} locale={locale} />
              ) : (
                <ProviderBoard provider={tab} model={model} locale={locale} />
              )}
            </>
          )}
        </div>
      </main>
    </div>
  );
}

function StatusLine({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="text-muted-foreground">{label}</span>
      <span className="truncate font-medium">{value}</span>
    </div>
  );
}

function UnavailableState({
  title,
  description,
  variant = "inline",
}: {
  title: string;
  description: string;
  variant?: "inline" | "section";
}) {
  return (
    <div className={cn(
      "flex items-start gap-3 text-body",
      variant === "section" && "rounded-md border bg-card p-4",
    )}>
      <AlertTriangle className="mt-0.5 size-4 shrink-0 text-destructive" />
      <div className="min-w-0 space-y-1">
        <p className="font-medium">{title}</p>
        <p className="text-muted-foreground">{description}</p>
      </div>
    </div>
  );
}

function CoverageBanner({ model }: { model: MemoryComparisonModel }) {
  const t = useMemoryT();
  const liveCoverage = model.coverage.liveSamples > 0
    ? model.coverage.pairedSamples / Math.max(1, model.coverage.liveSamples / 2)
    : 0;
  return (
    <section className="rounded-md border bg-card p-4">
      <div className="flex items-start gap-3">
        {model.source === "fixture" ? (
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-amber-600" />
        ) : (
          <ShieldCheck className="mt-0.5 size-4 shrink-0 text-emerald-600" />
        )}
        <div className="min-w-0 flex-1 space-y-1">
          <p className="text-body font-medium">
            {model.source === "fixture" ? t("coverage.fixture_title") : t("coverage.live_title")}
          </p>
          <p className="text-body text-muted-foreground">
            {model.source === "fixture"
              ? t("coverage.fixture_body", { count: model.sampleSize })
              : t("coverage.live_body", {
                  paired: model.coverage.pairedSamples,
                  live: model.coverage.liveSamples,
                  unpaired: model.coverage.unpairedLiveSamples,
                  coverage: formatPercent(liveCoverage),
                })}
          </p>
        </div>
      </div>
    </section>
  );
}

function ComparisonBoard({ model, locale }: { model: MemoryComparisonModel; locale: string }) {
  return (
    <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_24rem]">
      <div className="space-y-4">
        <SummaryTiles model={model} locale={locale} />
        <WriteMatrix model={model} locale={locale} />
        <RecallMetrics model={model} locale={locale} />
        <RankMetrics model={model} />
        <LifecycleTable model={model} />
      </div>
      <div className="space-y-4">
        <ComparisonChart model={model} />
        <ProvenanceDrilldown samples={model.samples} />
      </div>
    </div>
  );
}

function SummaryTiles({ model, locale }: { model: MemoryComparisonModel; locale: string }) {
  const t = useMemoryT();
  return (
    <section className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      <StatTile label={t("summary.sample_size")} value={formatNumber(model.sampleSize, locale)} detail={t("summary.paired_only")} />
      <StatTile label={t("summary.overlap")} value={formatNullablePercent(model.overlapAverage)} detail={t("summary.mean_jaccard")} />
      <StatTile label={t("summary.hindsight_correctness")} value={formatNullablePercent(model.providers.hindsight.correctnessRate)} detail={formatCi(model.providers.hindsight.correctnessCi)} />
      <StatTile label={t("summary.mem0_correctness")} value={formatNullablePercent(model.providers.mem0.correctnessRate)} detail={formatCi(model.providers.mem0.correctnessCi)} />
    </section>
  );
}

function StatTile({ label, value, detail }: { label: string; value: string; detail: string }) {
  return (
    <div className="rounded-md border bg-card p-4">
      <p className="text-caption font-medium uppercase text-muted-foreground">{label}</p>
      <p className="mt-2 text-display-sm font-semibold tracking-normal">{value}</p>
      <p className="mt-1 text-caption text-muted-foreground">{detail}</p>
    </div>
  );
}

function WriteMatrix({ model, locale }: { model: MemoryComparisonModel; locale: string }) {
  const t = useMemoryT();
  return (
    <section className="rounded-md border bg-card p-4">
      <SectionHeader icon={<GitCompareArrows className="size-4" />} title={t("write.title")} description={t("write.description")} />
      <div className="mt-4 overflow-x-auto">
        <table className="w-full min-w-[680px] text-body">
          <thead className="border-b text-caption uppercase text-muted-foreground">
            <tr>
              <th className="py-2 text-left font-medium">{t("table.provider")}</th>
              <th className="py-2 text-right font-medium">{t("write.success")}</th>
              <th className="py-2 text-right font-medium">{t("write.retry_backlog")}</th>
              <th className="py-2 text-right font-medium">{t("write.missing")}</th>
              <th className="py-2 text-right font-medium">{t("write.p50_lag")}</th>
              <th className="py-2 text-right font-medium">{t("write.p95_lag")}</th>
            </tr>
          </thead>
          <tbody>
            {providerRows(model).map((summary) => (
              <tr key={summary.provider} className="border-b last:border-b-0">
                <td className="py-3 font-medium">{providerLabel(summary.provider)}</td>
                <td className="py-3 text-right">{formatCount(summary.writeSuccess, summary.sampleSize, locale)}</td>
                <td className="py-3 text-right">{formatCount(summary.retryBacklog, summary.sampleSize, locale)}</td>
                <td className="py-3 text-right">{formatCount(summary.missingDeliveries, summary.sampleSize, locale)}</td>
                <td className="py-3 text-right">{formatMs(summary.p50ReplicationLagMs)}</td>
                <td className="py-3 text-right">{formatMs(summary.p95ReplicationLagMs)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function RecallMetrics({ model, locale }: { model: MemoryComparisonModel; locale: string }) {
  const t = useMemoryT();
  return (
    <section className="rounded-md border bg-card p-4">
      <SectionHeader title={t("recall.title")} description={t("recall.description")} />
      <div className="mt-4 overflow-x-auto">
        <table className="w-full min-w-[780px] text-body">
          <thead className="border-b text-caption uppercase text-muted-foreground">
            <tr>
              <th className="py-2 text-left font-medium">{t("table.provider")}</th>
              <th className="py-2 text-right font-medium">{t("recall.correctness")}</th>
              <th className="py-2 text-right font-medium">{t("recall.confidence")}</th>
              <th className="py-2 text-right font-medium">{t("recall.p50_latency")}</th>
              <th className="py-2 text-right font-medium">{t("recall.p95_latency")}</th>
              <th className="py-2 text-right font-medium">{t("recall.tokens")}</th>
              <th className="py-2 text-right font-medium">{t("recall.cost")}</th>
              <th className="py-2 text-right font-medium">{t("recall.storage")}</th>
            </tr>
          </thead>
          <tbody>
            {providerRows(model).map((summary) => (
              <tr key={summary.provider} className="border-b last:border-b-0">
                <td className="py-3 font-medium">{providerLabel(summary.provider)}</td>
                <td className="py-3 text-right">{formatNullablePercent(summary.correctnessRate)}</td>
                <td className="py-3 text-right">{formatCi(summary.correctnessCi)}</td>
                <td className="py-3 text-right">{formatMs(summary.p50LatencyMs)}</td>
                <td className="py-3 text-right">{formatMs(summary.p95LatencyMs)}</td>
                <td className="py-3 text-right">{formatNumber(summary.totalTokens, locale)}</td>
                <td className="py-3 text-right">{formatUsd(summary.totalCostUsd)}</td>
                <td className="py-3 text-right">{formatBytes(summary.storageBytes, locale)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}


function RankMetrics({ model }: { model: MemoryComparisonModel }) {
  const t = useMemoryT();
  return (
    <section className="rounded-md border bg-card p-4">
      <SectionHeader title={t("rank.title")} description={t("rank.description")} />
      <div className="mt-4 overflow-x-auto">
        <table className="w-full min-w-[760px] text-body">
          <thead className="border-b text-caption uppercase text-muted-foreground">
            <tr>
              <th className="py-2 text-left font-medium">{t("table.query")}</th>
              <th className="py-2 text-right font-medium">{t("rank.hindsight_rank")}</th>
              <th className="py-2 text-right font-medium">{t("rank.mem0_rank")}</th>
              <th className="py-2 text-right font-medium">{t("rank.delta")}</th>
              <th className="py-2 text-right font-medium">{t("rank.overlap")}</th>
            </tr>
          </thead>
          <tbody>
            {model.samples.map((sample) => (
              <tr key={sample.correlationId} className="border-b last:border-b-0">
                <td className="max-w-md py-3 pr-4">
                  <span className="line-clamp-2">{sample.query}</span>
                </td>
                <td className="py-3 text-right">{formatRank(sample.hindsight.matchedRank)}</td>
                <td className="py-3 text-right">{formatRank(sample.mem0.matchedRank)}</td>
                <td className="py-3 text-right">{formatDelta(sample.rankDelta)}</td>
                <td className="py-3 text-right">{formatNullablePercent(sample.overlap)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function LifecycleTable({ model }: { model: MemoryComparisonModel }) {
  const t = useMemoryT();
  return (
    <section className="rounded-md border bg-card p-4">
      <SectionHeader title={t("lifecycle.title")} description={t("lifecycle.description")} />
      <div className="mt-4 grid gap-3 md:grid-cols-3">
        {providerRows(model).map((summary) => (
          <div key={summary.provider} className="rounded-md border bg-muted/20 p-3">
            <p className="text-body font-medium">{providerLabel(summary.provider)}</p>
            <dl className="mt-3 space-y-2 text-body">
              <MetricRow label={t("lifecycle.stale")} value={`${summary.stalePass}/${summary.sampleSize}`} />
              <MetricRow label={t("lifecycle.correction")} value={`${summary.correctionPass}/${summary.sampleSize}`} />
              <MetricRow label={t("lifecycle.deletion")} value={`${summary.deletionPass}/${summary.sampleSize}`} />
            </dl>
          </div>
        ))}
      </div>
    </section>
  );
}

function ComparisonChart({ model }: { model: MemoryComparisonModel }) {
  const t = useMemoryT();
  const data = providerRows(model).map((summary) => ({
    provider: providerLabel(summary.provider),
    success: summary.writeSuccess,
    retry: summary.retryBacklog,
    missing: summary.missingDeliveries,
  }));
  return (
    <section className="rounded-md border bg-card p-4">
      <SectionHeader title={t("chart.title")} description={t("chart.description")} />
      <ChartContainer
        config={{
          success: { label: t("write.success"), color: "var(--chart-2)" },
          retry: { label: t("write.retry_backlog"), color: "var(--chart-4)" },
          missing: { label: t("write.missing"), color: "var(--destructive)" },
        }}
        className="mt-4 aspect-[4/3] min-h-64 w-full"
      >
        <BarChart data={data} margin={{ left: 0, right: 8, top: 8, bottom: 0 }}>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="provider" tickLine={false} axisLine={false} />
          <YAxis allowDecimals={false} tickLine={false} axisLine={false} width={28} />
          <ChartTooltip content={<ChartTooltipContent />} />
          <Bar dataKey="success" stackId="status" fill="var(--color-success)" radius={[3, 3, 0, 0]} />
          <Bar dataKey="retry" stackId="status" fill="var(--color-retry)" radius={[3, 3, 0, 0]} />
          <Bar dataKey="missing" stackId="status" fill="var(--color-missing)" radius={[3, 3, 0, 0]} />
        </BarChart>
      </ChartContainer>
    </section>
  );
}

function ProvenanceDrilldown({ samples }: { samples: PairedMemorySample[] }) {
  const t = useMemoryT();
  return (
    <section className="rounded-md border bg-card p-4">
      <SectionHeader title={t("provenance.title")} description={t("provenance.description")} />
      <div className="mt-4 space-y-2">
        {samples.map((sample) => (
          <details key={sample.correlationId} className="rounded-md border bg-muted/20 p-3">
            <summary className="cursor-pointer text-body font-medium">{sample.query}</summary>
            <div className="mt-3 grid gap-3 text-caption md:grid-cols-2">
              <ProvenanceBlock label="Hindsight" observation={sample.hindsight} />
              <ProvenanceBlock label="mem0" observation={sample.mem0} />
            </div>
          </details>
        ))}
      </div>
    </section>
  );
}

function ProvenanceBlock({ label, observation }: { label: string; observation: ProviderObservation }) {
  return (
    <div className="min-w-0 rounded border bg-background p-2">
      <p className="mb-2 font-medium">{label}</p>
      <pre className="max-h-56 overflow-auto whitespace-pre-wrap break-words text-micro text-muted-foreground">
        {formatProvenance(observation.provenance)}
      </pre>
    </div>
  );
}

function ProviderBoard({ provider, model, locale }: { provider: MemoryProvider; model: MemoryComparisonModel; locale: string }) {
  const t = useMemoryT();
  const summary = model.providers[provider];
  const rows = model.samples.map((sample) => sample[provider]);
  return (
    <div className="space-y-4">
      <section className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile label={t("provider.samples")} value={formatNumber(summary.sampleSize, locale)} detail={providerLabel(provider)} />
        <StatTile label={t("provider.correctness")} value={formatNullablePercent(summary.correctnessRate)} detail={formatCi(summary.correctnessCi)} />
        <StatTile label={t("provider.latency")} value={formatMs(summary.p95LatencyMs)} detail={t("provider.p95_detail")} />
        <StatTile label={t("provider.cost_storage")} value={formatUsd(summary.totalCostUsd)} detail={formatBytes(summary.storageBytes, locale)} />
      </section>

      <section className="rounded-md border bg-card p-4">
        <SectionHeader title={t("provider.deliveries_title")} description={t("provider.deliveries_description")} />
        <div className="mt-4 overflow-x-auto">
          <table className="w-full min-w-[820px] text-body">
            <thead className="border-b text-caption uppercase text-muted-foreground">
              <tr>
                <th className="py-2 text-left font-medium">{t("table.query")}</th>
                <th className="py-2 text-right font-medium">{t("table.write_status")}</th>
                <th className="py-2 text-right font-medium">{t("table.rank")}</th>
                <th className="py-2 text-right font-medium">{t("table.correctness")}</th>
                <th className="py-2 text-right font-medium">{t("table.latency")}</th>
                <th className="py-2 text-right font-medium">{t("table.lag")}</th>
                <th className="py-2 text-right font-medium">{t("table.results")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr key={row.sampleId} className="border-b last:border-b-0">
                  <td className="max-w-md py-3 pr-4">
                    <span className="line-clamp-2">{row.query}</span>
                  </td>
                  <td className="py-3 text-right">{t(`states.write.${row.writeStatus}`)}</td>
                  <td className="py-3 text-right">{row.matchedRank ?? t("common.unknown")}</td>
                  <td className="py-3 text-right">{t(`states.correctness.${row.correctness}`)}</td>
                  <td className="py-3 text-right">{formatMs(row.latencyMs)}</td>
                  <td className="py-3 text-right">{formatMs(row.replicationLagMs)}</td>
                  <td className="py-3 text-right">{formatNumber(row.resultIds.length, locale)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}

function SectionHeader({ icon, title, description }: { icon?: ReactNode; title: string; description: string }) {
  return (
    <div className="flex items-start gap-2">
      {icon ? <span className="mt-0.5 text-muted-foreground">{icon}</span> : null}
      <div className="min-w-0">
        <h3 className="text-body font-semibold">{title}</h3>
        <p className="mt-1 text-body text-muted-foreground">{description}</p>
      </div>
    </div>
  );
}

function MetricRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="font-medium">{value}</dd>
    </div>
  );
}

function providerRows(model: MemoryComparisonModel): ProviderSummary[] {
  return [model.providers.hindsight, model.providers.mem0];
}

function providerLabel(provider: MemoryProvider): string {
  return provider === "hindsight" ? "Hindsight" : "mem0";
}

function formatNumber(value: number, locale: string): string {
  return new Intl.NumberFormat(locale).format(value);
}

function formatPercent(value: number): string {
  return `${Math.round(value * 100)}%`;
}

function formatNullablePercent(value: number | null): string {
  return value == null ? "-" : formatPercent(value);
}

function formatCount(value: number, total: number, locale: string): string {
  return `${formatNumber(value, locale)} / ${formatNumber(total, locale)}`;
}

function formatMs(value: number | null): string {
  if (value == null) return "-";
  return `${Math.round(value)} ms`;
}

function formatUsd(value: number): string {
  return new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", maximumFractionDigits: 4 }).format(value);
}

function formatBytes(value: number, locale: string): string {
  if (value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: unit === 0 ? 0 : 1 }).format(size)} ${units[unit]}`;
}

function formatCi(ci: [number, number] | null): string {
  if (!ci) return "coverage: -";
  return `95% CI ${formatPercent(ci[0])}-${formatPercent(ci[1])}`;
}

function formatRank(value: number | null): string {
  return value == null ? "-" : `#${value}`;
}

function formatDelta(value: number | null): string {
  if (value == null) return "-";
  if (value === 0) return "0";
  return value > 0 ? `+${value}` : `${value}`;
}

function formatProvenance(provenance: Record<string, unknown>): string {
  const summary = summarizeRedactedProvenance(provenance);
  return JSON.stringify({
    ...summary.fields,
    omitted_field_count: summary.omittedFieldCount,
  }, null, 2);
}
