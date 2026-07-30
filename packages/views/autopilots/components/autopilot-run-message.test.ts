import { describe, expect, it } from "vitest";
import { autopilotRunMessage } from "./autopilot-run-message";

const label = "Previous scheduled run still active";

describe("autopilotRunMessage", () => {
  it("localizes skipped_overlap without marking it destructive", () => {
    expect(
      autopilotRunMessage(
        { issue_id: null, failure_reason: "skipped_overlap", reason_code: "skipped_overlap" },
        label,
      ),
    ).toEqual({ kind: "skip", text: label });
  });

  it("keeps unknown failure reasons destructive", () => {
    expect(
      autopilotRunMessage(
        { issue_id: null, failure_reason: "agent_error", reason_code: undefined },
        label,
      ),
    ).toEqual({ kind: "error", text: "agent_error" });
  });

  it("lets linked issues win over failure text", () => {
    expect(
      autopilotRunMessage(
        { issue_id: "issue-1", failure_reason: "skipped_overlap", reason_code: "skipped_overlap" },
        label,
      ),
    ).toEqual({ kind: "issue" });
  });
});
