package moderation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"feedsystem_video_go/internal/video"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Rollout is an operator-owned, version-specific approval. The worker reads
// it inside the disposition transaction so disabling it takes effect live.
type Rollout struct {
	PolicyID              string `gorm:"size:32;primaryKey"`
	Model                 string
	PolicyVersion         string
	PromptVersion         string
	RuleVersion           string
	DatasetVersion        string
	DatasetSHA256         string `gorm:"size:64"`
	DevelopmentSHA256     string `gorm:"size:64"`
	ReportSHA256          string `gorm:"size:64"`
	Percent               uint
	NormalCases           uint
	FalseCandidates       uint
	SpamCases             uint
	CorrectSpamCandidates uint
	InjectionCases        uint
	InjectionBlocked      uint
}

func (Rollout) TableName() string { return "moderation_rollouts" }

func ValidSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func (r Rollout) allows(run ShadowRun, commentID uint) bool {
	return r.Percent > 0 && r.Percent <= 100 && commentID%100 < r.Percent &&
		r.Model != "" && r.Model == run.Model && r.DatasetVersion != "" &&
		ValidSHA256(r.DatasetSHA256) && ValidSHA256(r.DevelopmentSHA256) && ValidSHA256(r.ReportSHA256) && r.DatasetSHA256 != r.DevelopmentSHA256 &&
		r.PolicyVersion == policyVersion && r.PolicyVersion == run.PolicyVersion &&
		r.PromptVersion == promptVersion && r.PromptVersion == run.PromptVersion &&
		r.RuleVersion == CandidateRuleVersion && r.RuleVersion == run.RuleVersion &&
		r.NormalCases >= 100 && r.FalseCandidates == 0 &&
		r.SpamCases >= 30 && r.CorrectSpamCandidates <= r.SpamCases &&
		uint64(r.CorrectSpamCandidates)*5 >= uint64(r.SpamCases)*4 &&
		r.InjectionCases >= 20 && r.InjectionBlocked == r.InjectionCases
}

func applyAutomaticFold(tx *gorm.DB, item Case, run ShadowRun, modes ...AutoFoldMode) (string, error) {
	mode := autoFoldMode(modes)
	if mode == AutoFoldShadow {
		return "shadow_only", nil
	}
	if !run.FoldCandidate || run.Failure != "" {
		return "not_eligible", nil
	}
	var snapshot Snapshot
	var suggestion Suggestion
	if json.Unmarshal(run.Evidence, &snapshot) != nil || json.Unmarshal(run.Suggestion, &suggestion) != nil || !authorizedFoldIntent(run, snapshot, suggestion) || !ThresholdFoldCandidate(snapshot, suggestion) {
		return "invalid_terminal_intent", nil
	}
	if run.Model == "" || run.PolicyVersion != policyVersion || run.PromptVersion != promptVersion || run.RuleVersion != CandidateRuleVersion {
		return "version_mismatch", nil
	}
	var report Report
	if err := tx.First(&report, item.ReportID).Error; err != nil {
		return "", err
	}
	if mode == AutoFoldApproved {
		if !FoldCandidate(snapshot, suggestion) {
			return "not_eligible", nil
		}
		var rollout Rollout
		err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&rollout, "policy_id = ?", "SPAM_PROMO").Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "rollout_missing", nil
		}
		if err != nil {
			return "", err
		}
		if !rollout.allows(run, report.CommentID) {
			return "rollout_ineligible", nil
		}
		counts, err := countCalibration(tx, run.Model)
		if err != nil {
			return "", err
		}
		if counts.Confirmed < 30 || counts.Rejected != 0 {
			return "calibration_pending", nil
		}
	}
	var target video.Comment
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&target, report.CommentID).Error; err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(target.Content))
	if target.RemovedAt != nil || hex.EncodeToString(digest[:]) != snapshot.TargetDigest {
		return "target_changed", nil
	}
	for _, evidence := range snapshot.Items {
		if evidence.ID == "target:1" && (evidence.Text != evidenceText(target.Content) || (mode == AutoFoldApproved && !hasOriginalChannel(target.Content)) ||
			evidence.CollectedAt.IsZero() || time.Since(evidence.CollectedAt) > shadowLease || time.Until(evidence.CollectedAt) > time.Second) {
			return "target_evidence_stale", nil
		}
	}
	if target.ReplyToCommentID != nil {
		var parent video.Comment
		err := threadScope(tx, target).Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", *target.ReplyToCommentID).Take(&parent).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "parent_unavailable", nil
		}
		if err != nil {
			return "", err
		}
		matches := false
		for _, evidence := range snapshot.Items {
			if evidence.ID == "parent:1" && evidence.Text == evidenceText(parent.Content) {
				matches = true
				break
			}
		}
		if !matches {
			return "parent_changed", nil
		}
	}
	// A human keep or restore takes priority over later automation.
	var humanOverrides int64
	if err := tx.Table("moderation_decisions d").Joins("JOIN moderation_cases c ON c.id = d.case_id").
		Joins("JOIN moderation_reports r ON r.id = c.report_id").
		Where("r.comment_id = ? AND d.reviewer_id <> 0 AND d.action IN ?", target.ID, []string{"keep", "restore"}).
		Count(&humanOverrides).Error; err != nil {
		return "", err
	}
	if humanOverrides > 0 {
		return "human_override", nil
	}
	decision := Decision{
		CaseID: item.ID, CaseVersion: item.Version, TargetVersion: target.ModerationVersion,
		RequestID: fmt.Sprintf("auto-case-%d", item.ID), ShadowRunID: &run.ID,
		PolicyID: "SPAM_PROMO", Action: "fold", Reason: suggestion.Reason,
	}
	changed := !target.ModerationFolded
	if changed {
		if target.ModerationVersion != snapshot.TargetVersion {
			return "target_version_changed", nil
		}
		if err := tx.Model(&target).Updates(map[string]any{"moderation_folded": true, "moderation_version": target.ModerationVersion + 1}).Error; err != nil {
			return "", err
		}
	} else {
		var applied Decision
		// Lock only the decision alias. Other reports can already hold their own case rows.
		err := tx.Table("moderation_decisions d").Select("d.*").
			Joins("JOIN moderation_cases c ON c.id = d.case_id").
			Joins("JOIN moderation_reports r ON r.id = c.report_id").
			Clauses(clause.Locking{Strength: "SHARE", Table: clause.Table{Name: "d"}}).
			Where("r.comment_id = ? AND d.action = ? AND d.related_decision_id IS NULL", target.ID, "fold").
			Order("d.id DESC").Take(&applied).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "prior_decision_missing", nil
		}
		if err != nil {
			return "", err
		}
		if applied.PolicyID != "SPAM_PROMO" || applied.TargetVersion+1 != target.ModerationVersion ||
			(snapshot.TargetVersion != target.ModerationVersion && snapshot.TargetVersion != applied.TargetVersion) {
			return "prior_decision_mismatch", nil
		}
		decision.RelatedDecisionID = &applied.ID
		decision.Reason = applied.Reason
	}
	if err := recordDecision(tx, report, target, &decision, changed); err != nil {
		return "", err
	}
	return "applied", nil
}
