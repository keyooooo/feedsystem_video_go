package moderation

import (
	"strings"
	"testing"
)

func TestRolloutRequiresMatchingEvidenceAndSampleThresholds(t *testing.T) {
	run := ShadowRun{Model: "test-model", PolicyVersion: policyVersion, PromptVersion: promptVersion, RuleVersion: CandidateRuleVersion}
	approved := Rollout{Model: run.Model, PolicyVersion: run.PolicyVersion, PromptVersion: run.PromptVersion, RuleVersion: run.RuleVersion, DatasetVersion: "holdout-test",
		DatasetSHA256: strings.Repeat("1", 64), DevelopmentSHA256: strings.Repeat("2", 64), ReportSHA256: strings.Repeat("3", 64), Percent: 5, NormalCases: 100, SpamCases: 30, CorrectSpamCandidates: 24, InjectionCases: 20, InjectionBlocked: 20}
	if !approved.allows(run, 104) || approved.allows(run, 105) {
		t.Fatal("rollout bucket was not stable")
	}
	for _, test := range []struct {
		name   string
		change func(*Rollout)
	}{
		{"disabled", func(r *Rollout) { r.Percent = 0 }},
		{"different model", func(r *Rollout) { r.Model = "other" }},
		{"different rule", func(r *Rollout) { r.RuleVersion = "old" }},
		{"normal false positive", func(r *Rollout) { r.FalseCandidates = 1 }},
		{"insufficient spam recall", func(r *Rollout) { r.CorrectSpamCandidates = 23 }},
		{"invalid spam count", func(r *Rollout) { r.CorrectSpamCandidates = 31 }},
		{"injection missed", func(r *Rollout) { r.InjectionBlocked = 19 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := approved
			test.change(&candidate)
			if candidate.allows(run, 104) {
				t.Fatal("invalid rollout accepted")
			}
		})
	}
}
