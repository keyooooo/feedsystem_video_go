package moderation

import (
	"errors"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"strings"
	"time"

	"feedsystem_video_go/internal/middleware/jwt"
	"feedsystem_video_go/internal/video"
	"feedsystem_video_go/internal/worker"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Reviewer struct {
	AccountID uint `gorm:"primaryKey;autoIncrement:false"`
}

func (Reviewer) TableName() string { return "moderation_reviewers" }

type decisionInput struct {
	FalseFold     bool   `json:"false_fold,omitempty"`
	PolicyID      string `json:"policy_id,omitempty" binding:"omitempty,oneof=SPAM_PROMO HARASSMENT"`
	CaseID        uint   `json:"case_id" binding:"required"`
	CaseVersion   *uint  `json:"case_version" binding:"required"`
	TargetVersion *uint  `json:"target_version" binding:"required"`
	Action        string `json:"action" binding:"required,oneof=keep fold restore"`
	Reason        string `json:"reason" binding:"required,max=200"`
	RequestID     string `json:"request_id" binding:"required,max=64"`
}

type Decision struct {
	RelatedDecisionID *uint     `json:"related_decision_id,omitempty"`
	FalseFold         bool      `json:"false_fold,omitempty" gorm:"not null;default:false"`
	PolicyID          string    `json:"policy_id,omitempty" gorm:"size:32;not null;default:''"`
	IndependentReview bool      `json:"independent_review,omitempty" gorm:"not null;default:false"`
	ShadowRunID       *uint     `json:"shadow_run_id,omitempty"`
	ID                uint      `json:"id"`
	CaseID            uint      `json:"case_id" gorm:"index"`
	ReviewerID        uint      `json:"reviewer_id" gorm:"uniqueIndex:review_request"`
	RequestID         string    `json:"-" gorm:"size:64;uniqueIndex:review_request"`
	CaseVersion       uint      `json:"case_version"`
	TargetVersion     uint      `json:"target_version"`
	Action            string    `json:"action" gorm:"size:16"`
	Reason            string    `json:"reason" gorm:"size:200"`
	CreatedAt         time.Time `json:"created_at"`
}

func (Decision) TableName() string { return "moderation_decisions" }

func respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.AbortWithStatus(404)
	case errors.Is(err, errConflict):
		c.AbortWithStatus(409)
	default:
		c.AbortWithStatus(500)
	}
}

func RegisterReview(group *gin.RouterGroup, db *gorm.DB, cache *rediscache.Client) {
	group.Use(func(c *gin.Context) {
		id, err := jwt.GetAccountID(c)
		if err != nil {
			c.AbortWithStatus(401)
			return
		}
		err = db.WithContext(c.Request.Context()).First(&Reviewer{}, "account_id = ?", id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatus(403)
			return
		}
		if err != nil {
			respondError(c, err)
			return
		}
		c.Next()
	})
	group.GET("/stream", reviewStream(db, cache))
	group.POST("/list", func(c *gin.Context) {
		var input struct {
			After uint   `json:"after"`
			State string `json:"state" binding:"omitempty,oneof=queued awaiting_review resolved running"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.AbortWithStatus(400)
			return
		}
		cases := []Case{}
		query := db.WithContext(c.Request.Context()).Where("id > ?", input.After)
		if input.State != "" {
			query = query.Where("state = ?", input.State)
		}
		if err := query.Order("id").Limit(50).Find(&cases).Error; err != nil {
			respondError(c, err)
			return
		}
		c.JSON(200, gin.H{"cases": cases})
	})
	group.POST("/shadow-metrics", shadowMetricsHandler(db))
	group.POST("/calibration", calibrationReport(db))
	group.POST("/suggestions", reviewSuggestions(db))
	group.POST("/detail", func(c *gin.Context) {
		var input struct {
			ID uint `json:"case_id" binding:"required"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.AbortWithStatus(400)
			return
		}
		tx := db.WithContext(c.Request.Context())
		var item Case
		var report Report
		var target video.Comment
		history := []Decision{}
		var runCount int64
		var parentText string
		err := tx.Transaction(func(tx *gorm.DB) error {
			if err := tx.First(&item, input.ID).Error; err != nil {
				return err
			}
			if err := tx.First(&report, item.ReportID).Error; err != nil {
				return err
			}
			if err := tx.First(&target, report.CommentID).Error; err != nil {
				return err
			}
			if err := tx.Where("case_id = ?", item.ID).Order("id").Find(&history).Error; err != nil {
				return err
			}
			if target.ReplyToCommentID != nil {
				parent := []video.Comment{}
				if err := threadScope(tx, target).Select("content").Where("id = ?", *target.ReplyToCommentID).Limit(1).Find(&parent).Error; err != nil {
					return err
				}
				if len(parent) > 0 {
					parentText = parent[0].Content
				}
			}
			return tx.Model(&ShadowRun{}).Where("case_id = ?", item.ID).Count(&runCount).Error
		})
		if err != nil {
			respondError(c, err)
			return
		}
		c.JSON(200, gin.H{"case": item, "comment": target, "target_version": target.ModerationVersion, "reason_code": report.ReasonCode, "detail": report.Detail, "decisions": history, "shadow_run_count": runCount, "reply_context": parentText})
	})
	group.POST("/decide", func(c *gin.Context) {
		var input decisionInput
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.Reason) == "" || strings.TrimSpace(input.RequestID) == "" || (input.FalseFold && (input.Action != "restore" || input.PolicyID != "")) {
			c.AbortWithStatus(400)
			return
		}
		user, _ := jwt.GetAccountID(c)
		decision, err := decide(db.WithContext(c.Request.Context()), user, input)
		if err != nil {
			respondError(c, err)
			return
		}
		publishReviewEvent(c.Request.Context(), cache, reviewEvent{CaseID: decision.CaseID, Version: decision.CaseVersion + 1, Type: "decided"})
		c.JSON(200, decision)
	})
}

