import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { MemoryEraseRequest, MemoryMutationRequest } from "../types";
import { useWorkspaceId } from "../hooks";
import { memoryKeys } from "./queries";

function useInvalidateMemoryAudit() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return () => qc.invalidateQueries({ queryKey: memoryKeys.all(wsId) });
}

export function useCorrectMemory() {
  const invalidate = useInvalidateMemoryAudit();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ eventId, ...body }: { eventId: string } & MemoryMutationRequest) =>
      api.correctMemoryAuditEvent(wsId, eventId, body),
    onSettled: invalidate,
  });
}

export function useInvalidateMemory() {
  const invalidate = useInvalidateMemoryAudit();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ eventId, ...body }: { eventId: string } & MemoryMutationRequest) =>
      api.invalidateMemoryAuditEvent(wsId, eventId, body),
    onSettled: invalidate,
  });
}

export function useDeleteMemory() {
  const invalidate = useInvalidateMemoryAudit();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ eventId, ...body }: { eventId: string } & MemoryMutationRequest) =>
      api.deleteMemoryAuditEvent(wsId, eventId, body),
    onSettled: invalidate,
  });
}

export function useEraseMemoryScope() {
  const invalidate = useInvalidateMemoryAudit();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (body: MemoryEraseRequest) => api.eraseMemoryScope(wsId, body),
    onSettled: invalidate,
  });
}
