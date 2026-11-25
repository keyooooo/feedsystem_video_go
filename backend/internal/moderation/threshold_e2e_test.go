//go:build moderation_e2e

package moderation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/account"
	appdb "feedsystem_video_go/internal/db"
	"feedsystem_video_go/internal/moderation"
	"feedsystem_video_go/internal/video"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	mysqlconfig "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type thresholdDecisionModel struct {
	model.ToolCallingChatModel
	confidence float64
}

func (m thresholdDecisionModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
func (m thresholdDecisionModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	raw, _ := json.Marshal(map[string]any{"action": "fold", "policy_id": "SPAM_PROMO", "confidence": m.confidence, "evidence_ids": []string{"target:1"}, "reason": "主动招揽私聊领取福利"})
	return schema.AssistantMessage("", []schema.ToolCall{{ID: "threshold-final", Type: "function", Function: schema.FunctionCall{Name: "submit_review_decision", Arguments: string(raw)}}}), nil
}

func TestThresholdAutomaticFoldMySQL(t *testing.T) {
	dsn := os.Getenv("MODERATION_TEST_MYSQL_DSN")
	cfg, err := mysqlconfig.ParseDSN(dsn)
	if err != nil || cfg.DBName != "moderation_e2e_test" {
		t.Fatal("MODERATION_TEST_MYSQL_DSN must select moderation_e2e_test")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := appdb.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                         string
		mode                         moderation.AutoFoldMode
		confidence                   float64
		changed, override, duplicate bool
		outcome                      string
	}{
		{name: "threshold without channel or rollout", mode: moderation.AutoFoldThreshold, confidence: .90, outcome: "applied"},
		{name: "below threshold refers to human", mode: moderation.AutoFoldThreshold, confidence: .899, outcome: "not_eligible"},
		{name: "shadow never writes", mode: moderation.AutoFoldShadow, confidence: .99, outcome: "shadow_only"},
		{name: "approved retains keyword gate", mode: moderation.AutoFoldApproved, confidence: .99, outcome: "not_eligible"},
		{name: "changed target is protected", mode: moderation.AutoFoldThreshold, confidence: .93, changed: true, outcome: "target_changed"},
		{name: "human keep is protected", mode: moderation.AutoFoldThreshold, confidence: .93, override: true, outcome: "human_override"},
		{name: "duplicate reports share disposition", mode: moderation.AutoFoldThreshold, confidence: .93, duplicate: true, outcome: "applied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			reporter := account.Account{Username: fmt.Sprintf("threshold-%d", time.Now().UnixNano())}
			must(db.Create(&reporter).Error)
			target := video.Comment{VideoID: 1, AuthorID: reporter.ID, Content: "加我微信，免费送福利噢，私聊我。"}
			must(db.Create(&target).Error)
			seed := func() moderation.Case {
				report := moderation.Report{ReporterID: reporter.ID, CommentID: target.ID, ReasonCode: "spam_promo", RequestID: fmt.Sprintf("threshold-%d", time.Now().UnixNano())}
				must(db.Create(&report).Error)
				item := moderation.Case{ReportID: report.ID, State: "queued"}
				must(db.Create(&item).Error)
				return item
			}
			if tc.override {
				item := seed()
				must(db.Model(&item).Update("state", "resolved").Error)
				must(db.Create(&moderation.Decision{CaseID: item.ID, ReviewerID: reporter.ID, Action: "keep", Reason: "人工保留", RequestID: fmt.Sprintf("keep-%d", item.ID)}).Error)
			}
			items := []moderation.Case{seed()}
			if tc.duplicate {
				items = append(items, seed())
			}
			evaluator := moderation.NewShadowEvaluator(db, thresholdDecisionModel{confidence: tc.confidence}, "scripted-threshold-test")
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- moderation.RunShadow(ctx, db, func(ctx context.Context, r moderation.Report) (moderation.ShadowRun, bool) {
					result, retry := evaluator(ctx, r)
					if tc.changed {
						db.Model(&video.Comment{}).Where("id = ?", target.ID).Update("content", "已修改")
					}
					return result, retry
				}, nil, tc.mode)
			}()
			defer func() { cancel(); <-done }()
			deadline := time.Now().Add(10 * time.Second)
			for {
				complete := true
				for _, item := range items {
					var current moderation.Case
					must(db.First(&current, item.ID).Error)
					if current.State != "resolved" && current.State != "awaiting_review" {
						complete = false
					}
				}
				if complete {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("worker did not finish")
				}
				time.Sleep(25 * time.Millisecond)
			}
			for _, item := range items {
				var run moderation.ShadowRun
				must(db.Where("case_id = ?", item.ID).First(&run).Error)
				if run.ExecutionOutcome != tc.outcome || run.ExecutionMode != string(tc.mode) {
					t.Fatalf("outcome=%s mode=%s failure=%s", run.ExecutionOutcome, run.ExecutionMode, run.Failure)
				}
			}
			must(db.First(&target, target.ID).Error)
			if target.ModerationFolded != (tc.outcome == "applied") {
				t.Fatal("unexpected visibility")
			}
			if tc.duplicate && target.ModerationVersion != 1 {
				t.Fatal("duplicate report changed target twice")
			}
		})
	}
}
