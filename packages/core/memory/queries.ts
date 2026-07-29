import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const memoryKeys = {
  all: (wsId: string) => ["memory", wsId] as const,
  config: (wsId: string) => [...memoryKeys.all(wsId), "config"] as const,
  recallSamples: (
    wsId: string,
    params: {
      limit: number;
      offset: number;
      provider?: string;
      projectId?: string | null;
      agentId?: string | null;
    },
  ) =>
    [
      ...memoryKeys.all(wsId),
      "recall-samples",
      params.limit,
      params.offset,
      params.provider ?? null,
      params.projectId ?? null,
      params.agentId ?? null,
    ] as const,
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

export function memoryRecallSamplesOptions(
  wsId: string,
  params: {
    limit?: number;
    offset?: number;
    provider?: string;
    projectId?: string | null;
    agentId?: string | null;
  } = {},
) {
  const limit = params.limit ?? 100;
  const offset = params.offset ?? 0;
  const queryKey = memoryKeys.recallSamples(wsId, {
    limit,
    offset,
    provider: params.provider,
    projectId: params.projectId,
    agentId: params.agentId,
  });
  return queryOptions({
    queryKey,
    queryFn: () =>
      api.getMemoryRecallSamples(wsId, {
        limit,
        offset,
        provider: params.provider,
        project_id: params.projectId ?? undefined,
        agent_id: params.agentId ?? undefined,
      }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
    placeholderData: keepPreviousData,
  });
}
