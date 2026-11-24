package moderation

import (
	"strings"
	"testing"
)

func TestDecisionIntentContract(t *testing.T) {
	snapshot := Snapshot{Items: []Evidence{{ID: "target:1", Status: "ok", Text: "加群领红包，渠道在主页"}}}
	const fold = `{"action":"fold","policy_id":"SPAM_PROMO","confidence":0.98,"evidence_ids":["target:1"],"reason":"主动招揽导流"}`
	for _, test := range []struct {
		name string
		raw  string
		want bool
	}{
		{"fold", fold, true},
		{"review", `{"action":"needs_review","policy_id":"","confidence":0.3,"evidence_ids":[],"reason":"语境不足"}`, true},
		{"identity injection", strings.TrimSuffix(fold, "}") + `,"case_id":42}`, false},
		{"unapproved policy", strings.ReplaceAll(fold, "SPAM_PROMO", "HARASSMENT"), false},
		{"restore", strings.ReplaceAll(fold, `"fold"`, `"restore"`), false},
		{"keep deferred", strings.ReplaceAll(fold, `"fold"`, `"keep"`), false},
		{"forged citation", strings.ReplaceAll(fold, "target:1", "target:999"), false},
		{"missing confidence", strings.ReplaceAll(fold, `"confidence":0.98,`, ""), false},
		{"out of range", strings.ReplaceAll(fold, "0.98", "1.1"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseDecisionIntent(test.raw, snapshot)
			if (err == nil) != test.want {
				t.Fatalf("accepted=%v want=%v error=%v", err == nil, test.want, err)
			}
		})
	}
	snapshot.NeedsContext = true
	if _, err := parseDecisionIntent(fold, snapshot); err == nil {
		t.Fatal("fold without required context accepted")
	}
	snapshot.Items = append(snapshot.Items, Evidence{ID: "parent:1", Status: "ok"})
	if _, err := parseDecisionIntent(fold, snapshot); err == nil {
		t.Fatal("fold without context citation accepted")
	}
	withContext := strings.ReplaceAll(fold, `["target:1"]`, `["target:1","parent:1"]`)
	if _, err := parseDecisionIntent(withContext, snapshot); err != nil {
		t.Fatal(err)
	}
}
