package moderation

import (
	"feedsystem_video_go/internal/middleware/jwt"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SuggestionView struct {
	CaseID     uint `gorm:"primaryKey;autoIncrement:false"`
	ReviewerID uint `gorm:"primaryKey;autoIncrement:false"`
}

func (SuggestionView) TableName() string { return "moderation_suggestion_views" }

func reviewSuggestions(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input struct {
			ID uint `json:"case_id" binding:"required"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.AbortWithStatus(400)
			return
		}
		reviewer, _ := jwt.GetAccountID(c)
		runs := []ShadowRun{}
		err := db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
			// Serialize reads of model output with decisions for the same reviewer.
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&Reviewer{}, "account_id = ?", reviewer).Error; err != nil {
				return err
			}
			var item Case
			if err := tx.First(&item, input.ID).Error; err != nil {
				return err
			}
			if err := tx.Where("case_id = ?", item.ID).Order("attempt").Find(&runs).Error; err != nil {
				return err
			}
			for _, run := range runs {
				if len(run.Suggestion) > 0 && string(run.Suggestion) != "null" {
					return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&SuggestionView{CaseID: item.ID, ReviewerID: reviewer}).Error
				}
			}
			return nil
		})
		if err != nil {
			respondError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(200, gin.H{"shadow_runs": runs})
	}
}

func recordIndependentReview(tx *gorm.DB, item Case, decision *Decision) error {
	if item.State != "awaiting_review" || (decision.Action != "keep" && decision.Action != "fold") {
		return nil
	}
	var runs []ShadowRun
	if err := tx.Select("id", "fold_candidate").Where("case_id = ? AND attempt = ?", item.ID, item.Attempts).Find(&runs).Error; err != nil {
		return err
	}
	if len(runs) == 0 || !runs[0].FoldCandidate {
		return nil
	}
	decision.ShadowRunID = &runs[0].ID
	var views int64
	if err := tx.Model(&SuggestionView{}).Where("case_id = ? AND reviewer_id = ?", item.ID, decision.ReviewerID).Count(&views).Error; err != nil {
		return err
	}
	decision.IndependentReview = views == 0
	return nil
}
