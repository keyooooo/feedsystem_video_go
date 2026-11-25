package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"

	"feedsystem_video_go/internal/moderation"
)

type labelSheet struct {
	DatasetVersion string      `json:"dataset_version"`
	DatasetSHA256  string      `json:"dataset_sha256"`
	ReviewBatch    string      `json:"review_batch"`
	Items          []labelItem `json:"items"`
}

type labelItem struct {
	ReviewID         string                           `json:"review_id"`
	Snapshot         moderation.Snapshot              `json:"snapshot"`
	Thread           map[string][]moderation.Evidence `json:"thread,omitempty"`
	ExpectedActions  []string                         `json:"expected_actions"`
	ExpectedPolicy   string                           `json:"expected_policy"`
	RequiredEvidence []string                         `json:"required_evidence"`
	Injection        *bool                            `json:"injection_tag"`
}

func reviewID(dataset datasetInput, fixture moderation.EvalCase) string {
	digest := sha256.Sum256([]byte(dataset.digest + "\x00" + fixture.ID))
	return hex.EncodeToString(digest[:])
}

func makeLabelSheet(dataset datasetInput) labelSheet {
	sheet := labelSheet{DatasetVersion: dataset.version, DatasetSHA256: dataset.digest, Items: make([]labelItem, 0, len(dataset.fixtures))}
	for _, fixture := range dataset.fixtures {
		snapshot := fixture.Snapshot
		snapshot.Tools = nil
		sheet.Items = append(sheet.Items, labelItem{
			ReviewID: reviewID(dataset, fixture), Snapshot: snapshot, Thread: fixture.Thread,
		})
	}
	slices.SortFunc(sheet.Items, func(a, b labelItem) int { return strings.Compare(a.ReviewID, b.ReviewID) })
	return sheet
}

func exportLabelSheet(input string, out io.Writer) error {
	dataset, err := loadDataset(input, false)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(makeLabelSheet(dataset))
}

func reviewedFixtures(dataset datasetInput, sheet labelSheet, version string) ([]moderation.EvalCase, error) {
	if sheet.DatasetVersion != dataset.version || sheet.DatasetSHA256 != dataset.digest {
		return nil, fmt.Errorf("annotation sheet belongs to a different source dataset")
	}
	if strings.TrimSpace(sheet.ReviewBatch) == "" || strings.TrimSpace(version) == "" || version == dataset.version {
		return nil, fmt.Errorf("review_batch and a new dataset version are required")
	}
	original := make(map[string]labelItem, len(dataset.fixtures))
	for _, item := range makeLabelSheet(dataset).Items {
		original[item.ReviewID] = item
	}
	labels := make(map[string]labelItem, len(sheet.Items))
	for _, item := range sheet.Items {
		if item.ReviewID == "" || labels[item.ReviewID].ReviewID != "" {
			return nil, fmt.Errorf("missing or duplicate review ID")
		}
		source, exists := original[item.ReviewID]
		if !exists || !reflect.DeepEqual(item.Snapshot, source.Snapshot) || !reflect.DeepEqual(item.Thread, source.Thread) {
			return nil, fmt.Errorf("annotation evidence differs from the source for review ID %s", item.ReviewID)
		}
		labels[item.ReviewID] = item
	}
	if len(labels) != len(dataset.fixtures) {
		return nil, fmt.Errorf("every sample requires exactly one reviewed label")
	}
	output := make([]moderation.EvalCase, 0, len(dataset.fixtures))
	for _, fixture := range dataset.fixtures {
		id := reviewID(dataset, fixture)
		item, ok := labels[id]
		if !ok || len(item.ExpectedActions) == 0 || item.Injection == nil {
			return nil, fmt.Errorf("missing annotation for review ID %s", id)
		}
		if item.ExpectedPolicy != "" && item.ExpectedPolicy != "SPAM_PROMO" && item.ExpectedPolicy != "HARASSMENT" {
			return nil, fmt.Errorf("invalid policy on review ID %s", id)
		}
		for _, action := range item.ExpectedActions {
			switch action {
			case "suggest_keep", "needs_review":
			case "suggest_fold":
				if item.ExpectedPolicy == "" {
					return nil, fmt.Errorf("fold label requires a policy on review ID %s", id)
				}
			default:
				return nil, fmt.Errorf("invalid expected action on review ID %s", id)
			}
		}
		ids := map[string]bool{}
		for _, evidence := range fixture.Snapshot.Items {
			ids[evidence.ID] = evidence.Status == "ok"
		}
		for _, items := range fixture.Thread {
			for _, evidence := range items {
				ids[evidence.ID] = evidence.Status == "ok"
			}
		}
		for _, id := range item.RequiredEvidence {
			if !ids[id] {
				return nil, fmt.Errorf("unknown or unavailable evidence ID %s", id)
			}
		}
		fixture.Version = version
		fixture.LabelStatus = "human_reviewed"
		fixture.ReviewBatch = strings.TrimSpace(sheet.ReviewBatch)
		fixture.ExpectedActions = item.ExpectedActions
		fixture.ExpectedPolicy = item.ExpectedPolicy
		fixture.RequiredEvidence = item.RequiredEvidence
		fixture.Injection = *item.Injection
		fixture.Script = nil
		fixture.AgentScript = nil
		output = append(output, fixture)
	}
	return output, nil
}

func importLabelSheet(input, path, version string, out io.Writer) error {
	dataset, err := loadDataset(input, false)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var sheet labelSheet
	if err := json.Unmarshal(raw, &sheet); err != nil {
		return err
	}
	output, err := reviewedFixtures(dataset, sheet, version)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	for _, fixture := range output {
		if err := encoder.Encode(fixture); err != nil {
			return err
		}
	}
	return nil
}
