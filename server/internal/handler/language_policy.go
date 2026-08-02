package handler

import (
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// supportedLanguagePolicies is the allow-list of BCP-47 values a language
// policy may take. NULL (unset) inherits from the next level up; anything
// outside this set is rejected at write time and treated as unset at
// resolution time. This is deliberately separate from the UI locale
// (user.language) and the agent programming-language list (language_codes),
// which stay unrelated to the runtime language policy.
//
// The canonical list lives in packages/core/types/language-policy.ts
// (AGENT_LANGUAGE_POLICY_VALUES); TestSupportedLanguagePoliciesMatchCore
// fails if this Go list drifts from the core list.
var supportedLanguagePolicies = map[string]bool{
	"ru":      true,
	"en":      true,
	"zh-Hans": true,
	"ja":      true,
	"ko":      true,
}

// languagePolicyValues lists the supported codes in stable display order. It
// mirrors supportedLanguagePolicies and is used to build the write-time
// rejection message so the hint cannot drift from the allow-list.
var languagePolicyValues = []string{"ru", "en", "zh-Hans", "ja", "ko"}

// languagePolicyHint is the rejection message for unsupported policy codes.
var languagePolicyHint = "language_policy must be one of: " + strings.Join(languagePolicyValues, ", ")

// validateLanguagePolicy reports whether v is an accepted policy value.
func validateLanguagePolicy(v string) bool {
	return supportedLanguagePolicies[strings.TrimSpace(v)]
}

// normaliseLanguagePolicy converts an API *string into the column value.
// nil and empty/whitespace map to "unset" (invalid pgtype.Text{}); any
// other value must already have passed validateLanguagePolicy.
func normaliseLanguagePolicy(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{}
	}
	s := strings.TrimSpace(*v)
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// resolveLanguagePolicy picks the effective policy for a task using the
// agent > project > workspace precedence. Unset or unknown values inherit
// from the next level up; when nothing resolves, the empty string keeps the
// previous no-policy behavior (the brief's Language Policy section is
// omitted entirely).
func resolveLanguagePolicy(agent, project, workspace pgtype.Text) string {
	for _, v := range []pgtype.Text{agent, project, workspace} {
		if v.Valid && supportedLanguagePolicies[v.String] {
			return v.String
		}
	}
	return ""
}
