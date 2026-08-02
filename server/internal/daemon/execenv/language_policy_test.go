package execenv

import (
	"strings"
	"testing"
)

// TestBriefLanguagePolicySection pins the ROL language-policy contract: when
// a policy resolves, every task kind renders the `## Language Policy` section
// naming the language; the literal-keep rule is part of the same section.
func TestBriefLanguagePolicySection(t *testing.T) {
	t.Parallel()

	for name, ctx := range deliveryInvariantFixtures() {
		ctx.LanguagePolicy = "ru"
		out := buildMetaSkillContent("claude", ctx)
		if !strings.Contains(out, "## Language Policy") {
			t.Errorf("kind=%s: brief missing Language Policy section with policy set", name)
		}
		for _, want := range []string{"**Russian**", "(`ru`)"} {
			if !strings.Contains(out, want) {
				t.Errorf("kind=%s: brief does not name the policy language (%q)", name, want)
			}
		}
	}
}

// TestBriefLanguagePolicyOmittedWithoutPolicy locks the safe fallback: no
// policy anywhere means the section (and the word "Language Policy") must not
// appear, keeping the brief byte-identical to the previous behavior for
// projects without a policy.
func TestBriefLanguagePolicyOmittedWithoutPolicy(t *testing.T) {
	t.Parallel()

	for name, ctx := range deliveryInvariantFixtures() {
		out := buildMetaSkillContent("claude", ctx)
		if strings.Contains(out, "## Language Policy") {
			t.Errorf("kind=%s: brief must not render Language Policy without a policy", name)
		}
	}
}

// TestBriefLanguagePolicyLiteralExemptions locks the hard-rule wording for an
// issue task: the policy applies to every agent-authored surface while
// commands, paths, ids, code, mentions, URLs and CLI output stay verbatim.
func TestBriefLanguagePolicyLiteralExemptions(t *testing.T) {
	t.Parallel()

	ctx := deliveryInvariantFixtures()["comment"]
	ctx.LanguagePolicy = "ru"
	out := buildMetaSkillContent("claude", ctx)

	for _, want := range []string{
		"must be written in **Russian** (`ru`)",
		"issue comments (results and replies)",
		"handoff/delegation notes",
		"blocker reports",
		"autopilot summaries",
		"issue titles/descriptions you create",
		"Commands, file paths, enum/status/model IDs",
		"never translate them",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("brief missing language-policy phrase %q", want)
		}
	}
}

// TestBriefLanguagePolicyEnglish locks the en policy renders as English.
func TestBriefLanguagePolicyEnglish(t *testing.T) {
	t.Parallel()

	ctx := deliveryInvariantFixtures()["autopilot"]
	ctx.LanguagePolicy = "en"
	out := buildMetaSkillContent("claude", ctx)
	if !strings.Contains(out, "**English** (`en`)") {
		t.Errorf("brief does not render English policy: %s", out)
	}
}
