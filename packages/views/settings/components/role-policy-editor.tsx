"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { AlertCircle, AlertTriangle, Bot, Cpu, Loader2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Switch } from "@multica/ui/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import { api } from "@multica/core/api";
import {
  agentListOptions,
  workspaceKeys,
  workspaceRolePolicyOptions,
} from "@multica/core/workspace/queries";
import {
  runtimeListOptions,
  runtimeModelsOptions,
} from "@multica/core/runtimes";
import type {
  Agent,
  AgentRuntime,
  RolePolicyFallback,
  WorkspaceRolePolicy,
  WorkspaceRolePolicyRule,
} from "@multica/core/types";
import { useT } from "../../i18n";
import {
  SettingsCard,
  SettingsRow,
  SettingsSaveState,
  SettingsSection,
  type SettingsSaveStatus,
} from "./settings-layout";

/**
 * Workspace role → AI execution config policy editor (ROLEPOL-0 / ROL-214).
 *
 * Renders the 10 canonical roles (TL/BE/FE/FS/QA/OPS/ML/DA/SRE/SEC) and lets
 * an owner/admin bind each to a concrete agent (Variant A), an execution
 * config layered over the assigned agent (Variant B: runtime + model +
 * thinking level + service tier), or no rule. `fallback` decides what happens
 * when the policy cannot be applied: `agent_default` keeps the historical
 * agent-centric behavior, `disabled` fails closed. Saving PUTs the full
 * matrix; the section is read-only for non-owner/admin members.
 */

export const ROLE_POLICY_ROLE_CODES = [
  "TL",
  "BE",
  "FE",
  "FS",
  "QA",
  "OPS",
  "ML",
  "DA",
  "SRE",
  "SEC",
] as const;

export type RolePolicyMode = "agent" | "exec" | "none";

interface RolePolicyRuleDraft {
  mode: RolePolicyMode;
  agentId: string | null;
  runtimeId: string | null;
  model: string | null;
  thinkingLevel: string | null;
  serviceTier: string | null;
  fallback: RolePolicyFallback;
}

type RolePolicyDraft = Record<string, RolePolicyRuleDraft>;

/** Select sentinel for "unset" — maps to `null` in the draft, absent in the wire. */
const UNSET = "__unset__";

function emptyRule(): RolePolicyRuleDraft {
  return {
    mode: "none",
    agentId: null,
    runtimeId: null,
    model: null,
    thinkingLevel: null,
    serviceTier: null,
    fallback: "agent_default",
  };
}

function ruleToDraft(rule?: WorkspaceRolePolicyRule): RolePolicyRuleDraft {
  const mode: RolePolicyMode = rule?.agent_id
    ? "agent"
    : rule?.runtime_id
      ? "exec"
      : "none";
  return {
    mode,
    agentId: rule?.agent_id ?? null,
    runtimeId: rule?.runtime_id ?? null,
    model: rule?.model ?? null,
    thinkingLevel: rule?.thinking_level ?? null,
    serviceTier: rule?.service_tier ?? null,
    fallback: rule?.fallback ?? "agent_default",
  };
}

function policyToDraft(policy: WorkspaceRolePolicy): RolePolicyDraft {
  const out: RolePolicyDraft = {};
  for (const code of ROLE_POLICY_ROLE_CODES) {
    out[code] = ruleToDraft(policy.rules?.[code]);
  }
  return out;
}

function draftToWire(draft: RolePolicyDraft): Record<string, WorkspaceRolePolicyRule> {
  const rules: Record<string, WorkspaceRolePolicyRule> = {};
  for (const [code, rule] of Object.entries(draft)) {
    if (rule.mode === "agent" && rule.agentId) {
      rules[code] = { agent_id: rule.agentId, fallback: rule.fallback };
    } else if (rule.mode === "exec" && rule.runtimeId) {
      rules[code] = {
        runtime_id: rule.runtimeId,
        model: rule.model ?? undefined,
        thinking_level: rule.thinkingLevel ?? undefined,
        service_tier: rule.serviceTier ?? undefined,
        fallback: rule.fallback,
      };
    } else if (rule.fallback === "disabled") {
      // A fallback-only row is meaningful: fail closed for this role.
      rules[code] = { fallback: "disabled" };
    }
    // mode "none" + agent_default equals "no rule"; the backend already
    // treats a missing row as agent_default, so omit it.
  }
  return rules;
}

