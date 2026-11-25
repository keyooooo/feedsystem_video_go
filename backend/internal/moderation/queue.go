package moderation

import (
	"context"
	"encoding/json"
	"errors"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	queueName         = "moderation.report.created.v1"
	reportReplayAfter = 5 * time.Minute
)

type reportEvent struct {
	Version  int  `json:"version"`
	EventID  uint `json:"event_id"`
	ReportID uint `json:"report_id"`
}

func declareQueue(ch *amqp.Channel) error {
	if _, err := ch.QueueDeclare(queueName+".dead", true, false, false, false, nil); err != nil {
		return err
	}
	_, err := ch.QueueDeclare(queueName, true, false, false, false, amqp.Table{"x-dead-letter-exchange": "", "x-dead-letter-routing-key": queueName + ".dead"})
	return err
}

func PublishReports(ctx context.Context, db *gorm.DB, ch *amqp.Channel) error {
	if err := declareQueue(ch); err != nil {
		return err
	}
	if err := ch.Confirm(false); err != nil {
		return err
	}
	returns := ch.NotifyReturn(make(chan amqp.Return, 1))
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var batch []Outbox
		// A broker confirmation proves routing, while a case proves processing.
		replayBefore := time.Now().UTC().Add(-reportReplayAfter)
		err := db.WithContext(ctx).
			Where("published_at IS NULL OR (published_at <= ? AND NOT EXISTS (SELECT 1 FROM moderation_cases WHERE moderation_cases.report_id = moderation_outbox.report_id))", replayBefore).
			Order("COALESCE(published_at, created_at) ASC, id ASC").
			Limit(32).Find(&batch).Error
		if err != nil {
			return err
		}
		for _, item := range batch {
			body, err := json.Marshal(reportEvent{Version: 1, EventID: item.ID, ReportID: item.ReportID})
			if err != nil {
				return err
			}
			confirm, err := ch.PublishWithDeferredConfirmWithContext(ctx, "", queueName, true, false, amqp.Publishing{ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: body})
			if err != nil {
				return err
			}
			deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
			ok, err := confirm.WaitContext(deadline)
			cancel()
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("report event was not confirmed")
			}
			select {
			case <-returns:
				return errors.New("report event was returned")
			default:
			}
			if err = db.WithContext(ctx).Model(&item).Update("published_at", time.Now().UTC()).Error; err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func ConsumeReports(ctx context.Context, db *gorm.DB, ch *amqp.Channel, cache *rediscache.Client) error {
	if err := declareQueue(ch); err != nil {
		return err
	}
	stream, err := ch.Consume(queueName, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case delivery, ok := <-stream:
			if !ok {
				return errors.New("report queue disconnected")
			}
			var evt reportEvent
			if json.Unmarshal(delivery.Body, &evt) != nil || evt.Version != 1 || evt.EventID == 0 || evt.ReportID == 0 {
				if err := delivery.Reject(false); err != nil {
					return err
				}
				continue
			}
			var created Case
			err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				var event Outbox
				if err := tx.Where("id=? AND report_id=?", evt.EventID, evt.ReportID).First(&event).Error; err != nil {
					return err
				}
				var report Report
				if err := tx.Select("reason_code").First(&report, event.ReportID).Error; err != nil {
					return err
				}
				created = Case{ReportID: event.ReportID, State: "queued"}
				if report.ReasonCode == mediaReportReason {
					created.State = "awaiting_review"
					created.RoutingReason = "outside_text_scope"
				}
				return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&created).Error
			})
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := delivery.Reject(false); err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			publishReviewEvent(ctx, cache, reviewEvent{CaseID: created.ID, Type: "created"})
			if err := delivery.Ack(false); err != nil {
				return err
			}
		}
	}
}
