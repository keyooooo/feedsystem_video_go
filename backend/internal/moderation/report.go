package moderation

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"feedsystem_video_go/internal/middleware/jwt"
	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

type Report struct {
	ID         uint
	ReporterID uint   `gorm:"uniqueIndex:report_request;not null"`
	RequestID  string `gorm:"size:64;uniqueIndex:report_request;not null"`
	CommentID  uint   `gorm:"not null;index"`
	ReasonCode string `gorm:"size:32;not null"`
	Detail     string `gorm:"size:512"`
	CreatedAt  time.Time
}

func (Report) TableName() string { return "moderation_reports" }

type Outbox struct {
	ID          uint
	ReportID    uint `gorm:"uniqueIndex;not null"`
	CreatedAt   time.Time
	PublishedAt *time.Time `gorm:"index"`
}

func (Outbox) TableName() string { return "moderation_outbox" }

const mediaReportReason = "media_only"

type Case struct {
	RoutingReason string     `json:"routing_reason,omitempty" gorm:"size:32"`
	ReadyAt       time.Time  `json:"-" gorm:"-"`
	Attempts      uint       `json:"-" gorm:"not null;default:0"`
	LeaseUntil    *time.Time `json:"-" gorm:"index"`
	NextAttemptAt *time.Time `json:"-" gorm:"index"`
	ID            uint       `json:"id"`
	Version       uint       `json:"version" gorm:"not null;default:0"`
	ReportID      uint       `gorm:"uniqueIndex;not null"`
	State         string     `json:"state" gorm:"size:32;index;not null"`
	CreatedAt     time.Time
}

func (Case) TableName() string { return "moderation_cases" }

var errConflict = errors.New("request key already used with different content")

type reportInput struct {
	CommentID  uint   `json:"comment_id" binding:"required"`
	RequestID  string `json:"request_id" binding:"required,max=64"`
	ReasonCode string `json:"reason_code" binding:"required,oneof=spam_promo harassment media_only other"`
	Detail     string `json:"detail" binding:"max=512"`
}

func RegisterReports(group *gin.RouterGroup, db *gorm.DB, limiter gin.HandlerFunc) {
	group.POST("/create", limiter, func(c *gin.Context) {
		var input reportInput
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.RequestID) == "" {
			c.JSON(400, gin.H{"error": "invalid report"})
			return
		}
		id, err := jwt.GetAccountID(c)
		if err != nil {
			c.AbortWithStatus(401)
			return
		}
		report := Report{ReporterID: id, RequestID: input.RequestID, CommentID: input.CommentID, ReasonCode: input.ReasonCode, Detail: strings.TrimSpace(input.Detail)}
		conn := db.WithContext(c.Request.Context())
		lookup := func(tx *gorm.DB) error {
			var old Report
			if err := tx.Where("reporter_id = ? AND request_id = ?", id, input.RequestID).First(&old).Error; err != nil {
				return err
			}
			if old.CommentID != report.CommentID || old.ReasonCode != report.ReasonCode || old.Detail != report.Detail {
				return errConflict
			}
			report.ID = old.ID
			return nil
		}
		err = conn.Transaction(func(tx *gorm.DB) error {
			if e := lookup(tx); !errors.Is(e, gorm.ErrRecordNotFound) {
				return e
			}
			var target struct{ ID uint }
			if e := tx.Table("comments").Select("id").Where("id = ? AND removed_at IS NULL", input.CommentID).Take(&target).Error; e != nil {
				return e
			}
			if e := tx.Create(&report).Error; e != nil {
				return e
			}
			return tx.Create(&Outbox{ReportID: report.ID}).Error
		})
		var duplicate *mysql.MySQLError
		if errors.As(err, &duplicate) && duplicate.Number == 1062 {
			err = lookup(conn)
		}
		switch {
		case err == nil:
			c.JSON(http.StatusAccepted, gin.H{"report_id": report.ID})
		case errors.Is(err, errConflict):
			c.AbortWithStatus(409)
		case errors.Is(err, gorm.ErrRecordNotFound):
			c.AbortWithStatus(404)
		default:
			c.AbortWithStatus(500)
		}
	})
	group.POST("/get", func(c *gin.Context) {
		var req struct {
			ID uint `json:"report_id" binding:"required"`
		}
		if c.ShouldBindJSON(&req) != nil {
			c.AbortWithStatus(400)
			return
		}
		id, err := jwt.GetAccountID(c)
		if err != nil {
			c.AbortWithStatus(401)
			return
		}
		var row struct {
			ReportID uint   `json:"report_id"`
			State    string `json:"state"`
			Result   string `json:"result,omitempty"`
		}
		err = db.WithContext(c.Request.Context()).Table("moderation_reports r").Select("r.id AS report_id, COALESCE(m.state, 'accepted') AS state, COALESCE(d.action, '') AS result").
			Joins("LEFT JOIN moderation_cases m ON m.report_id=r.id").
			Joins("LEFT JOIN moderation_decisions d ON d.id = (SELECT MAX(recent.id) FROM moderation_decisions recent WHERE recent.case_id = m.id)").Where("r.id=? AND r.reporter_id=?", req.ID, id).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatus(404)
			return
		}
		if err != nil {
			c.AbortWithStatus(500)
			return
		}
		c.JSON(200, row)
	})
}
