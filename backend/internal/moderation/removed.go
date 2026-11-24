package moderation

import (
	"errors"
	"fmt"

	"feedsystem_video_go/internal/video"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func resolveRemovedTarget(tx *gorm.DB, item Case, run ShadowRun) (bool, error) {
	var report Report
	if err := tx.First(&report, item.ReportID).Error; err != nil {
		return false, err
	}
	var target video.Comment
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&target, report.CommentID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if target.RemovedAt == nil {
		return false, nil
	}
	decision := Decision{
		CaseID: item.ID, CaseVersion: item.Version, TargetVersion: target.ModerationVersion,
		RequestID: fmt.Sprintf("removed-case-%d", item.ID), ShadowRunID: &run.ID, Action: "target_removed", Reason: "作者已删除被举报评论",
	}
	if err := tx.Create(&decision).Error; err != nil {
		return false, err
	}
	return true, notifyDecision(tx, report, target, decision, false)
}
