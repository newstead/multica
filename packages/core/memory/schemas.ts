import { z } from "zod";
import type {
  ListMemoryRecallSamplesResponse,
  MemoryConfig,
  MemoryRecallSample,
} from "./types";

export const MemoryConfigSchema = z.object({
  workspace_id: z.string().optional().default(""),
  enabled: z.boolean().optional().default(false),
  primary_provider: z.string().optional().default(""),
  shadow_provider: z.string().nullable().optional(),
  read_mode: z.string().optional().default("primary"),
  provider_settings: z.record(z.string(), z.unknown()).optional().default({}),
  created_at: z.string().optional(),
  updated_at: z.string().optional(),
}).loose();

export const EMPTY_MEMORY_CONFIG: MemoryConfig = {
  workspace_id: "",
  enabled: false,
  primary_provider: "",
  shadow_provider: null,
  read_mode: "primary",
  provider_settings: {},
};

export const MemoryRecallSampleSchema = z.object({
  id: z.string(),
  workspace_id: z.string().optional().default(""),
  project_id: z.string().nullable().optional(),
  agent_id: z.string().nullable().optional(),
  issue_id: z.string().nullable().optional(),
  task_id: z.string().nullable().optional(),
  provider: z.string(),
  read_mode: z.string().optional().default(""),
  recall_correlation_id: z.string().optional().default(""),
  query: z.string().optional().default(""),
  results: z.array(z.unknown()).optional().default([]),
  provenance: z.record(z.string(), z.unknown()).optional().default({}),
  sampled_at: z.string().optional().default(""),
}).loose();

export const EMPTY_MEMORY_RECALL_SAMPLE: MemoryRecallSample = {
  id: "",
  workspace_id: "",
  provider: "",
  read_mode: "",
  recall_correlation_id: "",
  query: "",
  results: [],
  provenance: {},
  sampled_at: "",
};

export const ListMemoryRecallSamplesResponseSchema = z.object({
  samples: z.array(MemoryRecallSampleSchema).optional().default([]),
}).loose();

export const EMPTY_LIST_MEMORY_RECALL_SAMPLES_RESPONSE: ListMemoryRecallSamplesResponse = {
  samples: [],
};
