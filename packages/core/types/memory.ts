export type MemoryProvider = "hindsight" | "mem0" | string;
export type MemoryReadMode = "primary" | "shadow" | "dual" | string;

export interface MemoryConfig {
  workspace_id: string;
  enabled: boolean;
  primary_provider: MemoryProvider;
  shadow_provider?: MemoryProvider | null;
  read_mode: MemoryReadMode;
  provider_settings: Record<string, unknown>;
  created_at?: string;
  updated_at?: string;
}

export type MemoryConfigResponse = MemoryConfig;

export interface MemoryProviderHealth {
  provider: MemoryProvider;
  ok: boolean;
  details?: Record<string, unknown>;
}

export interface MemoryMem0BoardDelivery {
  id: string;
  workspace_id: string;
  memory_event_id: string;
  project_id?: string | null;
  agent_id?: string | null;
  issue_id?: string | null;
  task_id?: string | null;
  event_type: "retain" | "update" | "invalidate" | "delete" | string;
  provider: MemoryProvider;
  status: "queued" | "delivering" | "delivered" | "retry" | "terminal_failed" | "skipped" | string;
  attempt_count: number;
  delivery_lag_ms: number;
  event_created_at: string;
  delivery_created_at: string;
  last_attempt_at?: string;
  terminal_at?: string;
  updated_at: string;
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

export interface MemoryMem0BoardResponse {
  health?: MemoryProviderHealth | null;
  health_error?: string;
  deliveries: MemoryMem0BoardDelivery[];
  recall_samples: MemoryRecallSample[];
}

export interface MemoryRecallSamplesResponse {
  samples: MemoryRecallSample[];
}
