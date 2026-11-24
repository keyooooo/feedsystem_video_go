package moderation

import (
	"errors"
	"math"
)

const DecisionProtocolVersion = "decision-tool-v1"

// DecisionIntent contains only model-supplied fields. Identity, target and
// execution versions are bound by the worker after validation.
type DecisionIntent struct {
	Action      string   `json:"action"`
	PolicyID    string   `json:"policy_id"`
	Confidence  *float64 `json:"confidence"`
	EvidenceIDs []string `json:"evidence_ids"`
	Reason      string   `json:"reason"`
}

func parseDecisionIntent(raw string, snapshot Snapshot) (DecisionIntent, error) {
	var intent DecisionIntent
	if len(raw) > 4096 || strictJSON(raw, &intent) != nil {
		return DecisionIntent{}, errors.New("invalid decision arguments")
	}
	if !intent.valid(snapshot) {
		return DecisionIntent{}, errors.New("invalid decision intent")
	}
	intent.Reason = evidenceText(intent.Reason)
	return intent, nil
}

func (intent DecisionIntent) valid(snapshot Snapshot) bool {
	if intent.Confidence == nil || math.IsNaN(*intent.Confidence) || math.IsInf(*intent.Confidence, 0) {
		return false
	}
	action := intent.Action
	switch action {
	case "fold":
		if intent.PolicyID != "SPAM_PROMO" {
			return false
		}
		action = "suggest_fold"
	case "needs_review":
		if intent.PolicyID != "" {
			return false
		}
	default:
		return false
	}
	suggestion := Suggestion{
		Action: action, PolicyID: intent.PolicyID, Confidence: intent.Confidence,
		EvidenceIDs: intent.EvidenceIDs, Reason: intent.Reason,
	}
	if !suggestion.valid(snapshot.Items) {
		return false
	}
	if intent.Action == "fold" && snapshot.NeedsContext {
		for _, item := range snapshot.Items {
			if item.ID == "parent:1" && item.Status == "ok" {
				return containsEvidence(intent.EvidenceIDs, item.ID)
			}
		}
		return false
	}
	return true
}
