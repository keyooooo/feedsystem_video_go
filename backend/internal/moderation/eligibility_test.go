package moderation

import "testing"

func TestFoldCandidate(t *testing.T) {
	tests := []struct {
		name, text   string
		confidence   float64
		needsContext bool
		parent       bool
		want         bool
	}{
		{"explicit promotion", "加群领返现，群号见主页", 0.97, false, false, true},
		{"quoted promotion", "他说“加群领返现，群号见主页”", 0.99, false, false, false},
		{"warning", "别信加群领返现，群号见主页", 0.99, false, false, false},
		{"unverified contact", "加群领返现 [链接]", 0.99, false, false, false},
		{"no solicitation", "返现活动详情，群号见主页", 0.99, false, false, false},
		{"confidence below threshold", "加群领返现，群号见主页", 0.94, false, false, false},
		{"reply missing parent", "加群领返现，群号见主页", 0.99, true, false, false},
		{"reply with cited parent", "加群领返现，群号见主页", 0.99, true, true, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := Snapshot{VerifiedChannel: hasOriginalChannel(test.text), NeedsContext: test.needsContext, Items: []Evidence{{ID: "target:1", Status: "ok", Text: test.text}}}
			suggestion := Suggestion{Action: "suggest_fold", PolicyID: "SPAM_PROMO", Confidence: &test.confidence, Reason: "营销", EvidenceIDs: []string{"target:1"}}
			if !test.needsContext {
				snapshot.Items = append(snapshot.Items, Evidence{ID: "parent:1", Status: "empty"})
			}
			if test.parent {
				snapshot.Items = append(snapshot.Items, Evidence{ID: "parent:1", Status: "ok", Text: "评论上下文"})
				suggestion.EvidenceIDs = append(suggestion.EvidenceIDs, "parent:1")
			}
			if got := FoldCandidate(snapshot, suggestion); got != test.want {
				t.Fatalf("candidate=%t, want %t", got, test.want)
			}
		})
	}
}
