"use client";

import { useMemo, useState } from "react";
import { Ban, Download, RefreshCw, Search, Trash2, Wrench } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { memoryAuditOptions, type MemoryAuditParams } from "@multica/core/memory";
import { useCorrectMemory, useDeleteMemory, useEraseMemoryScope, useInvalidateMemory } from "@multica/core/memory";
import type { MemoryAuditDelivery, MemoryAuditEvent, MemoryMutationProviderResult } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@multica/ui/components/ui/table";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useT } from "../i18n";

type ActionKind = "correct" | "invalidate" | "delete";
type EraseScope = "workspace" | "project" | "issue";
type MemoryT = (key: string, options?: Record<string, unknown>) => string;

const EMPTY_EVENTS: MemoryAuditEvent[] = [];

type ActionState = {
  kind: ActionKind;
  event: MemoryAuditEvent;
  delivery?: MemoryAuditDelivery;
} | null;

function statusTone(status: string): "default" | "secondary" | "destructive" | "outline" {
  if (status === "delivered") return "default";
  if (status === "queued" || status === "retry" || status === "delivering") return "secondary";
  if (status === "terminal_failed" || status === "failed") return "destructive";
  return "outline";
}

function formatDate(value?: string | null) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function compactId(value?: string | null) {
  if (!value) return "-";
  return value.length > 14 ? `${value.slice(0, 8)}...${value.slice(-4)}` : value;
}

function resultSummary(results: MemoryMutationProviderResult[]) {
  if (results.length === 0) return "0/0";
  const ok = results.filter((r) => r.status === "delivered").length;
  return `${ok}/${results.length}`;
}

function responseString(response: Record<string, unknown> | undefined, key: string) {
  const value = response?.[key];
  return typeof value === "string" && value.trim() ? value.trim() : "";
}

function hasVerifiedInvalidateTarget(event: MemoryAuditEvent, delivery: MemoryAuditDelivery) {
  if (delivery.provider !== "hindsight") return Boolean(delivery.provider_memory_id);
  if (event.event_type === "invalidate") return Boolean(delivery.provider_memory_id);
  return Boolean(responseString(delivery.response, "memory_id") || responseString(delivery.response, "id"));
}

function ProviderResults({ results }: { results: MemoryMutationProviderResult[] }) {
  const { t: rawT } = useT("memory");
  const t = rawT as MemoryT;
  return (
    <div className="grid gap-2 text-body">
      <div className="text-muted-foreground">{t("audit.dialog.result", { count: resultSummary(results) })}</div>
      <div className="grid gap-1">
        {results.map((result, index) => (
          <div key={`${result.provider}-${result.provider_memory_id ?? result.delivery_id ?? index}`} className="grid gap-1 rounded-md border border-border px-3 py-2">
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant={statusTone(result.status)}>{result.provider}</Badge>
              <span>{result.status}</span>
              <span className="text-caption text-muted-foreground">{compactId(result.provider_memory_id)}</span>
            </div>
            {result.error ? <div className="text-caption text-destructive">{result.error}</div> : null}
          </div>
        ))}
      </div>
    </div>
  );
}

