package moderation

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type calibrationCounts struct {
	Confirmed    uint `json:"confirmed"`
	Rejected     uint `json:"rejected"`
	Unclassified uint `json:"unclassified"`
}

func countCalibration(db *gorm.DB, modelID string) (calibrationCounts, error) {
	var counts calibrationCounts
	// Each comment contributes once. A disagreement takes precedence over
	// confirmations from other reports associated with the same comment.
	grouped := db.Table("moderation_decisions d").
		Select(`r.comment_id, MAX(d.action = 'fold' AND d.policy_id = 'SPAM_PROMO') AS confirmed,
    MAX(d.action = 'keep' OR (d.action = 'fold' AND d.policy_id = 'HARASSMENT')) AS rejected`).
		Joins("JOIN moderation_shadow_runs s ON s.id = d.shadow_run_id AND s.case_id = d.case_id").
		Joins("JOIN moderation_cases c ON c.id = d.case_id").
		Joins("JOIN moderation_reports r ON r.id = c.report_id").
		Where("d.independent_review = ? AND s.fold_candidate = ? AND s.model = ? AND s.policy_version = ? AND s.prompt_version = ? AND s.rule_version = ?",
			true, true, modelID, policyVersion, promptVersion, CandidateRuleVersion).
		Group("r.comment_id")
	err := db.Table("(?) AS samples", grouped).
		Select(`COALESCE(SUM(rejected = 0 AND confirmed = 1), 0) AS confirmed,
    COALESCE(SUM(rejected = 1), 0) AS rejected,
    COALESCE(SUM(rejected = 0 AND confirmed = 0), 0) AS unclassified`).Scan(&counts).Error
	return counts, err
}

func calibrationReport(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input struct {
			Model string `json:"model" binding:"required,max=100"`
		}
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.Model) == "" {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		modelID := strings.TrimSpace(input.Model)
		counts, err := countCalibration(db.WithContext(c.Request.Context()), modelID)
		if err != nil {
			respondError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{
			"model": modelID, "policy_version": policyVersion, "prompt_version": promptVersion, "rule_version": CandidateRuleVersion,
			"shadow_confirmed": counts.Confirmed, "shadow_rejected": counts.Rejected, "shadow_unclassified": counts.Unclassified,
			"shadow_gate_passed": counts.Confirmed >= 30 && counts.Rejected == 0,
		})
	}
}
