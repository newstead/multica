import type { AutopilotRun } from "@multica/core/types";

export type AutopilotRunMessage =
  | { kind: "issue" }
  | { kind: "skip"; text: string }
  | { kind: "error"; text: string }
  | null;

type RunMessageInput = Pick<AutopilotRun, "issue_id" | "failure_reason" | "reason_code">;

export function autopilotRunMessage(
  run: RunMessageInput,
  skippedOverlapText: string,
): AutopilotRunMessage {
  if (run.issue_id) {
    return { kind: "issue" };
  }
  if (run.reason_code === "skipped_overlap" || run.failure_reason === "skipped_overlap") {
    return { kind: "skip", text: skippedOverlapText };
  }
  if (run.failure_reason) {
    return { kind: "error", text: run.failure_reason };
  }
  return null;
}
