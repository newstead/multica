import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const memoryKeys = {
  all: (wsId: string) => ["memory", wsId] as const,
  config: (wsId: string) => [...memoryKeys.all(wsId), "config"] as const,
  recallSamples: (wsId: string, limit: number, offset: number) =>
    [...memoryKeys.all(wsId), "recall-samples", limit, offset] as const,
  mem0Board: (
    wsId: string,
    limit: number,
    offset: number,
    projectId: string | null,
    agentId: string | null,
    issueId: string | null,
    taskId: string | null,
  ) => [...memoryKeys.all(wsId), "mem0-board", limit, offset, projectId, agentId, issueId, taskId] as const,
};

const STALE_TIME = 60 * 1000;

export function memoryConfigOptions(wsId: string) {
  return queryOptions({
    queryKey: memoryKeys.config(wsId),
    queryFn: () => api.getMemoryConfig(wsId),
    enabled: !!wsId,
    staleTime: STALE_TIME,
  });
}


export function memoryMem0BoardOptions(
  wsId: string,
  params: {
    limit?: number;
    offset?: number;
    project_id?: string | null;
    agent_id?: string | null;
    issue_id?: string | null;
    task_id?: string | null;
  } = {},
) {
  const limit = params.limit ?? 500;
  const offset = params.offset ?? 0;
  const projectId = params.project_id ?? null;
  const agentId = params.agent_id ?? null;
  const issueId = params.issue_id ?? null;
  const taskId = params.task_id ?? null;
  return queryOptions({
    queryKey: memoryKeys.mem0Board(wsId, limit, offset, projectId, agentId, issueId, taskId),
    queryFn: () => api.getMemoryMem0Board(wsId, {
      limit,
      offset,
      project_id: projectId,
      agent_id: agentId,
      issue_id: issueId,
      task_id: taskId,
    }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
    placeholderData: keepPreviousData,
  });
}

export function memoryRecallSamplesOptions(
  wsId: string,
  params: { limit?: number; offset?: number } = {},
) {
  const limit = params.limit ?? 200;
  const offset = params.offset ?? 0;
  return queryOptions({
    queryKey: memoryKeys.recallSamples(wsId, limit, offset),
    queryFn: () => api.listMemoryRecallSamples(wsId, { limit, offset }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
    placeholderData: keepPreviousData,
  });
}
