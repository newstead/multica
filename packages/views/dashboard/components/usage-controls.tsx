"use client";

import { FolderKanban } from "lucide-react";
import {
  NumberFlow,
  NumberFlowGroup,
} from "@multica/ui/components/ui/number-flow";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import {
  Tabs,
  TabsList,
  TabsTrigger,
} from "@multica/ui/components/ui/tabs";
import { paths, useWorkspaceSlug } from "@multica/core/paths";
import { useNavigation } from "../../navigation";
import { ProjectIcon } from "../../projects/components/project-icon";
import { useT } from "../../i18n";
import { formatDuration } from "../utils";

export const TIME_RANGES = [
  { label: "1d", days: 1, dims: ["daily"] as const },
  { label: "7d", days: 7, dims: ["daily"] as const },
  { label: "30d", days: 30, dims: ["daily", "weekly"] as const },
  { label: "90d", days: 90, dims: ["daily", "weekly"] as const },
  { label: "180d", days: 180, dims: ["weekly"] as const },
] as const;

export type TimeRange = (typeof TIME_RANGES)[number]["days"];
export type Dim = "daily" | "weekly";

export const DEFAULT_DAYS_BY_DIM: Record<Dim, TimeRange> = {
  daily: 30,
  weekly: 90,
};

export function rangesForDim(dim: Dim) {
  return TIME_RANGES.filter((r) => (r.dims as readonly string[]).includes(dim));
}

// Sentinel for "no project filter" — kept distinct from the empty string
// so it survives a refactor that ever lets a project be slug-keyed.
export const ALL_PROJECTS = "__all__";

export function Segmented<T extends string | number>({
  label,
  value,
  onChange,
  options,
}: {
  label?: string;
  value: T;
  onChange: (v: T) => void;
  options: readonly { label: string; value: T }[];
}) {
  return (
    <div
      role={label ? "group" : undefined}
      aria-label={label}
      className="inline-flex items-center gap-0.5 rounded-md bg-muted p-0.5"
    >
      {options.map((o) => (
        <button
          key={String(o.value)}
          type="button"
          onClick={() => onChange(o.value)}
          className={`rounded-sm px-2.5 py-1 text-xs font-medium transition-colors ${
            o.value === value
              ? "bg-background text-foreground shadow-sm"
              : "text-muted-foreground hover:text-foreground"
          }`}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function UsagePageTabs({ value }: { value: "overview" | "agents" }) {
  const slug = useWorkspaceSlug();
  if (!slug) return <UsageTabsView value={value} onChange={() => {}} />;
  return <UsagePageTabsWithNavigation value={value} slug={slug} />;
}

function UsagePageTabsWithNavigation({
  value,
  slug,
}: {
  value: "overview" | "agents";
  slug: string;
}) {
  const navigation = useNavigation();
  const workspacePaths = paths.workspace(slug);
  return (
    <UsageTabsView
      value={value}
      onChange={(next) => {
        navigation.push(
          next === "agents" ? workspacePaths.usageAgents() : workspacePaths.usage(),
        );
      }}
    />
  );
}

function UsageTabsView({
  value,
  onChange,
}: {
  value: "overview" | "agents";
  onChange: (next: string) => void;
}) {
  const { t } = useT("usage");
  return (
    <Tabs value={value} onValueChange={onChange}>
      <TabsList className="h-8">
        <TabsTrigger value="overview" className="px-2.5 text-xs">
          {t(($) => $.tabs.overview)}
        </TabsTrigger>
        <TabsTrigger value="agents" className="px-2.5 text-xs">
          {t(($) => $.tabs.agents)}
        </TabsTrigger>
      </TabsList>
    </Tabs>
  );
}

export function ProjectFilter({
  projects,
  value,
  onChange,
}: {
  projects: { id: string; title: string; icon: string | null }[];
  value: string;
  onChange: (v: string) => void;
}) {
  const { t } = useT("usage");
  const allLabel = t(($) => $.filter.all_projects);
  const selected = projects.find((p) => p.id === value);
  const selectedTitle =
    value === ALL_PROJECTS ? allLabel : selected?.title ?? allLabel;
  const projectItems = [
    { value: ALL_PROJECTS, label: allLabel },
    ...projects.map((project) => ({ value: project.id, label: project.title })),
  ];

  return (
    <Select
      items={projectItems}
      value={value}
      onValueChange={(v) => onChange(v ?? ALL_PROJECTS)}
    >
      <SelectTrigger size="sm" className="min-w-[180px]">
        <SelectValue>
          {() => (
            <>
              {selected ? (
                <ProjectIcon project={selected} size="sm" />
              ) : (
                <FolderKanban className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              )}
              <span className="truncate">{selectedTitle}</span>
            </>
          )}
        </SelectValue>
      </SelectTrigger>
      <SelectContent align="start" alignItemWithTrigger={false} className="max-h-72">
        <SelectItem value={ALL_PROJECTS}>
          <FolderKanban className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          <span className="truncate">{allLabel}</span>
        </SelectItem>
        {projects.map((p) => (
          <SelectItem key={p.id} value={p.id}>
            <ProjectIcon project={p} size="sm" />
            <span className="truncate">{p.title}</span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

export function DurationNumberFlow({
  seconds,
  lessThanMinuteLabel,
  locales,
}: {
  seconds: number;
  lessThanMinuteLabel: string;
  locales?: Intl.LocalesArgument;
}) {
  const label = formatDuration(seconds, lessThanMinuteLabel);
  const parts = Array.from(label.matchAll(/(\d+)([a-z]+)/gi), (match) => ({
    value: Number(match[1]),
    unit: match[2] ?? "",
  }));

  if (parts.length === 0) return label;

  return (
    <>
      <span className="sr-only">{label}</span>
      <NumberFlowGroup>
        <span aria-hidden className="inline-flex items-baseline gap-1">
          {parts.map((part) => (
            <NumberFlow
              key={part.unit}
              value={part.value}
              locales={locales}
              suffix={part.unit}
              format={{ maximumFractionDigits: 0, useGrouping: false }}
            />
          ))}
        </span>
      </NumberFlowGroup>
    </>
  );
}
