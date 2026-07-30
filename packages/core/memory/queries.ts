import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { api } from "../api";

type NullableFilter = string | null | undefined;

export interface MemoryAuditParams {
  limit?: number;
  offset?: number;
  project_id?: string;
  agent_id?: string;
  issue_id?: string;
  task_id?: string;
}

export const memoryKeys = {
  all: (workspaceId: string) => ["memory", workspaceId] as const,
  config: (workspaceId: string) => [...memoryKeys.all(workspaceId), "config"] as const,
  recallSamples: (
    workspaceId: string,
    params: {
      limit: number;
      offset: number;
      provider?: string;
      projectId?: NullableFilter;
      agentId?: NullableFilter;
    },
  ) =>
    [
      ...memoryKeys.all(workspaceId),
      "recall-samples",
      params.limit,
      params.offset,
      params.provider ?? null,
      params.projectId ?? null,
      params.agentId ?? null,
    ] as const,
  mem0Board: (
    workspaceId: string,
    limit: number,
    offset: number,
    projectId?: NullableFilter,
    agentId?: NullableFilter,
    issueId?: NullableFilter,
    taskId?: NullableFilter,
  ) =>
    [
      ...memoryKeys.all(workspaceId),
      "mem0-board",
      limit,
      offset,
      projectId ?? null,
      agentId ?? null,
      issueId ?? null,
      taskId ?? null,
    ] as const,
  audit: (wsId: string, params: MemoryAuditParams = {}) =>
    [...memoryKeys.all(wsId), "audit", params] as const,
};

const STALE_TIME = 60 * 1000;

export function memoryConfigOptions(workspaceId: string) {
  return queryOptions({
    queryKey: memoryKeys.config(workspaceId),
    queryFn: () => api.getMemoryConfig(workspaceId),
    enabled: Boolean(workspaceId),
    staleTime: STALE_TIME,
  });
}

export function memoryRecallSamplesOptions(
  workspaceId: string,
  params: {
    limit?: number;
    offset?: number;
    provider?: string;
    projectId?: NullableFilter;
    agentId?: NullableFilter;
  } = {},
) {
  const limit = params.limit ?? 100;
  const offset = params.offset ?? 0;
  const projectId = params.projectId ?? null;
  const agentId = params.agentId ?? null;
  return queryOptions({
    queryKey: memoryKeys.recallSamples(workspaceId, {
      limit,
      offset,
      provider: params.provider,
      projectId,
      agentId,
    }),
    queryFn: () =>
      api.getMemoryRecallSamples(workspaceId, {
        limit,
        offset,
        provider: params.provider,
        project_id: projectId ?? undefined,
        agent_id: agentId ?? undefined,
      }),
    enabled: Boolean(workspaceId),
    staleTime: STALE_TIME,
    placeholderData: keepPreviousData,
  });
}

export function memoryMem0BoardOptions(
  workspaceId: string,
  params: {
    limit?: number;
    offset?: number;
    projectId?: NullableFilter;
    agentId?: NullableFilter;
    issueId?: NullableFilter;
    taskId?: NullableFilter;
    project_id?: NullableFilter;
    agent_id?: NullableFilter;
    issue_id?: NullableFilter;
    task_id?: NullableFilter;
  } = {},
) {
  const limit = params.limit ?? 25;
  const offset = params.offset ?? 0;
  const projectId = params.projectId ?? params.project_id ?? null;
  const agentId = params.agentId ?? params.agent_id ?? null;
  const issueId = params.issueId ?? params.issue_id ?? null;
  const taskId = params.taskId ?? params.task_id ?? null;
  return queryOptions({
    queryKey: memoryKeys.mem0Board(
      workspaceId,
      limit,
      offset,
      projectId,
      agentId,
      issueId,
      taskId,
    ),
    queryFn: () =>
      api.getMemoryMem0Board(workspaceId, {
        limit,
        offset,
        project_id: projectId ?? undefined,
        agent_id: agentId ?? undefined,
        issue_id: issueId ?? undefined,
        task_id: taskId ?? undefined,
      }),
    enabled: Boolean(workspaceId),
    staleTime: STALE_TIME,
    placeholderData: keepPreviousData,
  });
}

export function memoryAuditOptions(wsId: string, params: MemoryAuditParams = {}) {
  return queryOptions({
    queryKey: memoryKeys.audit(wsId, params),
    queryFn: () => api.listMemoryAudit(wsId, params),
    enabled: !!wsId,
    staleTime: STALE_TIME,
    placeholderData: keepPreviousData,
  });
}
