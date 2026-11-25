package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	rediscache "feedsystem_video_go/internal/middleware/redis"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	shadowTimeout     = 20 * time.Second
	shadowLease       = 35 * time.Second
	maxShadowAttempts = 3
)

type ShadowRun struct {
	ExecutionOutcome   string          `json:"execution_outcome,omitempty" gorm:"size:32"`
	DecisionProtocol   string          `json:"decision_protocol,omitempty" gorm:"size:32"`
	QueueMS            int64           `json:"queue_ms" gorm:"not null;default:0"`
	EvidenceMS         int64           `json:"evidence_ms" gorm:"not null;default:0"`
	ModelMS            int64           `json:"model_ms" gorm:"not null;default:0"`
	ModelCalls         int             `json:"model_calls" gorm:"not null;default:0"`
	Tokens             int             `json:"tokens" gorm:"not null;default:0"`
	ReportToDecisionMS int64           `json:"report_to_decision_ms" gorm:"not null;default:0"`
	ResultState        string          `json:"result_state,omitempty" gorm:"size:32"`
	FoldCandidate      bool            `json:"fold_candidate" gorm:"not null;default:false"`
	RuleVersion        string          `json:"rule_version,omitempty"`
	ID                 uint            `json:"id"`
	CaseID             uint            `json:"case_id" gorm:"uniqueIndex:shadow_attempt"`
	Attempt            uint            `json:"attempt" gorm:"uniqueIndex:shadow_attempt"`
	Evidence           json.RawMessage `json:"evidence,omitempty" gorm:"type:json"`
	Suggestion         json.RawMessage `json:"suggestion,omitempty" gorm:"type:json"`
	Model              string          `json:"model,omitempty"`
	PolicyVersion      string          `json:"policy_version,omitempty"`
	PromptVersion      string          `json:"prompt_version,omitempty"`
	Failure            string          `json:"failure,omitempty"`
	DurationMS         int64           `json:"duration_ms"`
	CreatedAt          time.Time       `json:"created_at" gorm:"index"`
}

func (ShadowRun) TableName() string { return "moderation_shadow_runs" }

type ShadowEvaluator func(context.Context, Report) (result ShadowRun, retry bool)

func claimShadowCase(db *gorm.DB) (Case, error) {
	var item Case
	err := db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("(state = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)) OR (state = ? AND lease_until <= ?)", "queued", now, "running", now).
			Order("id").Take(&item).Error
		if err != nil {
			return err
		}
		item.ReadyAt = item.CreatedAt
		if item.State == "running" && item.LeaseUntil != nil {
			item.ReadyAt = *item.LeaseUntil
		} else if item.NextAttemptAt != nil {
			item.ReadyAt = *item.NextAttemptAt
		}
		if item.Attempts >= maxShadowAttempts {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ShadowRun{CaseID: item.ID, Attempt: item.Attempts, Failure: "lease_expired", ResultState: "awaiting_review"}).Error; err != nil {
				return err
			}
			item.State = "awaiting_review"
			item.Version++
			return tx.Model(&item).Updates(map[string]any{"state": item.State, "lease_until": nil, "version": item.Version}).Error
		}
		if item.State == "running" {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ShadowRun{CaseID: item.ID, Attempt: item.Attempts, Failure: "lease_expired"}).Error; err != nil {
				return err
			}
		}
		item.Version++
		item.Attempts++
		deadline := now.Add(shadowLease)
		item.LeaseUntil = &deadline
		item.State = "running"
		return tx.Model(&item).Updates(map[string]any{"state": item.State, "version": item.Version, "attempts": item.Attempts, "lease_until": deadline, "next_attempt_at": nil}).Error
	})
	return item, err
}

func finishShadowCase(db *gorm.DB, item Case, result ShadowRun, retry bool, cache *rediscache.Client) error {
	changed := false
	err := db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		var nextAttempt *time.Time
		state := "awaiting_review"
		if retry && item.Attempts < maxShadowAttempts {
			state = "queued"
			due := now.Add(time.Second * time.Duration(1<<item.Attempts))
			nextAttempt = &due
		}
		updated := tx.Model(&Case{}).Where("id = ? AND state = ? AND version = ? AND lease_until > ?", item.ID, "running", item.Version, now).
			Updates(map[string]any{"state": state, "lease_until": nil, "next_attempt_at": nextAttempt, "version": item.Version + 1})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			return nil
		}
		changed = true
		result.CaseID = item.ID
		result.Attempt = item.Attempts
		result.ResultState = state
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		outcome := "retry_scheduled"
		if state == "awaiting_review" {
			if result.Failure == "target_removed" {
				closed, err := resolveRemovedTarget(tx, item, result)
				if err != nil {
					return err
				}
				outcome = "target_changed"
				if closed {
					outcome = "target_removed"
				}
			} else if result.Failure != "" {
				outcome = "evaluation_failed"
			} else {
				var err error
				outcome, err = applyAutomaticFold(tx, item, result)
				if err != nil {
					return err
				}
			}
		}
		if outcome == "applied" || outcome == "target_removed" {
			if err := tx.Model(&Case{}).Where("id = ?", item.ID).Update("state", "resolved").Error; err != nil {
				return err
			}
			state = "resolved"
		}
		return tx.Model(&result).Updates(map[string]any{"result_state": state, "execution_outcome": outcome}).Error
	})
	if err == nil && changed {
		publishReviewEvent(db.Statement.Context, cache, reviewEvent{CaseID: item.ID, Version: item.Version + 1, Type: "evaluated"})
	}
	return err
}

// RunShadow consumes durable work leases. Evaluators return suggestions and
// evidence. An approved rollout may apply a verified spam candidate.
func RunShadow(ctx context.Context, db *gorm.DB, evaluate ShadowEvaluator, cache *rediscache.Client) error {
	if evaluate == nil {
		return errors.New("missing shadow evaluator")
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	db = db.WithContext(ctx)
	for {
		item, err := claimShadowCase(db)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				continue
			}
		}
		if err != nil {
			return err
		}
		if item.State != "running" {
			publishReviewEvent(ctx, cache, reviewEvent{CaseID: item.ID, Version: item.Version, Type: "evaluated"})
			continue
		}
		var report Report
		if err := db.First(&report, item.ReportID).Error; err != nil {
			return err
		}
		started := time.Now()
		queueMS := max(int64(0), started.Sub(item.ReadyAt).Milliseconds())
		attempt, cancel := context.WithTimeout(ctx, shadowTimeout)
		result, retry := evaluate(attempt, report)
		if errors.Is(attempt.Err(), context.DeadlineExceeded) {
			result.Failure = "deadline_exceeded"
			result.Suggestion = nil
			retry = true
		}
		cancel()
		finished := time.Now()
		result.DurationMS = finished.Sub(started).Milliseconds()
		result.QueueMS = queueMS
		result.ReportToDecisionMS = max(int64(0), finished.Sub(report.CreatedAt).Milliseconds())
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := finishShadowCase(db, item, result, retry, cache); err != nil {
			return err
		}
	}
}
