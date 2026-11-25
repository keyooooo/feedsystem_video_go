package moderation

import (
	"context"
	"log"
	"time"

	rediscache "feedsystem_video_go/internal/middleware/redis"
	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

// RunReportQueue replaces the broker connection when either moderation
// channel stops. Pending reports remain in MySQL while it reconnects.
func RunReportQueue(ctx context.Context, dial func() (*amqp.Connection, error), db *gorm.DB, cache *rediscache.Client) {
	for ctx.Err() == nil {
		conn, err := dial()
		if err == nil {
			runReportConnection(ctx, conn, db, cache)
			_ = conn.Close()
		} else if ctx.Err() == nil {
			log.Printf("Moderation broker connection unavailable (%T)", err)
		}
		retry := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			retry.Stop()
			return
		case <-retry.C:
		}
	}
}

func runReportConnection(ctx context.Context, conn *amqp.Connection, db *gorm.DB, cache *rediscache.Client) {
	publish, err := conn.Channel()
	if err != nil {
		return
	}
	defer publish.Close()
	consume, err := conn.Channel()
	if err != nil {
		return
	}
	defer consume.Close()
	if err := consume.Qos(20, 0, false); err != nil {
		return
	}
	session, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{}, 2)
	go func() { defer func() { done <- struct{}{} }(); _ = PublishReports(session, db, publish) }()
	go func() { defer func() { done <- struct{}{} }(); _ = ConsumeReports(session, db, consume, cache) }()
	<-done
	cancel()
	<-done
}
