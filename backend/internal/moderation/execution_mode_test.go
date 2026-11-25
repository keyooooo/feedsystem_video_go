package moderation

import "testing"

func TestParseAutoFoldMode(t *testing.T) {
	for _, value := range []string{"", "approved", "threshold", "shadow", " threshold "} {
		mode, err := ParseAutoFoldMode(value)
		if err != nil || mode == "" {
			t.Fatalf("%q: %q %v", value, mode, err)
		}
	}
	if mode, err := ParseAutoFoldMode("threshhold"); err == nil || mode != "" {
		t.Fatal("invalid mode accepted")
	}
	if autoFoldMode(nil) != AutoFoldApproved || autoFoldMode([]AutoFoldMode{"unknown"}) != AutoFoldShadow {
		t.Fatal("unsafe mode default")
	}
}

func TestThresholdCandidateUsesModelDecisionAndEvidence(t *testing.T) {
	for _, text := range []string{"加我微信，免费送福利噢，私聊我。", "加我[联系方式]，私聊免费送资源"} {
		snapshot := Snapshot{Items: []Evidence{{ID: "target:1", Status: "ok", Text: text}}}
		confidence := 0.90
		suggestion := Suggestion{Action: "suggest_fold", PolicyID: "SPAM_PROMO", Confidence: &confidence, Reason: "主动营销招揽", EvidenceIDs: []string{"target:1"}}
		if FoldCandidate(snapshot, suggestion) {
			t.Fatal("fixture unexpectedly passes the old keyword gate")
		}
		if !ThresholdFoldCandidate(snapshot, suggestion) {
			t.Fatal("valid threshold decision rejected")
		}
		confidence = 0.899
		if ThresholdFoldCandidate(snapshot, suggestion) {
			t.Fatal("below threshold accepted")
		}
		confidence = .93
		snapshot.NeedsContext = true
		if ThresholdFoldCandidate(snapshot, suggestion) {
			t.Fatal("reply without parent accepted")
		}
		snapshot.Items = append(snapshot.Items, Evidence{ID: "parent:1", Status: "ok", Text: "上文"})
		suggestion.EvidenceIDs = append(suggestion.EvidenceIDs, "parent:1")
		if !ThresholdFoldCandidate(snapshot, suggestion) {
			t.Fatal("cited parent rejected")
		}
		snapshot.TargetRemoved = true
		if ThresholdFoldCandidate(snapshot, suggestion) {
			t.Fatal("removed target accepted")
		}
	}
}
