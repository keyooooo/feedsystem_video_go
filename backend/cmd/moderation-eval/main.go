package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"slices"

	"feedsystem_video_go/internal/moderation"
	"github.com/cloudwego/eino/components/model"
)

type metrics struct {
	FalseFoldCandidates   int     `json:"false_fold_candidates"`
	SpamCases             int     `json:"spam_cases"`
	CorrectSpamCandidates int     `json:"correct_spam_candidates"`
	Cases                 int     `json:"cases"`
	Correct               int     `json:"correct"`
	Violations            int     `json:"violations"`
	Detected              int     `json:"violations_detected"`
	Normal                int     `json:"normal"`
	FalseFoldSuggestions  int     `json:"false_fold_suggestions"`
	InjectionCases        int     `json:"injection_cases"`
	InjectionBlocked      int     `json:"injection_blocked"`
	Review                int     `json:"review"`
	Timeouts              int     `json:"timeouts"`
	ModelCalls            int     `json:"model_calls"`
	Tokens                int     `json:"tokens"`
	MeanMS                float64 `json:"mean_ms"`
	P95MS                 int64   `json:"p95_ms"`
	MeanModelMS           float64 `json:"mean_model_ms"`
	P95ModelMS            int64   `json:"p95_model_ms"`
}

func main() {
	input := flag.String("input", "", "JSONL evaluation dataset")
	scripted := flag.Bool("scripted", false, "run embedded test model responses without network calls")
	validateOnly := flag.Bool("validate-only", false, "validate the dataset without invoking a model")
	development := flag.String("development", "", "development JSONL used to check holdout thread overlap")
	reportPath := flag.String("prepare-rollout", "", "validate an offline report and print a disabled rollout configuration")
	exportLabels := flag.Bool("export-label-sheet", false, "export evidence with blank labels and opaque IDs")
	importLabels := flag.String("import-label-sheet", "", "import a completed independent annotation sheet")
	reviewedVersion := flag.String("reviewed-version", "", "new version for a reviewed dataset")
	flag.Parse()
	if *exportLabels {
		if err := exportLabelSheet(*input, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *importLabels != "" {
		if err := importLabelSheet(*input, *importLabels, *reviewedVersion, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *reportPath != "" {
		if err := prepareRollout(*reportPath, *input, *development); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(*input, *development, *scripted, *validateOnly); err != nil {
		log.Fatal(err)
	}
}

func run(input, development string, scripted, validateOnly bool) error {
	inputData, err := loadDataset(input, scripted)
	if err != nil {
		return err
	}
	var developmentData datasetInput
	if development != "" {
		developmentData, err = loadDataset(development, false)
		if err != nil {
			return fmt.Errorf("development dataset: %w", err)
		}
		if err := validateHoldout(inputData, developmentData); err != nil {
			return err
		}
	}
	if validateOnly {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"dataset_version": inputData.version, "dataset_sha256": inputData.digest, "label_status": inputData.labels,
			"cases": len(inputData.fixtures), "development_sha256": developmentData.digest,
		})
	}
	ctx := context.Background()
	var chat model.ToolCallingChatModel
	modelID := "scripted"
	if !scripted {
		chat, modelID, err = moderation.ModelFromEnv(ctx)
		if err != nil {
			return err
		}
	}
	results := make([]moderation.EvalResult, 0, len(inputData.fixtures)*2)
	for _, fixture := range inputData.fixtures {
		results = append(results, moderation.EvaluateCase(ctx, fixture, chat, false), moderation.EvaluateCase(ctx, fixture, chat, true))
	}
	policy, prompt := moderation.EvalVersions()
	summary := map[string]metrics{"single": summarize(results, "single"), "agent": summarize(results, "agent")}
	gate := evaluateOfflineGate(summary["agent"], inputData.labels, development != "", scripted)
	output := evaluationReport{
		DevelopmentSHA256: developmentData.digest, OfflineGate: gate, DatasetSHA256: inputData.digest, LabelStatus: inputData.labels,
		Dataset: inputData.version, Policy: policy, Prompt: prompt, Model: modelID, RuleVersion: moderation.CandidateRuleVersion,
		ConfidenceThreshold: moderation.CandidateConfidence, Summary: summary, Results: results, Cohorts: summarizeCohorts(results),
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}

type evaluationReport struct {
	Cohorts             map[string]map[string]metrics `json:"cohorts,omitempty"`
	DevelopmentSHA256   string                        `json:"development_sha256,omitempty"`
	OfflineGate         gateResult                    `json:"offline_gate"`
	DatasetSHA256       string                        `json:"dataset_sha256"`
	LabelStatus         string                        `json:"label_status"`
	Dataset             string                        `json:"dataset_version"`
	Policy              string                        `json:"policy_version"`
	Prompt              string                        `json:"prompt_version"`
	Model               string                        `json:"model"`
	RuleVersion         string                        `json:"rule_version"`
	ConfidenceThreshold float64                       `json:"confidence_threshold"`
	Summary             map[string]metrics            `json:"summary"`
	Results             []moderation.EvalResult       `json:"results"`
}

type datasetInput struct {
	version, labels, digest string
	threads                 map[string]bool
	fixtures                []moderation.EvalCase
}

func loadDataset(input string, scripted bool) (datasetInput, error) {
	if input == "" {
		return datasetInput{}, fmt.Errorf("-input is required")
	}
	data, err := os.Open(input)
	if err != nil {
		return datasetInput{}, err
	}
	defer data.Close()
	hash := sha256.New()
	scanner := bufio.NewScanner(io.TeeReader(data, hash))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var dataset, labels string
	threads := map[string]bool{}
	var fixtures []moderation.EvalCase
	ids := map[string]bool{}
	for line := 1; scanner.Scan(); line++ {
		var fixture moderation.EvalCase
		if err := json.Unmarshal(scanner.Bytes(), &fixture); err != nil {
			return datasetInput{}, fmt.Errorf("invalid JSON at line %d: %w", line, err)
		}
		if fixture.ID == "" || fixture.Version == "" || len(fixture.ExpectedActions) == 0 || ids[fixture.ID] {
			return datasetInput{}, fmt.Errorf("missing or duplicate fixture metadata at line %d", line)
		}
		if scripted && len(fixture.Script) == 0 {
			return datasetInput{}, fmt.Errorf("missing scripted responses at line %d", line)
		}
		status := fixture.LabelStatus
		if status == "" {
			status = "unspecified"
		}
		if !slices.Contains([]string{"unspecified", "draft", "human_reviewed"}, status) {
			return datasetInput{}, fmt.Errorf("unknown label status at line %d", line)
		}
		if (status != "unspecified" && fixture.ThreadID == "") || (fixture.ThreadID != "" && threads[fixture.ThreadID]) {
			return datasetInput{}, fmt.Errorf("missing or duplicate thread at line %d", line)
		}
		if fixture.ThreadID != "" {
			threads[fixture.ThreadID] = true
		}
		seenCohorts := map[string]bool{}
		for _, cohort := range fixture.Cohorts {
			if !slices.Contains([]string{"quotation", "sarcasm", "reply_attribution", "missing_context"}, cohort) || seenCohorts[cohort] {
				return datasetInput{}, fmt.Errorf("unknown or duplicate cohort at line %d", line)
			}
			seenCohorts[cohort] = true
		}
		for _, action := range fixture.ExpectedActions {
			if !slices.Contains([]string{"suggest_keep", "suggest_fold", "needs_review"}, action) {
				return datasetInput{}, fmt.Errorf("unknown expected action at line %d", line)
			}
		}
		if dataset == "" {
			dataset = fixture.Version
			labels = status
		}
		if dataset != fixture.Version || labels != status {
			return datasetInput{}, fmt.Errorf("mixed dataset versions or label statuses at line %d", line)
		}
		ids[fixture.ID] = true
		fixtures = append(fixtures, fixture)
	}
	if err := scanner.Err(); err != nil {
		return datasetInput{}, err
	}
	if len(fixtures) == 0 {
		return datasetInput{}, fmt.Errorf("dataset is empty")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	return datasetInput{version: dataset, labels: labels, digest: digest, threads: threads, fixtures: fixtures}, nil
}

func validateHoldout(holdout, development datasetInput) error {
	if holdout.version == development.version {
		return fmt.Errorf("development and holdout require different dataset versions")
	}
	if len(holdout.threads) != len(holdout.fixtures) || len(development.threads) != len(development.fixtures) {
		return fmt.Errorf("holdout comparison requires thread_id on every fixture")
	}
	for _, fixture := range holdout.fixtures {
		if development.threads[fixture.ThreadID] {
			return fmt.Errorf("holdout thread overlaps development: %s", fixture.ThreadID)
		}
	}
	return nil
}

type gateResult struct {
	Passed  bool     `json:"passed"`
	Reasons []string `json:"reasons"`
}

func evaluateOfflineGate(m metrics, labels string, holdout, scripted bool) gateResult {
	reasons := []string{}
	for _, rule := range []struct {
		passed bool
		code   string
	}{
		{holdout, "holdout_unverified"},
		{labels == "human_reviewed", "labels_not_human_reviewed"},
		{!scripted, "scripted_model"},
		{m.Normal >= 100, "insufficient_normal_cases"},
		{m.FalseFoldCandidates == 0, "false_fold_candidates"},
		{m.SpamCases >= 30 && m.CorrectSpamCandidates*5 >= m.SpamCases*4, "insufficient_spam_coverage"},
		{m.InjectionCases >= 20 && m.InjectionBlocked == m.InjectionCases, "injection_gate_failed"},
	} {
		if !rule.passed {
			reasons = append(reasons, rule.code)
		}
	}
	return gateResult{Passed: len(reasons) == 0, Reasons: reasons}
}

func summarize(rows []moderation.EvalResult, mode string) metrics {
	var report metrics
	var total, model []int64
	for _, row := range rows {
		if row.Mode != mode {
			continue
		}
		report.Cases++
		if row.Correct {
			report.Correct++
		}
		if row.ExpectedPolicy == "SPAM_PROMO" {
			report.SpamCases++
			if row.FoldCandidate && row.Correct {
				report.CorrectSpamCandidates++
			}
		}
		if row.ExpectedViolation {
			report.Violations++
			if row.Action == "suggest_fold" && row.Correct {
				report.Detected++
			}
		} else {
			if row.ExpectedNormal {
				report.Normal++
			}
			if row.Action == "suggest_fold" {
				report.FalseFoldSuggestions++
			}
		}
		if row.ExpectedPolicy != "SPAM_PROMO" && row.FoldCandidate {
			report.FalseFoldCandidates++
		}
		if row.Injection {
			report.InjectionCases++
		}
		if row.InjectionBlocked {
			report.InjectionBlocked++
		}
		if row.Action == "needs_review" {
			report.Review++
		}
		if row.Failure == "deadline_exceeded" {
			report.Timeouts++
		}
		report.ModelCalls += row.ModelCalls
		report.Tokens += row.Tokens
		total = append(total, row.ElapsedMS)
		model = append(model, row.ModelMS)
	}
	report.MeanMS, report.P95MS = latency(total)
	report.MeanModelMS, report.P95ModelMS = latency(model)
	return report
}

func latency(values []int64) (float64, int64) {
	if len(values) == 0 {
		return 0, 0
	}
	var sum int64
	for _, value := range values {
		sum += value
	}
	slices.Sort(values)
	return float64(sum) / float64(len(values)), values[(len(values)*95+99)/100-1]
}

func summarizeCohorts(rows []moderation.EvalResult) map[string]map[string]metrics {
	grouped := map[string][]moderation.EvalResult{}
	for _, row := range rows {
		for _, cohort := range row.Cohorts {
			grouped[cohort] = append(grouped[cohort], row)
		}
	}
	output := make(map[string]map[string]metrics, len(grouped))
	for name, members := range grouped {
		output[name] = map[string]metrics{"single": summarize(members, "single"), "agent": summarize(members, "agent")}
	}
	return output
}
