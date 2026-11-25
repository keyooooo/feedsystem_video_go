//go:build moderation_e2e

package moderation_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/auth"
	appdb "feedsystem_video_go/internal/db"
	apphttp "feedsystem_video_go/internal/http"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/moderation"
	"feedsystem_video_go/internal/video"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	mysqlconfig "github.com/go-sql-driver/mysql"
	amqp "github.com/rabbitmq/amqp091-go"
	goredis "github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Run with disposable MySQL, RabbitMQ and Redis instances (see moderation-e2e workflow).
func TestReportEndToEnd(t *testing.T) {
	dsn := os.Getenv("MODERATION_TEST_MYSQL_DSN")
	config, err := mysqlconfig.ParseDSN(dsn)
	if err != nil || config.DBName != "moderation_e2e_test" {
		t.Fatal("MODERATION_TEST_MYSQL_DSN must select moderation_e2e_test")
	}
	uri := os.Getenv("MODERATION_TEST_AMQP_URL")
	parsed, err := amqp.ParseURI(uri)
	if err != nil || parsed.Vhost != "moderation_e2e_test" {
		t.Fatal("MODERATION_TEST_AMQP_URL must select moderation_e2e_test")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := appdb.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	conn, err := amqp.Dial(uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	channel := func() *amqp.Channel {
		t.Helper()
		ch, err := conn.Channel()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ch.Close() })
		return ch
	}
	t.Setenv("SESSION_COOKIE_ORIGIN", "")
	t.Setenv("FEEDSYSTEM_PRODUCTION", "false")
	redisAddress := os.Getenv("MODERATION_TEST_REDIS_ADDR")
	host, _, err := net.SplitHostPort(redisAddress)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("MODERATION_TEST_REDIS_ADDR must select disposable loopback Redis")
	}
	prefix := fmt.Sprintf("moderation_e2e:%d:", time.Now().UnixNano())
	newCache := func() *rediscache.Client {
		cache := rediscache.NewClient(goredis.NewClient(&goredis.Options{Addr: redisAddress}), prefix)
		t.Cleanup(func() { cache.Close() })
		if err := cache.Ping(context.Background()); err != nil {
			t.Fatal(err)
		}
		return cache
	}
	apiCache, remoteCache, workerCache := newCache(), newCache(), newCache()
	router := apphttp.SetRouter(db, apiCache, nil, nil)
	token := func(label string) string {
		t.Helper()
		a := account.Account{Username: fmt.Sprintf("%s-%d", label, time.Now().UnixNano())}
		if err := db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
		tok, err := auth.GenerateToken(a.ID, a.Username)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&a).Update("token", tok).Error; err != nil {
			t.Fatal(err)
		}
		return tok
	}
	reporter, other := token("reporter"), token("other")
	baseVideo := video.Video{Title: "Moderation fixture", Username: "fixture", PlayURL: "/fixture.mp4", CoverURL: "/fixture.png"}
	if err := db.Create(&baseVideo).Error; err != nil {
		t.Fatal(err)
	}
	comment := video.Comment{VideoID: baseVideo.ID, Content: fmt.Sprintf("test comment %d", time.Now().UnixNano())}
	if err := db.Create(&comment).Error; err != nil {
		t.Fatal(err)
	}
	parent := video.Comment{VideoID: 73, Content: "联系 test@example.com 获取详情"}
	foreign := video.Comment{VideoID: 73, Content: "unrelated thread"}
	for _, row := range []*video.Comment{&parent, &foreign} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	reply := video.Comment{VideoID: 73, RootCommentID: &parent.ID, ReplyToCommentID: &parent.ID, Content: "不建议联系"}
	if err := db.Create(&reply).Error; err != nil {
		t.Fatal(err)
	}
	snapshot, err := moderation.CollectEvidence(context.Background(), db, moderation.Report{CommentID: reply.ID, ReasonCode: "other"})
	if err != nil || !snapshot.NeedsContext || snapshot.TargetDigest == "" {
		t.Fatalf("missing reply evidence: %v", err)
	}
	contextItems, err := snapshot.ThreadContext(context.Background(), db, "neighbors", 5)
	if err != nil || len(contextItems) != 1 || !strings.Contains(contextItems[0].Text, "[邮箱]") || strings.Contains(contextItems[0].Text, "example.com") {
		t.Fatalf("thread boundary or redaction failed: %+v (%v)", contextItems, err)
	}
	t.Run("optional source times out without canceling other evidence", func(t *testing.T) {
		lockContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		lock, err := pool.Conn(lockContext)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		if _, err := lock.ExecContext(lockContext, "LOCK TABLES videos WRITE"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			cleanup, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			if _, err := lock.ExecContext(cleanup, "UNLOCK TABLES"); err != nil {
				t.Error(err)
			}
		}()
		snapshot, err := moderation.CollectEvidence(lockContext, db, moderation.Report{CommentID: reply.ID, ReasonCode: "other"})
		if err != nil {
			t.Fatal(err)
		}
		statuses := map[string]string{}
		for _, item := range snapshot.Items {
			statuses[item.ID] = item.Status
		}
		if statuses["video:1"] != "unavailable" || statuses["target:1"] != "ok" || statuses["parent:1"] != "ok" || statuses["history:1"] == "unavailable" {
			t.Fatalf("unexpected evidence statuses after video timeout: %v", statuses)
		}
	})
	if err := db.Model(&parent).Update("removed_at", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	contextItems, err = snapshot.ThreadContext(context.Background(), db, "parent_chain", 5)
	if err != nil || len(contextItems) != 0 {
		t.Fatalf("deleted parent exposed: %+v (%v)", contextItems, err)
	}
	post := func(path, body, bearer string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s returned %d, want %d: %s", path, rec.Code, status, rec.Body.String())
		}
		return rec
	}
	create := fmt.Sprintf(`{"request_id":"retry","comment_id":%d,"reason_code":"other"}`, comment.ID)
	post("/report/create", create, "", 401)
	removedAt := time.Now()
	deleted := video.Comment{RemovedAt: &removedAt}
	if err := db.Create(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	post("/report/create", fmt.Sprintf(`{"request_id":"deleted","comment_id":%d,"reason_code":"other"}`, deleted.ID), reporter, 404)
	var rejected int64
	if err := db.Model(&moderation.Report{}).Where("comment_id = ?", deleted.ID).Count(&rejected).Error; err != nil || rejected != 0 {
		t.Fatalf("unexpected persisted report on deleted target: count=%d, err=%v", rejected, err)
	}
	post("/report/create", strings.Replace(create, "other", "unknown", 1), reporter, 400)
	receive := func() uint {
		t.Helper()
		var response struct {
			ID uint `json:"report_id"`
		}
		rec := post("/report/create", create, reporter, 202)
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.ID == 0 {
			t.Fatal("missing report id")
		}
		return response.ID
	}
	id := receive()
	if receive() != id {
		t.Fatal("retry created a new report")
	}
	post("/report/create", strings.Replace(create, "other", "harassment", 1), reporter, 409)
	query := fmt.Sprintf(`{"report_id":%d}`, id)
	post("/report/get", query, other, 404)
	state := func() string {
		t.Helper()
		var resp struct {
			ID    uint `json:"report_id"`
			State string
		}
		r := post("/report/get", query, reporter, 200)
		if err := json.Unmarshal(r.Body.Bytes(), &resp); err != nil || resp.ID != id {
			t.Fatal("invalid report response")
		}
		return resp.State
	}
	if state() != "accepted" {
		t.Fatal("report must be accepted before workers start")
	}
	var events []moderation.Outbox
	if err := db.Where("report_id=?", id).Find(&events).Error; err != nil || len(events) != 1 || events[0].PublishedAt != nil {
		t.Fatalf("expected one pending outbox event: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var workersStopped []<-chan struct{}
	start := func(fn func(context.Context, *gorm.DB, *amqp.Channel) error) {
		t.Helper()
		ch := channel()
		done := make(chan error, 1)
		stopped := make(chan struct{})
		workersStopped = append(workersStopped, stopped)
		go func() {
			defer close(stopped)
			err := fn(ctx, db, ch)
			_ = ch.Close()
			done <- err
		}()
		t.Cleanup(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Error(err)
				}
			case <-time.After(6 * time.Second):
				t.Error("worker did not stop")
			}
		})
	}
	start(moderation.PublishReports)
	const queue = "moderation.report.created.v1"
	obs := channel()
	eventually := func(label string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !condition() {
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting for %s", label)
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	eventually("publisher confirmation", func() bool {
		var event moderation.Outbox
		if err := db.First(&event, events[0].ID).Error; err != nil {
			t.Fatal(err)
		}
		return event.PublishedAt != nil
	})
	// Discard a confirmed delivery to simulate loss after the Outbox was marked published.
	original, ok, err := obs.Get(queue, false)
	if err != nil || !ok {
		t.Fatalf("expected a routed message: %v", err)
	}
	var wire struct {
		Version  int
		EventID  uint `json:"event_id"`
		ReportID uint `json:"report_id"`
	}
	if err := json.Unmarshal(original.Body, &wire); err != nil || wire.Version != 1 || wire.ReportID != id || wire.EventID != events[0].ID || original.DeliveryMode != amqp.Persistent {
		t.Fatal("incorrect event envelope")
	}
	if err := original.Ack(false); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&events[0]).Update("published_at", time.Now().UTC().Add(-10*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	eventually("replayed report", func() bool {
		var event moderation.Outbox
		if err := db.First(&event, events[0].ID).Error; err != nil {
			t.Fatal(err)
		}
		return event.PublishedAt != nil && time.Since(*event.PublishedAt) < time.Minute
	})
	replayed, ok, err := obs.Get(queue, false)
	if err != nil || !ok || string(replayed.Body) != string(original.Body) {
		t.Fatalf("expected replay with the original event identity: %v", err)
	}
	if err := replayed.Nack(false, true); err != nil {
		t.Fatal(err)
	}
	publish := func(body []byte) {
		t.Helper()
		if err := obs.PublishWithContext(ctx, "", queue, false, false, amqp.Publishing{Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	publish(original.Body)
	// A malformed delivery behind both valid messages acts as a processing barrier.
	marker := fmt.Sprintf("invalid-%d", time.Now().UnixNano())
	publish([]byte(marker))
	start(func(ctx context.Context, db *gorm.DB, ch *amqp.Channel) error {
		return moderation.ConsumeReports(ctx, db, ch, workerCache)
	})
	eventually("dead-lettered marker", func() bool {
		d, ok, err := obs.Get(queue+".dead", true)
		if err != nil {
			t.Fatal(err)
		}
		return ok && string(d.Body) == marker
	})
	if state() != "queued" {
		t.Fatal("expected queued after consumption")
	}
	var count int64
	if err := db.Model(&moderation.Case{}).Where("report_id=?", id).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("expected one case after duplicate delivery, got %d (%v)", count, err)
	}
	mediaRequest := fmt.Sprintf(`{"comment_id":%d,"reason_code":"media_only","request_id":"media-scope"}`, comment.ID)
	mediaReceipt := post("/report/create", mediaRequest, reporter, 202)
	var media struct {
		ReportID uint `json:"report_id"`
	}
	if err := json.Unmarshal(mediaReceipt.Body.Bytes(), &media); err != nil || media.ReportID == 0 {
		t.Fatal("missing media report ID")
	}
	eventually("media report routed directly to human review", func() bool {
		var cases []moderation.Case
		if err := db.Where("report_id = ?", media.ReportID).Find(&cases).Error; err != nil {
			t.Fatal(err)
		}
		return len(cases) == 1 && cases[0].State == "awaiting_review" && cases[0].RoutingReason == "outside_text_scope"
	})
	removedTarget := video.Comment{VideoID: 1, Content: "delete after receipt"}
	if err := db.Create(&removedTarget).Error; err != nil {
		t.Fatal(err)
	}
	removalRequest := fmt.Sprintf(`{"comment_id":%d,"reason_code":"other","detail":"removal-fixture","request_id":"removed-after-receipt"}`, removedTarget.ID)
	removalReceipt := post("/report/create", removalRequest, reporter, 202)
	var removal struct {
		ReportID uint `json:"report_id"`
	}
	if err := json.Unmarshal(removalReceipt.Body.Bytes(), &removal); err != nil || removal.ReportID == 0 {
		t.Fatal("missing removed target report ID")
	}
	if err := video.NewCommentRepository(db).DeleteComment(context.Background(), &removedTarget); err != nil {
		t.Fatal(err)
	}
	calibrationModel := fmt.Sprintf("calibration-%d", id)
	autoModel := fmt.Sprintf("automatic-%d", id)
	shadowContext, stopShadow := context.WithCancel(ctx)
	shadowDone := make(chan error, 1)
	go func() {
		shadowDone <- moderation.RunShadow(shadowContext, db, func(attempt context.Context, report moderation.Report) (moderation.ShadowRun, bool) {
			if report.Detail == "removal-fixture" {
				return moderation.NewShadowEvaluator(db, nil, "test-model")(attempt, report)
			}
			if report.ReasonCode == "spam_promo" {
				return moderation.NewShadowEvaluator(db, terminalFoldModel{}, autoModel)(attempt, report)
			}
			policy, prompt := moderation.EvalVersions()
			return moderation.ShadowRun{PolicyVersion: policy, PromptVersion: prompt, RuleVersion: moderation.CandidateRuleVersion,
				Suggestion:    json.RawMessage(`{"action":"suggest_fold"}`),
				FoldCandidate: true,
				Evidence:      json.RawMessage(`[{"id":"target:1"}]`),
				Model:         calibrationModel,
			}, false
		}, workerCache)
	}()
	t.Cleanup(func() {
		stopShadow()
		select {
		case err := <-shadowDone:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("shadow worker: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("shadow worker did not stop")
		}
	})
	eventually("shadow recommendation handed to reviewer", func() bool { return state() == "awaiting_review" })
	var run moderation.ShadowRun
	if err := db.Where("case_id = (SELECT id FROM moderation_cases WHERE report_id = ?)", id).Take(&run).Error; err != nil || run.Attempt != 1 || run.Model != calibrationModel {
		t.Fatalf("missing saved shadow result: %+v (%v)", run, err)
	}
	var unchanged video.Comment
	if err := db.First(&unchanged, comment.ID).Error; err != nil || unchanged.ModerationFolded || unchanged.ModerationVersion != 0 {
		t.Fatalf("shadow recommendation mutated comment (%v)", err)
	}
	var reviewCase moderation.Case
	if err := db.Where("report_id = ?", id).Take(&reviewCase).Error; err != nil {
		t.Fatal(err)
	}
	caseQuery := fmt.Sprintf(`{"case_id":%d}`, reviewCase.ID)
	post("/moderation/detail", caseQuery, reporter, 403)
	claims, err := auth.ParseToken(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&moderation.Reviewer{AccountID: claims.AccountID}).Error; err != nil {
		t.Fatal(err)
	}
	detailBefore := post("/moderation/detail", caseQuery, other, 200)
	if strings.Contains(detailBefore.Body.String(), "suggest_fold") || !strings.Contains(detailBefore.Body.String(), `"shadow_run_count":1`) {
		t.Fatal("ordinary case detail exposed model output or missed run count")
	}
	server := httptest.NewServer(apphttp.SetRouter(db, remoteCache, nil, nil))
	defer server.Close()
	streamCtx, stopStream := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopStream()
	openStream := func(token string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, server.URL+"/moderation/stream", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	denied := openStream(reporter)
	denied.Body.Close()
	if denied.StatusCode != 403 {
		t.Fatalf("non-reviewer stream status %d", denied.StatusCode)
	}
	stream := openStream(other)
	defer stream.Body.Close()
	if stream.StatusCode != 200 || !strings.HasPrefix(stream.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("unexpected stream response: %d", stream.StatusCode)
	}
	reader := bufio.NewScanner(stream.Body)
	waitForChange := func(expected string) {
		t.Helper()
		for reader.Scan() {
			if reader.Text() == expected {
				return
			}
		}
		t.Fatalf("stream ended before change notification: %v", reader.Err())
	}
	waitForChange("data: {}")
	fold := fmt.Sprintf(`{"case_id":%d,"case_version":%d,"target_version":0,"action":"fold","policy_id":"SPAM_PROMO","reason":"测试折叠","request_id":"fold"}`, reviewCase.ID, reviewCase.Version)
	first := post("/moderation/decide", fold, other, 200)
	if !strings.Contains(first.Body.String(), `"independent_review":true`) {
		t.Fatal("blind recommendation review was not recorded")
	}
	metrics := post("/moderation/shadow-metrics", fmt.Sprintf(`{"model":%q}`, calibrationModel), other, 200)
	if !strings.Contains(metrics.Body.String(), `"attempts":1`) || !strings.Contains(metrics.Body.String(), `"awaiting_review":1`) {
		t.Fatalf("unexpected operational metrics: %s", metrics.Body.String())
	}
	calibration := post("/moderation/calibration", fmt.Sprintf(`{"model":%q}`, calibrationModel), other, 200)
	if !strings.Contains(calibration.Body.String(), `"shadow_confirmed":1`) || !strings.Contains(calibration.Body.String(), `"shadow_gate_passed":false`) {
		t.Fatalf("unexpected calibration counts: %s", calibration.Body.String())
	}
	suggestions := post("/moderation/suggestions", caseQuery, other, 200)
	if !strings.Contains(suggestions.Body.String(), "suggest_fold") {
		t.Fatal("explicit reveal missed recommendation")
	}
	waitForChange(fmt.Sprintf(`data: {"case_id":%d,"version":%d,"type":"decided"}`, reviewCase.ID, reviewCase.Version+1))
	retry := post("/moderation/decide", fold, other, 200)
	if first.Body.String() != retry.Body.String() {
		t.Fatal("duplicate decision did not return the original result")
	}
	post("/moderation/decide", strings.ReplaceAll(fold, `"fold"`, `"restore"`), other, 409)
	if state() != "resolved" {
		t.Fatal("expected resolved after reviewer decision")
	}
	listInput := fmt.Sprintf(`{"video_id":%d}`, baseVideo.ID)
	list := post("/comment/listThreads", listInput, "", 200)
	if strings.Contains(list.Body.String(), comment.Content) || !strings.Contains(list.Body.String(), `"moderation_folded":true`) {
		t.Fatal("public response did not mask the folded comment")
	}
	reveal := post("/comment/reveal", fmt.Sprintf(`{"comment_id":%d}`, comment.ID), "", 200)
	if !strings.Contains(reveal.Body.String(), comment.Content) {
		t.Fatal("explicit reveal did not return the folded text")
	}

	legacy := post("/comment/listAll", listInput, "", 200)
	if strings.Contains(legacy.Body.String(), comment.Content) || !strings.Contains(legacy.Body.String(), `"moderation_folded":true`) {
		t.Fatal("legacy comment list exposed the folded text or omitted its status")
	}
	foldedReply := video.Comment{VideoID: baseVideo.ID, RootCommentID: &comment.ID, ReplyToCommentID: &comment.ID, Content: "reply text hidden by reviewer"}
	if err := db.Create(&foldedReply).Error; err != nil {
		t.Fatal(err)
	}
	var replyReceipt struct {
		ID uint `json:"report_id"`
	}
	replyRequest := fmt.Sprintf(`{"comment_id":%d,"reason_code":"other","request_id":"fold-reply"}`, foldedReply.ID)
	replyResponse := post("/report/create", replyRequest, other, 202)
	if err := json.Unmarshal(replyResponse.Body.Bytes(), &replyReceipt); err != nil || replyReceipt.ID == 0 {
		t.Fatal("missing reply report ID")
	}
	var replyCase moderation.Case
	eventually("reply ready for human review", func() bool {
		var rows []moderation.Case
		if err := db.Where("report_id = ?", replyReceipt.ID).Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 && rows[0].State == "awaiting_review" {
			replyCase = rows[0]
			return true
		}
		return false
	})
	replyDecision := fmt.Sprintf(`{"case_id":%d,"case_version":%d,"target_version":0,"action":"fold","reason":"折叠回复","request_id":"fold-reply-decision"}`, replyCase.ID, replyCase.Version)
	post("/moderation/decide", replyDecision, other, 200)
	replies := post("/comment/listReplies", fmt.Sprintf(`{"video_id":%d,"root_comment_id":%d}`, baseVideo.ID, comment.ID), "", 200)
	var page struct {
		Comments []video.Comment `json:"comments"`
	}
	if err := json.Unmarshal(replies.Body.Bytes(), &page); err != nil || len(page.Comments) != 1 || page.Comments[0].ID != foldedReply.ID ||
		page.Comments[0].Content != "" || !page.Comments[0].ModerationFolded || page.Comments[0].ReplyToCommentID == nil || *page.Comments[0].ReplyToCommentID != comment.ID {
		t.Fatalf("reply list did not retain the masked reply and its parent: %+v (%v)", page, err)
	}
	explicitReply := post("/comment/reveal", fmt.Sprintf(`{"comment_id":%d}`, foldedReply.ID), "", 200)
	if !strings.Contains(explicitReply.Body.String(), foldedReply.Content) {
		t.Fatal("explicit reveal did not return the reply")
	}

	// Test-only approval data exercises the write path with a disposable database.
	policy, prompt := moderation.EvalVersions()
	approval := moderation.Rollout{PolicyID: "SPAM_PROMO", Model: autoModel, PolicyVersion: policy, PromptVersion: prompt, RuleVersion: moderation.CandidateRuleVersion,
		DatasetVersion: "integration-fixture", DatasetSHA256: strings.Repeat("1", 64), DevelopmentSHA256: strings.Repeat("2", 64), ReportSHA256: strings.Repeat("3", 64), Percent: 100, NormalCases: 100, SpamCases: 30, CorrectSpamCandidates: 24, InjectionCases: 20, InjectionBlocked: 20}
	if err := db.Save(&approval).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Delete(&approval).Error; err != nil {
			t.Error(err)
		}
	})
	calibrationSeed := func(n int) {
		t.Helper()
		if err := db.Transaction(func(tx *gorm.DB) error {
			for range n {
				comment := video.Comment{VideoID: 1, Content: "seeded verification record"}
				if err := tx.Create(&comment).Error; err != nil {
					return err
				}
				report := moderation.Report{ReporterID: claims.AccountID, CommentID: comment.ID, ReasonCode: "spam_promo", RequestID: fmt.Sprintf("seed-%d", comment.ID)}
				if err := tx.Create(&report).Error; err != nil {
					return err
				}
				item := moderation.Case{ReportID: report.ID, State: "resolved"}
				if err := tx.Create(&item).Error; err != nil {
					return err
				}
				run := moderation.ShadowRun{CaseID: item.ID, Attempt: 1, FoldCandidate: true, Model: approval.Model, PolicyVersion: policy, PromptVersion: prompt, RuleVersion: moderation.CandidateRuleVersion}
				if err := tx.Create(&run).Error; err != nil {
					return err
				}
				decision := moderation.Decision{CaseID: item.ID, ReviewerID: claims.AccountID, RequestID: fmt.Sprintf("seed-%d", comment.ID), ShadowRunID: &run.ID, IndependentReview: true, Action: "fold", PolicyID: "SPAM_PROMO"}
				if err := tx.Create(&decision).Error; err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	gated := video.Comment{VideoID: 1, Content: "加群领返现，群号见主页"}
	if err := db.Create(&gated).Error; err != nil {
		t.Fatal(err)
	}
	request := fmt.Sprintf(`{"comment_id":%d,"reason_code":"spam_promo","request_id":"insufficient-live-calibration"}`, gated.ID)
	var gatedReport struct {
		ID uint `json:"report_id"`
	}
	response := post("/report/create", request, reporter, 202)
	if err := json.Unmarshal(response.Body.Bytes(), &gatedReport); err != nil || gatedReport.ID == 0 {
		t.Fatal("missing gated report ID")
	}
	eventually("unconfirmed candidate sent to manual review", func() bool {
		var cases []moderation.Case
		if err := db.Where("report_id = ?", gatedReport.ID).Find(&cases).Error; err != nil {
			t.Fatal(err)
		}
		return len(cases) == 1 && cases[0].State == "awaiting_review"
	})
	var gatedRun moderation.ShadowRun
	if err := db.Where("case_id = (SELECT id FROM moderation_cases WHERE report_id = ?)", gatedReport.ID).Take(&gatedRun).Error; err != nil || gatedRun.ExecutionOutcome != "calibration_pending" {
		t.Fatalf("expected calibration referral, got %q (%v)", gatedRun.ExecutionOutcome, err)
	}
	calibrationSeed(30)
	promo := video.Comment{VideoID: 1, AuthorID: claims.AccountID, Content: "加群领返现，群号见主页"}
	if err := db.Create(&promo).Error; err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf(`{"comment_id":%d,"reason_code":"spam_promo","request_id":"approved-fold"}`, promo.ID)
	var submitted struct {
		ReportID uint `json:"report_id"`
	}
	response = post("/report/create", raw, reporter, 202)
	if err := json.Unmarshal(response.Body.Bytes(), &submitted); err != nil || submitted.ReportID == 0 {
		t.Fatal("missing approved report ID")
	}
	var automated moderation.Case
	eventually("approved automatic fold", func() bool {
		var rows []moderation.Case
		if err := db.Where("report_id = ?", submitted.ReportID).Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 && rows[0].State == "awaiting_review" {
			var trace moderation.ShadowRun
			if err := db.Where("case_id = ?", rows[0].ID).Last(&trace).Error; err != nil {
				t.Fatal(err)
			}
			t.Fatalf("approved candidate sent to manual review: model=%q failure=%q candidate=%t evidence=%s", trace.Model, trace.Failure, trace.FoldCandidate, trace.Evidence)
		}
		if len(rows) != 1 || rows[0].State != "resolved" {
			return false
		}
		automated = rows[0]
		return true
	})
	var automatic moderation.Decision
	if err := db.Where("case_id = ?", automated.ID).Take(&automatic).Error; err != nil || automatic.ReviewerID != 0 || automatic.ShadowRunID == nil || automatic.Action != "fold" {
		t.Fatalf("missing automatic audit record: %+v (%v)", automatic, err)
	}
	var appliedRun moderation.ShadowRun
	if err := db.First(&appliedRun, *automatic.ShadowRunID).Error; err != nil || appliedRun.ExecutionOutcome != "applied" || appliedRun.ModelCalls != 1 {
		t.Fatalf("missing applied terminal execution: outcome=%q calls=%d (%v)", appliedRun.ExecutionOutcome, appliedRun.ModelCalls, err)
	}
	if err := db.First(&promo, promo.ID).Error; err != nil || !promo.ModerationFolded {
		t.Fatalf("automatic fold did not change display state: %v", err)
	}
	duplicateTarget := fmt.Sprintf(`{"comment_id":%d,"reason_code":"spam_promo","request_id":"same-target-new-reporter"}`, promo.ID)
	var relatedReport struct {
		ID uint `json:"report_id"`
	}
	relatedResponse := post("/report/create", duplicateTarget, other, 202)
	if err := json.Unmarshal(relatedResponse.Body.Bytes(), &relatedReport); err != nil || relatedReport.ID == 0 {
		t.Fatal("missing second report ID")
	}
	eventually("existing disposition linked to second report", func() bool {
		var rows []moderation.Case
		if err := db.Where("report_id = ?", relatedReport.ID).Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 || rows[0].State != "resolved" {
			return false
		}
		var linked moderation.Decision
		if err := db.Where("case_id = ?", rows[0].ID).Take(&linked).Error; err != nil {
			t.Fatal(err)
		}
		if linked.RelatedDecisionID == nil || *linked.RelatedDecisionID != automatic.ID {
			t.Fatalf("expected original disposition link: %+v", linked)
		}
		return true
	})
	var linkedTarget video.Comment
	if err := db.First(&linkedTarget, promo.ID).Error; err != nil || linkedTarget.ModerationVersion != promo.ModerationVersion {
		t.Fatal("second report changed target version")
	}
	var authorNotices int64
	if err := db.Table("notifications").Where("type = ? AND target_id = ?", "moderation_comment", promo.ID).Count(&authorNotices).Error; err != nil || authorNotices != 1 {
		t.Fatalf("second report yielded duplicate author notification: %d (%v)", authorNotices, err)
	}
	restore := fmt.Sprintf(`{"case_id":%d,"case_version":%d,"target_version":%d,"action":"restore","false_fold":true,"reason":"确认误折叠","request_id":"restore-auto"}`, automated.ID, automated.Version, promo.ModerationVersion)
	restored := post("/moderation/decide", restore, other, 200)
	restoredRetry := post("/moderation/decide", restore, other, 200)
	if !strings.Contains(restored.Body.String(), `"false_fold":true`) || restored.Body.String() != restoredRetry.Body.String() {
		t.Fatal("false fold decision was not audited or idempotent")
	}
	if err := db.First(&approval, "policy_id = ?", "SPAM_PROMO").Error; err != nil || approval.Percent != 0 {
		t.Fatalf("confirmed false fold did not pause rollout: %d (%v)", approval.Percent, err)
	}
	if err := db.First(&promo, promo.ID).Error; err != nil || promo.ModerationFolded {
		t.Fatalf("manual override did not restore display state: %v", err)
	}

	pausedTarget := video.Comment{VideoID: 1, AuthorID: claims.AccountID, Content: promo.Content}
	if err := db.Create(&pausedTarget).Error; err != nil {
		t.Fatal(err)
	}
	request = fmt.Sprintf(`{"comment_id":%d,"reason_code":"spam_promo","request_id":"paused-rollout"}`, pausedTarget.ID)
	response = post("/report/create", request, token("paused-reporter"), 202)
	var pausedReport struct {
		ID uint `json:"report_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &pausedReport); err != nil || pausedReport.ID == 0 {
		t.Fatal("missing paused rollout report ID")
	}
	var pausedRun moderation.ShadowRun
	eventually("approved intent referred after rollout pause", func() bool {
		err := db.Where("case_id = (SELECT id FROM moderation_cases WHERE report_id = ?)", pausedReport.ID).Take(&pausedRun).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		return true
	})
	if !pausedRun.FoldCandidate || pausedRun.ResultState != "awaiting_review" || pausedRun.ExecutionOutcome != "rollout_ineligible" {
		t.Fatalf("paused rollout produced unexpected result: candidate=%t state=%q outcome=%q", pausedRun.FoldCandidate, pausedRun.ResultState, pausedRun.ExecutionOutcome)
	}
	var pausedDecisions int64
	if err := db.Model(&moderation.Decision{}).Where("case_id = ?", pausedRun.CaseID).Count(&pausedDecisions).Error; err != nil || pausedDecisions != 0 {
		t.Fatalf("paused rollout wrote %d decisions (%v)", pausedDecisions, err)
	}
	if err := db.First(&pausedTarget, pausedTarget.ID).Error; err != nil || pausedTarget.ModerationFolded {
		t.Fatalf("paused rollout changed display state (%v)", err)
	}

	removalQuery := fmt.Sprintf(`{"report_id":%d}`, removal.ReportID)
	eventually("deleted target public result", func() bool {
		rec := post("/report/get", removalQuery, reporter, 200)
		return strings.Contains(rec.Body.String(), `"result":"target_removed"`) && strings.Contains(rec.Body.String(), `"state":"resolved"`)
	})
	var noticeCount int64
	if err := db.Table("notifications").Where("target_id = ? AND type = ?", removal.ReportID, "moderation_report").Count(&noticeCount).Error; err != nil || noticeCount != 1 {
		t.Fatalf("expected one removed target notice: %d (%v)", noticeCount, err)
	}
	post("/comment/reveal", fmt.Sprintf(`{"comment_id":%d}`, removedTarget.ID), "", 404)

	cancel()
	for _, stop := range workersStopped {
		select {
		case <-stop:
		case <-time.After(6 * time.Second):
			t.Fatal("old broker channel did not close before reconnect rehearsal")
		}
	}
	reconnectCtx, stopReconnect := context.WithCancel(context.Background())
	defer stopReconnect()
	connections := make(chan *amqp.Connection, 2)
	releaseDial := make(chan struct{})
	supervisorDone := make(chan struct{})
	go func() {
		defer close(supervisorDone)
		first := true
		moderation.RunReportQueue(reconnectCtx, func() (*amqp.Connection, error) {
			if !first {
				select {
				case <-releaseDial:
				case <-reconnectCtx.Done():
					return nil, reconnectCtx.Err()
				}
			}
			first = false
			current, err := amqp.Dial(uri)
			if err == nil {
				select {
				case connections <- current:
				case <-reconnectCtx.Done():
					current.Close()
					return nil, reconnectCtx.Err()
				}
			}
			return current, err
		}, db, workerCache)
	}()
	nextConnection := func() *amqp.Connection {
		t.Helper()
		select {
		case current := <-connections:
			return current
		case <-time.After(8 * time.Second):
			t.Fatal("broker connection did not recover")
			return nil
		}
	}
	firstConnection := nextConnection()
	if err := firstConnection.Close(); err != nil {
		t.Fatal(err)
	}
	backlog := fmt.Sprintf(`{"comment_id":%d,"reason_code":"other","request_id":"broker-reconnect"}`, comment.ID)
	var disconnected struct {
		ReportID uint `json:"report_id"`
	}
	rec := post("/report/create", backlog, reporter, 202)
	if err := json.Unmarshal(rec.Body.Bytes(), &disconnected); err != nil || disconnected.ReportID == 0 {
		t.Fatal("missing report during reconnect")
	}
	reconnectQuery := fmt.Sprintf(`{"report_id":%d}`, disconnected.ReportID)
	if !strings.Contains(post("/report/get", reconnectQuery, reporter, 200).Body.String(), `"state":"accepted"`) {
		t.Fatal("expected pending receipt during disconnect")
	}
	close(releaseDial)
	secondConnection := nextConnection()
	if secondConnection == firstConnection {
		t.Fatal("broker session reused terminated connection")
	}
	eventually("case created after broker reconnect", func() bool {
		return strings.Contains(post("/report/get", reconnectQuery, reporter, 200).Body.String(), `"state":"queued"`)
	})
	var recoveredCount int64
	if err := db.Model(&moderation.Case{}).Where("report_id = ?", disconnected.ReportID).Count(&recoveredCount).Error; err != nil || recoveredCount != 1 {
		t.Fatalf("recovered report created %d cases (%v)", recoveredCount, err)
	}
	secondReviewer := token("second-reviewer")
	secondClaims, err := auth.ParseToken(secondReviewer)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&moderation.Reviewer{AccountID: secondClaims.AccountID}).Error; err != nil {
		t.Fatal(err)
	}
	var competingCase moderation.Case
	if err := db.Where("report_id = ?", disconnected.ReportID).Take(&competingCase).Error; err != nil {
		t.Fatal(err)
	}
	var competingTarget video.Comment
	if err := db.First(&competingTarget, comment.ID).Error; err != nil {
		t.Fatal(err)
	}
	decisionPayload := fmt.Sprintf(`{"case_id":%d,"case_version":%d,"target_version":%d,"action":"keep","reason":"并发终审","request_id":"concurrent-keep"}`, competingCase.ID, competingCase.Version, competingTarget.ModerationVersion)
	begin := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	for _, identity := range []string{other, secondReviewer} {
		go func() {
			<-begin
			req := httptest.NewRequest(http.MethodPost, "/moderation/decide", strings.NewReader(decisionPayload))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+identity)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			responses <- response
		}()
	}
	close(begin)
	statuses := map[int]int{}
	for range 2 {
		select {
		case response := <-responses:
			statuses[response.Code]++
		case <-time.After(6 * time.Second):
			t.Fatal("concurrent decisions did not finish")
		}
	}
	if statuses[200] != 1 || statuses[409] != 1 {
		t.Fatalf("expected one commit and one conflict, got %v", statuses)
	}
	var decisionCount int64
	if err := db.Model(&moderation.Decision{}).Where("case_id = ?", competingCase.ID).Count(&decisionCount).Error; err != nil || decisionCount != 1 {
		t.Fatalf("concurrent request wrote %d decisions (%v)", decisionCount, err)
	}
	if err := db.Table("notifications").Where("target_id = ? AND type = ?", disconnected.ReportID, "moderation_report").Count(&noticeCount).Error; err != nil || noticeCount != 1 {
		t.Fatalf("concurrent request wrote %d settlement notifications (%v)", noticeCount, err)
	}
	revocationContext, stopRevocation := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopRevocation()
	revocationRequest, err := http.NewRequestWithContext(revocationContext, http.MethodGet, server.URL+"/moderation/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	revocationRequest.Header.Set("Authorization", "Bearer "+secondReviewer)
	revocationStream, err := server.Client().Do(revocationRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer revocationStream.Body.Close()
	if revocationStream.StatusCode != 200 {
		t.Fatalf("unexpected initial stream status %d", revocationStream.StatusCode)
	}
	revocationReader := bufio.NewScanner(revocationStream.Body)
	ready := false
	for revocationReader.Scan() {
		if revocationReader.Text() == "data: {}" {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("stream did not send its initial snapshot invalidation")
	}
	if err := db.Delete(&moderation.Reviewer{AccountID: secondClaims.AccountID}).Error; err != nil {
		t.Fatal(err)
	}
	event := fmt.Sprintf(`{"case_id":%d,"version":%d,"type":"decided"}`, competingCase.ID, competingCase.Version+1)
	if err := workerCache.Publish(revocationContext, "moderation:review-events", []byte(event)); err != nil {
		t.Fatal(err)
	}
	for revocationReader.Scan() {
		if strings.HasPrefix(revocationReader.Text(), "data:") {
			t.Fatal("revoked reviewer received a later event")
		}
	}
	if err := revocationReader.Err(); err != nil {
		t.Fatalf("stream was not closed by server before deadline: %v", err)
	}
	post("/moderation/list", `{}`, secondReviewer, 403)
	stopReconnect()
	select {
	case <-supervisorDone:
	case <-time.After(5 * time.Second):
		t.Fatal("broker supervisor did not stop")
	}

}

type terminalFoldModel struct{ model.ToolCallingChatModel }

func (terminalFoldModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return terminalFoldModel{}, nil
}

func (terminalFoldModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage("", []schema.ToolCall{{
		ID: "fold-1", Type: "function", Function: schema.FunctionCall{
			Name:      "submit_review_decision",
			Arguments: `{"action":"fold","policy_id":"SPAM_PROMO","confidence":0.92,"evidence_ids":["target:1"],"reason":"规则核对通过"}`,
		},
	}}), nil
}