function patchRule(
  current: RolePolicyRuleDraft,
  patch: Partial<RolePolicyRuleDraft>,
): RolePolicyRuleDraft {
  return {
    mode: patch.mode ?? current.mode,
    agentId: patch.agentId !== undefined ? patch.agentId : current.agentId,
    runtimeId: patch.runtimeId !== undefined ? patch.runtimeId : current.runtimeId,
    model: patch.model !== undefined ? patch.model : current.model,
    thinkingLevel:
      patch.thinkingLevel !== undefined ? patch.thinkingLevel : current.thinkingLevel,
    serviceTier:
      patch.serviceTier !== undefined ? patch.serviceTier : current.serviceTier,
    fallback: patch.fallback ?? current.fallback,
  };
}

function draftsEqual(a: RolePolicyDraft, b: RolePolicyDraft): boolean {
  for (const code of ROLE_POLICY_ROLE_CODES) {
    const x = a[code];
    const y = b[code];
    if (!x || !y) return false;
    if (
      x.mode !== y.mode ||
      x.fallback !== y.fallback ||
      x.agentId !== y.agentId ||
      x.runtimeId !== y.runtimeId ||
      x.model !== y.model ||
      x.thinkingLevel !== y.thinkingLevel ||
      x.serviceTier !== y.serviceTier
    ) {
      return false;
    }
  }
  return true;
}

