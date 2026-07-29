import { describe, expect, it } from "vitest";
import {
  MemoryConfigSchema,
  MemoryRecallSamplesResponseSchema,
} from "./schemas";

describe("memory API schemas", () => {
  it("defaults missing memory config fields defensively", () => {
    const parsed = MemoryConfigSchema.parse({ workspace_id: "ws-1" });

    expect(parsed).toMatchObject({
      workspace_id: "ws-1",
      enabled: false,
      primary_provider: "",
      read_mode: "primary",
      provider_settings: {},
    });
  });

  it("keeps recall samples renderable when optional arrays and maps are absent", () => {
    const parsed = MemoryRecallSamplesResponseSchema.parse({
      samples: [{ id: "sample-1", provider: "hindsight" }],
    });

    expect(parsed.samples[0]).toMatchObject({
      id: "sample-1",
      provider: "hindsight",
      results: [],
      provenance: {},
    });
  });
});
