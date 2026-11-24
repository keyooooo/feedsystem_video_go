package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"feedsystem_video_go/internal/moderation"
)

func prepareRollout(reportPath, holdoutPath, developmentPath string) error {
	holdout, err := loadDataset(holdoutPath, false)
	if err != nil {
		return err
	}
	development, err := loadDataset(developmentPath, false)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		return err
	}
	var report evaluationReport
	if err := json.Unmarshal(data, &report); err != nil {
		return err
	}
	rollout, err := verifiedRollout(report, holdout, development)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	rollout.ReportSHA256 = hex.EncodeToString(digest[:])
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(rollout)
}

func verifiedRollout(report evaluationReport, holdout, development datasetInput) (moderation.Rollout, error) {
	empty := moderation.Rollout{}
	if err := validateHoldout(holdout, development); err != nil {
		return empty, err
	}
	policy, prompt := moderation.EvalVersions()
	if holdout.labels != "human_reviewed" || report.LabelStatus != holdout.labels || report.Dataset != holdout.version ||
		report.DatasetSHA256 != holdout.digest || report.DevelopmentSHA256 != development.digest ||
		report.Policy != policy || report.Prompt != prompt || report.RuleVersion != moderation.CandidateRuleVersion ||
		report.ConfidenceThreshold != moderation.CandidateConfidence || report.Model == "" || report.Model == "scripted" {
		return empty, fmt.Errorf("report does not match the reviewed datasets and current model contract")
	}
	cases := make(map[string]moderation.EvalCase, len(holdout.fixtures))
	for _, fixture := range holdout.fixtures {
		cases[fixture.ID] = fixture
	}
	seen := make(map[string]bool, len(cases)*2)
	for _, result := range report.Results {
		fixture, ok := cases[result.CaseID]
		key := result.Mode + "/" + result.CaseID
		if !ok || seen[key] || (result.Mode != "agent" && result.Mode != "single") {
			return empty, fmt.Errorf("unexpected or duplicate report result: %s", key)
		}
		expectedNormal := fixture.ExpectedPolicy == "" && slices.Contains(fixture.ExpectedActions, "suggest_keep")
		if !slices.Equal(result.Cohorts, fixture.Cohorts) || result.ExpectedPolicy != fixture.ExpectedPolicy || result.ExpectedViolation != (fixture.ExpectedPolicy != "") || result.ExpectedNormal != expectedNormal ||
			result.Injection != fixture.Injection || result.InjectionBlocked && (!result.Injection || !result.Correct) ||
			result.FoldCandidate && (result.Action != "suggest_fold" || result.PolicyID != "SPAM_PROMO" || result.Failure != "") ||
			result.Correct && (!slices.Contains(fixture.ExpectedActions, result.Action) || result.Failure == "" && result.PolicyID != fixture.ExpectedPolicy) {
			return empty, fmt.Errorf("report result does not match labels or disposition contract: %s", key)
		}
		seen[key] = true
	}
	if len(seen) != len(cases)*2 {
		return empty, fmt.Errorf("report is missing case results")
	}
	counts := summarize(report.Results, "agent")
	gate := evaluateOfflineGate(counts, holdout.labels, true, false)
	if !gate.Passed || !report.OfflineGate.Passed || report.Summary["agent"] != counts {
		return empty, fmt.Errorf("offline gate or summary verification failed: %v", gate.Reasons)
	}
	return moderation.Rollout{
		PolicyID: "SPAM_PROMO", Model: report.Model, PolicyVersion: policy, PromptVersion: prompt, RuleVersion: report.RuleVersion,
		DatasetVersion: holdout.version, DatasetSHA256: holdout.digest, DevelopmentSHA256: development.digest,
		NormalCases: uint(counts.Normal), FalseCandidates: uint(counts.FalseFoldCandidates), SpamCases: uint(counts.SpamCases),
		CorrectSpamCandidates: uint(counts.CorrectSpamCandidates), InjectionCases: uint(counts.InjectionCases), InjectionBlocked: uint(counts.InjectionBlocked),
	}, nil
}
