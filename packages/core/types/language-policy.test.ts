import { describe, expect, it } from "vitest";
import {
  AGENT_LANGUAGE_POLICY_VALUES,
  isAgentLanguagePolicy,
} from "./language-policy";

// The supported-code list is the single source of truth for both the UI
// select and the server allow-list/CHECK constraints. Pinning it here makes
// any deliberate change explicit; the Go side guards the same contract in
// server/internal/handler/language_policy_test.go.
describe("AGENT_LANGUAGE_POLICY_VALUES", () => {
  it("is the canonical supported list shared by UI and server", () => {
    expect(AGENT_LANGUAGE_POLICY_VALUES).toEqual([
      "ru",
      "en",
      "zh-Hans",
      "ja",
      "ko",
    ]);
  });

  it("accepts every supported code and rejects anything else", () => {
    for (const code of AGENT_LANGUAGE_POLICY_VALUES) {
      expect(isAgentLanguagePolicy(code)).toBe(true);
    }
    for (const value of ["", "RU", "fr", "zh-hans", "ru-ru", null, undefined]) {
      expect(isAgentLanguagePolicy(value)).toBe(false);
    }
  });
});
