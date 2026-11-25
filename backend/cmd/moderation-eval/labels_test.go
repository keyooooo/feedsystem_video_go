package main

import (
	"encoding/json"
	"strings"
	"testing"

	"feedsystem_video_go/internal/moderation"
)

func TestLabelSheetBlindsAndAuthenticatesSourceEvidence(t *testing.T) {
	dataset := datasetInput{version: "draft", digest: "input-digest", fixtures: []moderation.EvalCase{{
		ID: "promo-hidden-answer", ThreadID: "thread-1", ExpectedPolicy: "SPAM_PROMO", ExpectedActions: []string{"suggest_fold"}, Injection: true,
		Snapshot: moderation.Snapshot{Items: []moderation.Evidence{{ID: "target:1", Status: "ok", Text: "示例文本"}}},
	}}}
	sheet := makeLabelSheet(dataset)
	raw, err := json.Marshal(sheet)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"promo-hidden-answer", "SPAM_PROMO", "suggest_fold", "thread-1"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("unblinded field %q", forbidden)
		}
	}
	if _, err := reviewedFixtures(dataset, sheet, "v2"); err == nil {
		t.Fatal("blank sheet accepted")
	}
	sheet.ReviewBatch = "audit-1"
	sheet.Items[0].ExpectedActions = []string{"suggest_keep"}
	sheet.Items[0].RequiredEvidence = []string{"target:1"}
	injection := false
	sheet.Items[0].Injection = &injection
	output, err := reviewedFixtures(dataset, sheet, "v2")
	if err != nil || len(output) != 1 || output[0].LabelStatus != "human_reviewed" || output[0].ReviewBatch != "audit-1" || output[0].ExpectedPolicy != "" || output[0].Injection {
		t.Fatalf("reviewed labels not applied: %+v %v", output, err)
	}
	sheet.Items[0].RequiredEvidence = []string{"target:999"}
	if _, err := reviewedFixtures(dataset, sheet, "v2"); err == nil {
		t.Fatal("invalid evidence citation accepted")
	}
	sheet.Items[0].RequiredEvidence = nil
	sheet.DatasetSHA256 = "other"
	if _, err := reviewedFixtures(dataset, sheet, "v2"); err == nil {
		t.Fatal("different source accepted")
	}
	sheet.DatasetSHA256 = dataset.digest
	sheet.Items[0].Snapshot.Items = append([]moderation.Evidence(nil), sheet.Items[0].Snapshot.Items...)
	sheet.Items[0].Snapshot.Items[0].Text = "changed"
	if _, err := reviewedFixtures(dataset, sheet, "v2"); err == nil {
		t.Fatal("modified evidence accepted")
	}
}