export function MemoryAuditBoard() {
  const { t: rawT } = useT("memory");
  const t = rawT as MemoryT;
  const wsId = useWorkspaceId();
  const [filters, setFilters] = useState<MemoryAuditParams>({ limit: 100 });
  const [draftFilters, setDraftFilters] = useState({ project_id: "", issue_id: "", agent_id: "" });
  const [action, setAction] = useState<ActionState>(null);
  const [text, setText] = useState("");
  const [reason, setReason] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [lastResults, setLastResults] = useState<MemoryMutationProviderResult[] | null>(null);

  const audit = useQuery(memoryAuditOptions(wsId, filters));
  const correct = useCorrectMemory();
  const invalidate = useInvalidateMemory();
  const deleteMemory = useDeleteMemory();
  const erase = useEraseMemoryScope();

  const events = audit.data?.events ?? EMPTY_EVENTS;
  const totals = useMemo(() => {
    const deliveries = events.flatMap((event) => event.deliveries);
    return {
      events: events.length,
      delivered: deliveries.filter((d) => d.status === "delivered").length,
      failed: deliveries.filter((d) => d.status === "terminal_failed" || d.status === "failed").length,
    };
  }, [events]);

  const openAction = (kind: ActionKind, event: MemoryAuditEvent, delivery?: MemoryAuditDelivery) => {
    setAction({ kind, event, delivery });
    setText(event.text ?? "");
    setReason("");
    setConfirmation("");
    setLastResults(null);
  };

  const closeAction = () => {
    setAction(null);
    setLastResults(null);
  };

  const submitAction = async () => {
    if (!action) return;
    const base = {
      eventId: action.event.id,
      provider: action.delivery?.provider,
      provider_memory_id: action.delivery?.provider_memory_id ?? undefined,
      reason: reason || undefined,
    };
    const response = action.kind === "correct"
      ? await correct.mutateAsync({ ...base, text })
      : action.kind === "invalidate"
        ? await invalidate.mutateAsync({ ...base, confirmation: "INVALIDATE" })
        : await deleteMemory.mutateAsync({ ...base, confirmation: "DELETE" });
    setLastResults(response.results);
  };

  const applyFilters = () => {
    setFilters({
      limit: 100,
      project_id: draftFilters.project_id || undefined,
      issue_id: draftFilters.issue_id || undefined,
      agent_id: draftFilters.agent_id || undefined,
    });
  };

  const exportAudit = async () => {
    const data = await api.exportMemoryAudit(wsId, filters);
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `memory-audit-${wsId}.json`;
    link.click();
    URL.revokeObjectURL(url);
  };

  const canEraseScope = (scope: EraseScope) => {
    if (confirmation !== "ERASE") return false;
    if (scope === "project") return draftFilters.project_id.trim() !== "";
    if (scope === "issue") return draftFilters.issue_id.trim() !== "";
    return true;
  };

  const eraseScope = async (scope: EraseScope) => {
    const response = await erase.mutateAsync({
      scope,
      project_id: scope === "project" ? draftFilters.project_id.trim() : undefined,
      issue_id: scope === "issue" ? draftFilters.issue_id.trim() : undefined,
      confirmation: "ERASE",
    });
    setLastResults(response.results);
  };

  const actionTitle = (kind: ActionKind) => {
    if (kind === "correct") return t("audit.dialog.correct.title");
    if (kind === "invalidate") return t("audit.dialog.invalidate.title");
    return t("audit.dialog.delete.title");
  };

  return (
    <section className="rounded-md border bg-card">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
        <div className="min-w-0">
          <h2 className="text-body font-medium">{t("audit.title")}</h2>
          <p className="text-caption text-muted-foreground">{t("audit.subtitle")}</p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => audit.refetch()} disabled={audit.isFetching}>
            <RefreshCw className="size-4" />
            {t("audit.refresh")}
          </Button>
          <Button variant="outline" size="sm" onClick={exportAudit}>
            <Download className="size-4" />
            {t("audit.export")}
          </Button>
        </div>
      </div>

      <div className="grid gap-3 border-b border-border px-4 py-3 xl:grid-cols-[1fr_auto]">
        <div className="grid gap-2 sm:grid-cols-3">
          <Input placeholder={t("audit.filters.project")} value={draftFilters.project_id} onChange={(e) => setDraftFilters((v) => ({ ...v, project_id: e.target.value }))} />
          <Input placeholder={t("audit.filters.issue")} value={draftFilters.issue_id} onChange={(e) => setDraftFilters((v) => ({ ...v, issue_id: e.target.value }))} />
          <Input placeholder={t("audit.filters.agent")} value={draftFilters.agent_id} onChange={(e) => setDraftFilters((v) => ({ ...v, agent_id: e.target.value }))} />
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" onClick={applyFilters}><Search className="size-4" />{t("audit.filters.apply")}</Button>
          <Button size="sm" variant="destructive" onClick={() => eraseScope("workspace")} disabled={erase.isPending || !canEraseScope("workspace")}>
            <Trash2 className="size-4" />{t("audit.erase.workspace")}
          </Button>
          <Button size="sm" variant="destructive" onClick={() => eraseScope("project")} disabled={erase.isPending || !canEraseScope("project")}>
            <Trash2 className="size-4" />{t("audit.erase.project")}
          </Button>
          <Button size="sm" variant="destructive" onClick={() => eraseScope("issue")} disabled={erase.isPending || !canEraseScope("issue")}>
            <Trash2 className="size-4" />{t("audit.erase.issue")}
          </Button>
          <Input className="w-28" placeholder="ERASE" value={confirmation} onChange={(e) => setConfirmation(e.target.value)} />
        </div>
      </div>

      <div className="grid grid-cols-3 border-b border-border text-body">
        <div className="px-4 py-3"><div className="text-muted-foreground">{t("audit.stats.events")}</div><div className="text-title font-medium">{totals.events}</div></div>
        <div className="px-4 py-3"><div className="text-muted-foreground">{t("audit.stats.delivered")}</div><div className="text-title font-medium">{totals.delivered}</div></div>
        <div className="px-4 py-3"><div className="text-muted-foreground">{t("audit.stats.failed")}</div><div className="text-title font-medium">{totals.failed}</div></div>
      </div>

      {!action && lastResults ? <div className="border-b border-border px-4 py-2"><ProviderResults results={lastResults} /></div> : null}

      <div className="overflow-auto px-4 py-3">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("audit.table.created")}</TableHead>
              <TableHead>{t("audit.table.source")}</TableHead>
              <TableHead>{t("audit.table.scope")}</TableHead>
              <TableHead>{t("audit.table.event")}</TableHead>
              <TableHead>{t("audit.table.delivery")}</TableHead>
              <TableHead className="text-right">{t("audit.table.actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {events.map((event) => (
              <TableRow key={event.id}>
                <TableCell className="whitespace-nowrap text-caption text-muted-foreground">{formatDate(event.created_at)}</TableCell>
                <TableCell>
                  <div className="font-medium">{event.source_type ?? event.event_type}</div>
                  <div className="text-caption text-muted-foreground">{compactId(event.source_id ?? event.id)}</div>
                </TableCell>
                <TableCell className="text-caption text-muted-foreground">
                  <div>{t("audit.scope.project")} {compactId(event.project_id)}</div>
                  <div>{t("audit.scope.issue")} {compactId(event.issue_id)}</div>
                </TableCell>
                <TableCell><Badge variant="outline">{event.event_type}</Badge></TableCell>
                <TableCell>
                  <div className="flex flex-col gap-1">
                    {event.deliveries.length === 0 ? <span className="text-caption text-muted-foreground">{t("audit.table.no_delivery")}</span> : event.deliveries.map((delivery) => (
                      <div key={delivery.id} className="flex flex-wrap items-center gap-2 text-caption">
                        <Badge variant={statusTone(delivery.status)}>{delivery.provider}</Badge>
                        <span>{delivery.status}</span>
                        <span className="text-muted-foreground">{compactId(delivery.provider_memory_id)}</span>
                        {delivery.error ? <span className="text-destructive">{delivery.error}</span> : null}
                      </div>
                    ))}
                  </div>
                </TableCell>
                <TableCell>
                  <div className="flex justify-end gap-1">
                    {event.deliveries.map((delivery) => (
                      <div key={delivery.id} className="flex gap-1">
                        <Button size="icon-sm" variant="ghost" title={t("audit.actions.correct")} onClick={() => openAction("correct", event, delivery)}><Wrench className="size-4" /></Button>
                        {hasVerifiedInvalidateTarget(event, delivery) ? (
                          <Button size="icon-sm" variant="ghost" title={t("audit.actions.invalidate")} onClick={() => openAction("invalidate", event, delivery)}><Ban className="size-4" /></Button>
                        ) : null}
                        <Button size="icon-sm" variant="ghost" title={t("audit.actions.delete")} onClick={() => openAction("delete", event, delivery)}><Trash2 className="size-4" /></Button>
                      </div>
                    ))}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        {events.length === 0 && <div className="py-12 text-center text-body text-muted-foreground">{audit.isLoading ? t("audit.loading") : t("audit.empty")}</div>}
      </div>

      <Dialog open={!!action} onOpenChange={(open) => { if (!open) closeAction(); }}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{action ? actionTitle(action.kind) : ""}</DialogTitle>
          </DialogHeader>
          {action?.kind === "correct" ? (
            <Textarea value={text} onChange={(e) => setText(e.target.value)} rows={5} />
          ) : null}
          {action ? <Input placeholder={t("audit.dialog.reason")} value={reason} onChange={(e) => setReason(e.target.value)} /> : null}
          {action?.kind !== "correct" ? (
            <Input placeholder={action?.kind === "delete" ? "DELETE" : "INVALIDATE"} value={confirmation} onChange={(e) => setConfirmation(e.target.value)} />
          ) : null}
          {lastResults ? <ProviderResults results={lastResults} /> : null}
          <DialogFooter>
            <Button variant="outline" onClick={closeAction}>{t("audit.cancel")}</Button>
            <Button
              variant={action?.kind === "delete" ? "destructive" : "default"}
              disabled={!action || correct.isPending || invalidate.isPending || deleteMemory.isPending || (action?.kind === "delete" && confirmation !== "DELETE") || (action?.kind === "invalidate" && confirmation !== "INVALIDATE")}
              onClick={submitAction}
            >
              {t("audit.confirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}
