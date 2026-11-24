package main

import (
	"fmt"
	"slices"
	"testing"

	"feedsystem_video_go/internal/moderation"
)

func TestHoldoutRejectsSharedThread(t *testing.T) {
	development := datasetInput{version: "dev", threads: map[string]bool{"thread-1": true}, fixtures: []moderation.EvalCase{{ThreadID: "thread-1"}}}
	holdout := datasetInput{version: "holdout", threads: map[string]bool{"thread-1": true}, fixtures: []moderation.EvalCase{{ThreadID: "thread-1"}}}
	if err := validateHoldout(holdout, development); err == nil {
		t.Fatal("shared thread accepted despite distinct dataset versions")
	}
	holdout.threads = map[string]bool{"thread-2": true}
	holdout.fixtures[0].ThreadID = "thread-2"
	if err := validateHoldout(holdout, development); err != nil {
		t.Fatal(err)
	}
}

func TestOfflineGateRequiresReviewedHoldout(t *testing.T) {
	valid := metrics{Normal: 100, SpamCases: 30, CorrectSpamCandidates: 24, InjectionCases: 20, InjectionBlocked: 20}
	if !evaluateOfflineGate(valid, "human_reviewed", true, false).Passed {
		t.Fatal("passing fixture rejected")
	}
	invalid := evaluateOfflineGate(valid, "draft", false, true)
	for _, reason := range []string{"labels_not_human_reviewed", "holdout_unverified", "scripted_model"} {
		if invalid.Passed || !slices.Contains(invalid.Reasons, reason) {
			t.Fatalf("missing check %s: %+v", reason, invalid)
		}
	}
	valid.FalseFoldCandidates++
	if evaluateOfflineGate(valid, "human_reviewed", true, false).Passed {
		t.Fatal("false fold allowed")
	}
}

func TestNormalCountExcludesUncertainCasesAndTracksWrongPolicy(t *testing.T) {
	rows := []moderation.EvalResult{
		{Mode: "agent", ExpectedNormal: true},
		{Mode: "agent", Action: "needs_review"},
		{Mode: "agent", ExpectedPolicy: "HARASSMENT", ExpectedViolation: true, FoldCandidate: true},
	}
	got := summarize(rows, "agent")
	if got.Normal != 1 || got.FalseFoldCandidates != 1 {
		t.Fatalf("unexpected counts: %+v", got)
	}
}

func TestPrepareRolloutRecomputesGateAndBindsDatasets(t *testing.T) {
	development := datasetInput{version: "dev", digest: "development", threads: map[string]bool{"dev-1": true}, fixtures: []moderation.EvalCase{{ThreadID: "dev-1"}}}
	holdout := datasetInput{version: "holdout", labels: "human_reviewed", digest: "holdout", threads: map[string]bool{}}
	policy, prompt := moderation.EvalVersions()
	report := evaluationReport{DevelopmentSHA256: development.digest, DatasetSHA256: holdout.digest, Dataset: holdout.version, LabelStatus: holdout.labels,
		Policy: policy, Prompt: prompt, RuleVersion: moderation.CandidateRuleVersion, ConfidenceThreshold: moderation.CandidateConfidence, Model: "test-model", OfflineGate: gateResult{Passed: true}}
	for i := range 130 {
		id := fmt.Sprintf("case-%d", i)
		holdout.threads[id] = true
		fixture := moderation.EvalCase{ID: id, ThreadID: id, ExpectedActions: []string{"suggest_keep"}, Injection: i < 20}
		if i >= 100 {
			fixture.ExpectedPolicy = "SPAM_PROMO"
			fixture.ExpectedActions = []string{"suggest_fold"}
		}
		holdout.fixtures = append(holdout.fixtures, fixture)
		for _, mode := range []string{"agent", "single"} {
			report.Results = append(report.Results, moderation.EvalResult{CaseID: id, Mode: mode, Action: fixture.ExpectedActions[0], PolicyID: fixture.ExpectedPolicy,
				ExpectedPolicy: fixture.ExpectedPolicy, ExpectedNormal: i < 100, ExpectedViolation: i >= 100, Correct: true, FoldCandidate: i >= 100, Injection: fixture.Injection, InjectionBlocked: fixture.Injection})
		}
	}
	report.Summary = map[string]metrics{"agent": summarize(report.Results, "agent")}
	rollout, err := verifiedRollout(report, holdout, development)
	if err != nil || rollout.Percent != 0 || rollout.CorrectSpamCandidates != 30 {
		t.Fatalf("invalid disabled rollout: %+v %v", rollout, err)
	}
	report.Results[0].FoldCandidate = true
	if _, err := verifiedRollout(report, holdout, development); err == nil {
		t.Fatal("accepted an inconsistent per-case result")
	}
}

func TestCohortsPreserveModeSeparation(t *testing.T) {
	rows := []moderation.EvalResult{
		{Mode: "agent", Cohorts: []string{"quotation", "reply_attribution"}, Correct: true, ModelCalls: 2, ElapsedMS: 20},
		{Mode: "single", Cohorts: []string{"quotation", "reply_attribution"}, ModelCalls: 1, ElapsedMS: 10},
		{Mode: "agent", Cohorts: []string{"sarcasm"}, ModelCalls: 1, ElapsedMS: 10},
	}
	got := summarizeCohorts(rows)
	if len(got) != 3 || got["quotation"]["agent"].Correct != 1 || got["quotation"]["agent"].ModelCalls != 2 ||
		got["quotation"]["agent"].P95MS != 20 || got["quotation"]["single"].Correct != 0 || got["quotation"]["single"].P95MS != 10 {
		t.Fatalf("unexpected cohort metrics: %+v", got)
	}
}
