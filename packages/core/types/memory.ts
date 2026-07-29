export type MemoryProvider = "hindsight" | "mem0" | string;
export type MemoryReadMode = "primary" | "shadow" | "dual" | string;

export interface MemoryConfigResponse {
  workspace_id: string;
  enabled: boolean;
  primary_provider: MemoryProvider;
  shadow_provider?: MemoryProvider | null;
  read_mode: MemoryReadMode;
  provider_settings: Record<string, unknown>;
  created_at?: string;
  updated_at?: string;
}

export interface MemoryRecallSample {
  id: string;
  workspace_id: string;
  project_id?: string | null;
  agent_id?: string | null;
  issue_id?: string | null;
  task_id?: string | null;
  provider: MemoryProvider;
  read_mode: MemoryReadMode;
  recall_correlation_id: string;
  query: string;
  results: unknown[];
  provenance: Record<string, unknown>;
  sampled_at: string;
}

export interface MemoryRecallSamplesResponse {
  samples: MemoryRecallSample[];
}
