export interface MemoryConfig {
  workspace_id: string;
  enabled: boolean;
  primary_provider: string;
  shadow_provider?: string | null;
  read_mode: string;
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
  provider: string;
  read_mode: string;
  recall_correlation_id: string;
  query: string;
  results: unknown[];
  provenance: Record<string, unknown>;
  sampled_at: string;
}

export interface MemoryRecallSamplesResponse {
  samples: MemoryRecallSample[];
}