func decide(db *gorm.DB, user uint, input decisionInput) (Decision, error) {
	decision := Decision{FalseFold: input.FalseFold, PolicyID: input.PolicyID, CaseID: input.CaseID, ReviewerID: user, RequestID: input.RequestID, Action: input.Action, Reason: strings.TrimSpace(input.Reason), CaseVersion: *input.CaseVersion, TargetVersion: *input.TargetVersion}
	err := db.Transaction(func(tx *gorm.DB) error {
		lock := clause.Locking{Strength: "UPDATE"}
		// Lock the reviewer first to serialize retries using the same request key.
		if err := tx.Clauses(lock).First(&Reviewer{}, "account_id = ?", user).Error; err != nil {
			return err
		}
		var prior Decision
		err := tx.Where("reviewer_id = ? AND request_id = ?", user, input.RequestID).Take(&prior).Error
		if err == nil {
			if prior.CaseID != decision.CaseID || prior.Action != decision.Action || (!prior.FalseFold && prior.PolicyID != decision.PolicyID) || prior.FalseFold != decision.FalseFold || prior.Reason != decision.Reason || prior.CaseVersion != decision.CaseVersion || prior.TargetVersion != decision.TargetVersion {
				return errConflict
			}
			decision = prior
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var item Case
		if err := tx.Clauses(lock).First(&item, input.CaseID).Error; err != nil {
			return err
		}
		if item.State == "awaiting_review" || input.FalseFold {
			// Serialize independent labels against the worker's shared rollout lock.
			var rollout Rollout
			err := tx.Clauses(lock).First(&rollout, "policy_id = ?", "SPAM_PROMO").Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		var report Report
		if err := tx.First(&report, item.ReportID).Error; err != nil {
			return err
		}
		var target video.Comment
		if err := tx.Clauses(lock).First(&target, report.CommentID).Error; err != nil {
			return err
		}
		if item.Version != *input.CaseVersion || target.ModerationVersion != *input.TargetVersion || item.State == "running" {
			return errConflict
		}
		if target.RemovedAt != nil && input.Action != "keep" {
			return errConflict
		}
		if err := recordIndependentReview(tx, item, &decision); err != nil {
			return err
		}
		if input.FalseFold {
			if !target.ModerationFolded {
				return errConflict
			}
			if err := pauseFalseFold(tx, item, &decision); err != nil {
				return err
			}
		}
		wasFolded := target.ModerationFolded
		folded := input.Action == "fold" || (input.Action == "keep" && target.ModerationFolded)
		if err := tx.Model(&target).Updates(map[string]any{"moderation_folded": folded, "moderation_version": target.ModerationVersion + 1}).Error; err != nil {
			return err
		}
		if err := tx.Model(&item).Updates(map[string]any{"state": "resolved", "version": item.Version + 1}).Error; err != nil {
			return err
		}
		if err := tx.Create(&decision).Error; err != nil {
			return err
		}
		if err := notifyDecision(tx, report, target, decision, folded != wasFolded); err != nil {
			return err
		}
		return tx.First(&decision, decision.ID).Error
	})
	return decision, err
}

func RevealComment(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input struct {
			ID uint `json:"comment_id" binding:"required"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.AbortWithStatus(400)
			return
		}
		var target video.Comment
		err := db.WithContext(c.Request.Context()).Select("content").Where("id = ? AND removed_at IS NULL", input.ID).Take(&target).Error
		if err != nil {
			respondError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(200, gin.H{"content": target.Content})
	}
}

func notifyDecision(tx *gorm.DB, report Report, target video.Comment, decision Decision, changed bool) error {
	message := "你的评论举报已处理"
	switch decision.Action {
	case "fold":
		message = "你的举报已处理，相关评论已折叠"
	case "restore":
		message = "你的举报已复核，相关评论已恢复展示"
	case "target_removed":
		message = "你的举报已处理，作者已删除相关评论"
	}
	notifications := []worker.Notification{{RecipientID: report.ReporterID, Type: "moderation_report", TargetID: report.ID, Content: message}}
	if changed {
		notifications = append(notifications, worker.Notification{RecipientID: target.AuthorID, Type: "moderation_comment", TargetID: target.ID, Content: decision.Reason})
	}
	return tx.Create(&notifications).Error
}