export function RolePolicySection({
  workspaceId,
  canEdit,
}: {
  workspaceId: string | null | undefined;
  /** Owner/admin can edit; other members see the matrix read-only. */
  canEdit: boolean;
}) {
  const { t } = useT("settings");
  const qc = useQueryClient();
  const [draft, setDraft] = useState<RolePolicyDraft | null>(null);
  const [enabled, setEnabled] = useState(false);
  const [saveStatus, setSaveStatus] = useState<SettingsSaveStatus>("idle");
  const loadedWsId = useRef<string | null | undefined>(null);

  const policyQuery = useQuery(workspaceRolePolicyOptions(workspaceId ?? ""));
  const policy = policyQuery.data;
  const { data: agents = [] } = useQuery({
    ...agentListOptions(workspaceId ?? ""),
    enabled: !!workspaceId,
  });
  const { data: runtimes = [] } = useQuery({
    ...runtimeListOptions(workspaceId ?? ""),
    enabled: !!workspaceId,
  });

  // Reset local state when the workspace switches (same pattern as the
  // workspace details form).
  useEffect(() => {
    if (workspaceId !== loadedWsId.current) {
      loadedWsId.current = workspaceId;
      setDraft(null);
      setSaveStatus("idle");
    }
  }, [workspaceId]);

  useEffect(() => {
    if (policy && !draft) {
      setDraft(policyToDraft(policy));
      setEnabled(policy.enabled);
    }
  }, [policy, draft]);

  const dirty = useMemo(() => {
    if (!policy || !draft) return false;
    return (
      enabled !== policy.enabled || !draftsEqual(draft, policyToDraft(policy))
    );
  }, [policy, draft, enabled]);

  const activeRuleCount = useMemo(() => {
    if (!draft) return 0;
    return Object.values(draft).filter(
      (rule) => rule.mode !== "none" || rule.fallback === "disabled",
    ).length;
  }, [draft]);

  const updateRule = (code: string, patch: Partial<RolePolicyRuleDraft>) => {
    setDraft((prev) => {
      if (!prev) return prev;
      const current = prev[code];
      if (!current) return prev;
      const next = patchRule(current, patch);
      return { ...prev, [code]: next };
    });
  };

  const handleSave = async () => {
    if (!workspaceId || !draft) return;
    setSaveStatus("saving");
    try {
      const updated = await api.updateWorkspaceRolePolicy(workspaceId, {
        enabled,
        rules: draftToWire(draft),
      });
      setDraft(policyToDraft(updated));
      setEnabled(updated.enabled);
      setSaveStatus("saved");
      toast.success(t(($) => $.role_policy.saved), { id: "role-policy-save" });
      qc.setQueryData(workspaceKeys.rolePolicy(workspaceId), updated);
    } catch (error) {
      setSaveStatus("error");
      toast.error(
        error instanceof Error
          ? error.message
          : t(($) => $.role_policy.save_failed),
        { id: "role-policy-save" },
      );
    }
  };

  return (
    <SettingsSection
      title={t(($) => $.role_policy.title)}
      description={t(($) => $.role_policy.description)}
      action={
        canEdit && saveStatus !== "error" ? (
          <SettingsSaveState
            status={saveStatus}
            savingLabel={t(($) => $.role_policy.saving)}
            savedLabel={t(($) => $.role_policy.saved)}
            errorLabel={t(($) => $.role_policy.save_failed)}
          />
        ) : undefined
      }
    >
      <SettingsCard>
        <SettingsRow
          label={t(($) => $.role_policy.enabled_label)}
          description={t(($) => $.role_policy.enabled_hint)}
          size="select-wide"
        >
          <Switch
            checked={enabled}
            onCheckedChange={setEnabled}
            disabled={!canEdit}
            aria-label={t(($) => $.role_policy.enabled_label)}
          />
        </SettingsRow>

        {enabled && activeRuleCount === 0 && draft ? (
          <div className="flex items-start gap-2 px-4 py-3 text-caption text-muted-foreground">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warning" aria-hidden="true" />
            <span>{t(($) => $.role_policy.empty_warning)}</span>
          </div>
        ) : null}

        {!canEdit ? (
          <div className="px-4 py-3 text-caption text-muted-foreground">
            {t(($) => $.role_policy.manage_hint)}
          </div>
        ) : null}
      </SettingsCard>

      <SettingsCard>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-40">{t(($) => $.role_policy.role_header)}</TableHead>
              <TableHead className="w-36">{t(($) => $.role_policy.mode_header)}</TableHead>
              <TableHead>{t(($) => $.role_policy.agent_label)}</TableHead>
              <TableHead>{t(($) => $.role_policy.runtime_label)}</TableHead>
              <TableHead>{t(($) => $.role_policy.model_label)}</TableHead>
              <TableHead>{t(($) => $.role_policy.thinking_label)}</TableHead>
              <TableHead>{t(($) => $.role_policy.service_tier_label)}</TableHead>
              <TableHead className="w-44">{t(($) => $.role_policy.fallback_label)}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {ROLE_POLICY_ROLE_CODES.map((code) => (
              <RolePolicyRow
                key={code}
                code={code}
                label={t(($) => $.role_policy.roles[code])}
                rule={draft?.[code] ?? emptyRule()}
                agents={agents}
                runtimes={runtimes}
                canEdit={canEdit}
                onChange={(patch) => updateRule(code, patch)}
              />
            ))}
          </TableBody>
        </Table>
      </SettingsCard>

      {saveStatus === "error" ? (
        <div
          role="alert"
          className="flex items-start gap-2 px-4 py-3 text-caption text-destructive"
        >
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
          <span>{t(($) => $.role_policy.save_failed)}</span>
        </div>
      ) : null}

      {canEdit && dirty ? (
        <div className="flex justify-end">
          <Button size="sm" onClick={() => void handleSave()} disabled={saveStatus === "saving"}>
            {saveStatus === "saving"
              ? t(($) => $.role_policy.saving)
              : t(($) => $.role_policy.save)}
          </Button>
        </div>
      ) : null}
    </SettingsSection>
  );
}

