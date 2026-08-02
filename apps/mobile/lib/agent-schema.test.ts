import { describe, expect, it } from "vitest";
import { AgentSchema } from "../data/schemas";

describe("AgentSchema identity metadata", () => {
  it("defaults missing identity metadata to no badge", () => {
    const parsed = AgentSchema.parse({ id: "agent-1" });

    expect(parsed.role_code).toBeUndefined();
    expect(parsed.language_codes).toEqual([]);
  });

  it("parses identity metadata without rejecting future display codes", () => {
    const parsed = AgentSchema.parse({
      id: "agent-1",
      role_code: "BE",
      language_codes: ["GO", "PY"],
    });

    expect(parsed.role_code).toBe("BE");
    expect(parsed.language_codes).toEqual(["GO", "PY"]);
  });
});

describe("AgentSchema invocation permissions", () => {
  it("defaults missing invocation permissions to private access", () => {
    const parsed = AgentSchema.parse({ id: "agent-1" });

    expect(parsed.permission_mode).toBe("private");
    expect(parsed.invocation_targets).toEqual([]);
  });

  it("parses public invocation grants", () => {
    const parsed = AgentSchema.parse({
      id: "agent-1",
      permission_mode: "public_to",
      invocation_targets: [
        { target_type: "workspace" },
        { target_type: "member", target_id: "member-1" },
      ],
    });

    expect(parsed.permission_mode).toBe("public_to");
    expect(parsed.invocation_targets).toEqual([
      { target_type: "workspace", target_id: null },
      { target_type: "member", target_id: "member-1" },
    ]);
  });

  it("fails closed for unknown permission values", () => {
    const parsed = AgentSchema.parse({
      id: "agent-1",
      permission_mode: "future_mode",
      invocation_targets: [{ target_type: "future_target", target_id: 123 }],
    });

    expect(parsed.permission_mode).toBe("private");
    expect(parsed.invocation_targets).toEqual([
      { target_type: "team", target_id: null },
    ]);
  });
});
