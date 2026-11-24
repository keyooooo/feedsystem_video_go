package moderation

import (
	"errors"

	"gorm.io/gorm"
)

func pauseFalseFold(tx *gorm.DB, item Case, decision *Decision) error {
	var previous Decision
	err := tx.Where("case_id = ?", item.ID).Order("id DESC").Take(&previous).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errConflict
	}
	if err != nil {
		return err
	}
	if previous.RelatedDecisionID != nil {
		var original Decision
		if err := tx.First(&original, *previous.RelatedDecisionID).Error; err != nil {
			return err
		}
		previous = original
	}
	if previous.Action != "fold" || previous.ReviewerID != 0 || previous.ShadowRunID == nil {
		return errConflict
	}
	var run ShadowRun
	if err := tx.First(&run, *previous.ShadowRunID).Error; err != nil {
		return err
	}
	decision.ShadowRunID = previous.ShadowRunID
	decision.PolicyID = previous.PolicyID
	// A later release can have different approved model and rule versions.
	return tx.Model(&Rollout{}).
		Where("policy_id = ? AND model = ? AND policy_version = ? AND prompt_version = ? AND rule_version = ?",
			"SPAM_PROMO", run.Model, run.PolicyVersion, run.PromptVersion, run.RuleVersion).
		Update("percent", 0).Error
}