function RolePolicyRow({
  code,
  label,
  rule,
  agents,
  runtimes,
  canEdit,
  onChange,
}: {
  code: string;
  label: string;
  rule: RolePolicyRuleDraft;
  agents: Agent[];
  runtimes: AgentRuntime[];
  canEdit: boolean;
  onChange: (patch: Partial<RolePolicyRuleDraft>) => void;
}) {
  const { t } = useT("settings");
  // Only non-archived agents with a runtime are bindable (the backend
  // enforces the same contract on PUT).
  const bindableAgents = useMemo(
    () => agents.filter((a) => !a.archived_at && a.runtime_id),
    [agents],
  );
  const agentItems = useMemo(() => {
    const out: Record<string, string> = {};
    for (const a of bindableAgents) out[a.id] = a.name;
    // A saved rule can reference an agent that is not in the bindable list
    // (archived/removed, or hidden from this member's view). Show a readable
    // fallback instead of the raw UUID in the trigger.
    if (rule.agentId && !out[rule.agentId]) {
      out[rule.agentId] = `${t(($) => $.role_policy.agent_unavailable)} (${rule.agentId.slice(0, 8)})`;
    }
    return out;
  }, [bindableAgents, rule.agentId, t]);
  const runtimeItems = useMemo(
    () =>
      Object.fromEntries(
        runtimes.map((r) => [r.id, r.custom_name || r.name] as [string, string]),
      ),
    [runtimes],
  );
  const selectedRuntime = useMemo(
    () => runtimes.find((r) => r.id === rule.runtimeId) ?? null,
    [runtimes, rule.runtimeId],
  );
  // Unknown runtime (list still loading) is treated as online so the catalog
  // attempt happens; a known-offline runtime skips discovery and falls back to
  // the runtime default (same pattern as the agent model picker).
  const runtimeOnline = selectedRuntime ? selectedRuntime.status === "online" : true;
  const modeItems = useMemo(
    () => ({
      agent: t(($) => $.role_policy.mode_agent),
      exec: t(($) => $.role_policy.mode_exec),
      none: t(($) => $.role_policy.mode_none),
    }),
    [t],
  );

  return (
    <TableRow>
      <TableCell>
        <span className="inline-flex items-center gap-1.5">
          <span className="font-mono text-micro text-muted-foreground">{code}</span>
          <span className="truncate">{label}</span>
        </span>
      </TableCell>
      <TableCell>
        <Select
          items={modeItems}
          value={rule.mode}
          disabled={!canEdit}
          onValueChange={(mode) => onChange({ mode: mode as RolePolicyMode })}
        >
          <SelectTrigger size="sm" className="w-full" aria-label={`${code} ${t(($) => $.role_policy.mode_header)}`}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent align="end">
            {Object.entries(modeItems).map(([value, itemLabel]) => (
              <SelectItem key={value} value={value}>
                {itemLabel}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </TableCell>
      <TableCell>
        {rule.mode === "agent" ? (
          <Select
            items={agentItems}
            value={rule.agentId ?? UNSET}
            disabled={!canEdit}
            onValueChange={(value) =>
              onChange({ agentId: value === UNSET ? null : value })
            }
          >
            <SelectTrigger size="sm" className="w-full" aria-label={`${code} ${t(($) => $.role_policy.agent_label)}`}>
              <SelectValue placeholder={t(($) => $.role_policy.agent_placeholder)} />
            </SelectTrigger>
            <SelectContent align="end">
              <SelectItem value={UNSET}>{t(($) => $.role_policy.agent_placeholder)}</SelectItem>
              {bindableAgents.map((a) => (
                <SelectItem key={a.id} value={a.id}>
                  <span className="inline-flex items-center gap-1.5">
                    <Bot className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />
                    {a.name}
                  </span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : null}
      </TableCell>
      <TableCell>
        {rule.mode === "exec" ? (
          <Select
            items={runtimeItems}
            value={rule.runtimeId ?? UNSET}
            disabled={!canEdit}
            onValueChange={(value) =>
              onChange({
                runtimeId: value === UNSET ? null : value,
                model: null,
                thinkingLevel: null,
                serviceTier: null,
              })
            }
          >
            <SelectTrigger size="sm" className="w-full" aria-label={`${code} ${t(($) => $.role_policy.runtime_label)}`}>
              <SelectValue placeholder={t(($) => $.role_policy.runtime_placeholder)} />
            </SelectTrigger>
            <SelectContent align="end">
              <SelectItem value={UNSET}>{t(($) => $.role_policy.runtime_placeholder)}</SelectItem>
              {runtimes.map((r) => (
                <SelectItem key={r.id} value={r.id}>
                  <span className="inline-flex items-center gap-1.5">
                    <Cpu className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />
                    {r.custom_name || r.name}
                  </span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : null}
      </TableCell>
      <TableCell>
        {rule.mode === "exec" && rule.runtimeId ? (
          <ModelCell code={code} rule={rule} canEdit={canEdit} runtimeOnline={runtimeOnline} onChange={onChange} />
        ) : null}
      </TableCell>
      <TableCell>
        {rule.mode === "exec" && rule.runtimeId ? (
          <ThinkingCell code={code} rule={rule} canEdit={canEdit} onChange={onChange} />
        ) : null}
      </TableCell>
      <TableCell>
        {rule.mode === "exec" && rule.runtimeId ? (
          <ServiceTierCell code={code} rule={rule} canEdit={canEdit} onChange={onChange} />
        ) : null}
      </TableCell>
      <TableCell>
        <FallbackSelect code={code} value={rule.fallback} canEdit={canEdit} onChange={(fallback) => onChange({ fallback })} />
      </TableCell>
    </TableRow>
  );
}

function ModelCell({
  code,
  rule,
  canEdit,
  runtimeOnline,
  onChange,
}: {
  code: string;
  rule: RolePolicyRuleDraft;
  canEdit: boolean;
  runtimeOnline: boolean;
  onChange: (patch: Partial<RolePolicyRuleDraft>) => void;
}) {
  const { t } = useT("settings");
  const modelsQuery = useQuery(
    runtimeModelsOptions(runtimeOnline ? rule.runtimeId : null),
  );
  // Memoise the catalog so every downstream useMemo gets a stable
  // reference; `?? []` would mint a fresh array on every render.
  const models = useMemo(
    () => modelsQuery.data?.models ?? [],
    [modelsQuery.data],
  );
  const supported = modelsQuery.data?.supported ?? true;
  const items = useMemo(() => {
    const out: Record<string, string> = { [UNSET]: t(($) => $.role_policy.model_default) };
    for (const m of models) out[m.id] = m.label || m.id;
    return out;
  }, [models, t]);

  // Providers whose runtime ignores per-agent model selection: drop any
  // saved model instead of persisting a ghost value (same contract as the
  // agent model picker).
  useEffect(() => {
    if (!supported && rule.model) {
      onChange({ model: null, thinkingLevel: null, serviceTier: null });
    }
  }, [supported, rule.model, onChange]);

  if (!runtimeOnline) {
    // Offline runtime: no live discovery; the rule falls back to the
    // runtime's own default model.
    return (
      <div className="flex min-w-0 flex-col gap-1.5">
        <Select
          items={{ [UNSET]: t(($) => $.role_policy.model_default) }}
          value={UNSET}
          disabled={!canEdit}
          onValueChange={() => onChange({ model: null, thinkingLevel: null, serviceTier: null })}
        >
          <SelectTrigger size="sm" className="w-full" aria-label={`${code} ${t(($) => $.role_policy.model_label)}`}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent align="end">
            <SelectItem value={UNSET}>{t(($) => $.role_policy.model_default)}</SelectItem>
          </SelectContent>
        </Select>
        <p className="text-caption leading-4 text-muted-foreground">
          {t(($) => $.role_policy.catalog_runtime_offline)}
        </p>
      </div>
    );
  }

  if (modelsQuery.isLoading) {
    return (
      <div className="flex items-center gap-1.5 text-caption text-muted-foreground">
        <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
        <span>{t(($) => $.role_policy.catalog_loading)}</span>
      </div>
    );
  }

  if (modelsQuery.isError) {
    // Discovery failed (daemon unreachable / timed out): keep the rule
    // usable with the runtime default and say so instead of a silent
    // empty dropdown.
    return (
      <div className="flex min-w-0 flex-col gap-1.5">
        <Select
          items={items}
          value={rule.model ?? UNSET}
          disabled={!canEdit}
          onValueChange={(value) =>
            onChange({
              model: value === UNSET ? null : value,
              thinkingLevel: null,
              serviceTier: null,
            })
          }
        >
          <SelectTrigger size="sm" className="w-full" aria-label={`${code} ${t(($) => $.role_policy.model_label)}`}>
            <SelectValue placeholder={t(($) => $.role_policy.model_default)} />
          </SelectTrigger>
          <SelectContent align="end">
            {Object.entries(items).map(([value, itemLabel]) => (
              <SelectItem key={value} value={value}>
                {itemLabel}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <p className="text-caption leading-4 text-muted-foreground">
          {t(($) => $.role_policy.catalog_unavailable)}
        </p>
      </div>
    );
  }

  if (!supported) {
    return (
      <p className="text-caption leading-4 text-muted-foreground">
        {t(($) => $.role_policy.catalog_managed_by_runtime)}
      </p>
    );
  }

  return (
    <Select
      items={items}
      value={rule.model ?? UNSET}
      disabled={!canEdit}
      onValueChange={(value) =>
        onChange({
          model: value === UNSET ? null : value,
          thinkingLevel: null,
          serviceTier: null,
        })
      }
    >
      <SelectTrigger size="sm" className="w-full" aria-label={`${code} ${t(($) => $.role_policy.model_label)}`}>
        <SelectValue placeholder={t(($) => $.role_policy.model_default)} />
      </SelectTrigger>
      <SelectContent align="end">
        {Object.entries(items).map(([value, itemLabel]) => (
          <SelectItem key={value} value={value}>
            {itemLabel}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

function ThinkingCell({
  code,
  rule,
  canEdit,
  onChange,
}: {
  code: string;
  rule: RolePolicyRuleDraft;
  canEdit: boolean;
  onChange: (patch: Partial<RolePolicyRuleDraft>) => void;
}) {
  const { t } = useT("settings");
  const modelsQuery = useQuery(runtimeModelsOptions(rule.runtimeId));
  const entry = useMemo(
    () => modelsQuery.data?.models.find((m) => m.id === rule.model) ?? null,
    [modelsQuery.data, rule.model],
  );
  const levels = useMemo(
    () => entry?.thinking?.supported_levels ?? [],
    [entry],
  );
  const items = useMemo(() => {
    const out: Record<string, string> = { [UNSET]: t(($) => $.role_policy.thinking_default) };
    for (const l of levels) out[l.value] = l.label;
    return out;
  }, [levels, t]);
  if (levels.length === 0 && !rule.thinkingLevel) return null;
  return (
    <Select
      items={items}
      value={rule.thinkingLevel ?? UNSET}
      disabled={!canEdit}
      onValueChange={(value) =>
        onChange({ thinkingLevel: value === UNSET ? null : value })
      }
    >
      <SelectTrigger size="sm" className="w-full" aria-label={`${code} ${t(($) => $.role_policy.thinking_label)}`}>
        <SelectValue placeholder={t(($) => $.role_policy.thinking_default)} />
      </SelectTrigger>
      <SelectContent align="end">
        {Object.entries(items).map(([value, itemLabel]) => (
          <SelectItem key={value} value={value}>
            {itemLabel}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

function ServiceTierCell({
  code,
  rule,
  canEdit,
  onChange,
}: {
  code: string;
  rule: RolePolicyRuleDraft;
  canEdit: boolean;
  onChange: (patch: Partial<RolePolicyRuleDraft>) => void;
}) {
  const { t } = useT("settings");
  const modelsQuery = useQuery(runtimeModelsOptions(rule.runtimeId));
  const entry = useMemo(
    () => modelsQuery.data?.models.find((m) => m.id === rule.model) ?? null,
    [modelsQuery.data, rule.model],
  );
  const tiers = useMemo(() => entry?.service_tiers ?? [], [entry]);
  const items = useMemo(() => {
    const out: Record<string, string> = { [UNSET]: t(($) => $.role_policy.service_tier_default) };
    for (const tier of tiers) out[tier.id] = tier.name;
    return out;
  }, [tiers, t]);
  if (tiers.length === 0 && !rule.serviceTier) return null;
  return (
    <Select
      items={items}
      value={rule.serviceTier ?? UNSET}
      disabled={!canEdit}
      onValueChange={(value) =>
        onChange({ serviceTier: value === UNSET ? null : value })
      }
    >
      <SelectTrigger size="sm" className="w-full" aria-label={`${code} ${t(($) => $.role_policy.service_tier_label)}`}>
        <SelectValue placeholder={t(($) => $.role_policy.service_tier_default)} />
      </SelectTrigger>
      <SelectContent align="end">
        {Object.entries(items).map(([value, itemLabel]) => (
          <SelectItem key={value} value={value}>
            {itemLabel}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

function FallbackSelect({
  code,
  value,
  canEdit,
  onChange,
}: {
  code: string;
  value: RolePolicyFallback;
  canEdit: boolean;
  onChange: (next: RolePolicyFallback) => void;
}) {
  const { t } = useT("settings");
  const items = useMemo(
    () => ({
      agent_default: t(($) => $.role_policy.fallback_agent_default),
      disabled: t(($) => $.role_policy.fallback_disabled),
    }),
    [t],
  );
  return (
    <Select
      items={items}
      value={value}
      disabled={!canEdit}
      onValueChange={(next) => onChange(next as RolePolicyFallback)}
    >
      <SelectTrigger size="sm" className="w-full" aria-label={`${code} ${t(($) => $.role_policy.fallback_label)}`}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent align="end">
        {Object.entries(items).map(([itemValue, itemLabel]) => (
          <SelectItem key={itemValue} value={itemValue}>
            {itemLabel}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
