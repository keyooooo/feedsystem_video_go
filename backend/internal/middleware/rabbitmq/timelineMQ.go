package rabbitmq

import (
	"context"
	"errors"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type TimelineMQ struct {
	ch *amqp.Channel
}

const (
	timelineExchange   = "video.timeline.events"
	timelineQueue      = "video.timeline.update.queue"
	timelineBindingKey = "video.timeline.*"
	timelinePublishRK  = "video.timeline.publish"
)

type TimelineEvent struct {
	EventID    string    `json:"event_id"`
	VideoID    uint      `json:"video_id"`
	CreateTime int64     `json:"create_time"`
	OccurredAt time.Time `json:"occurred_at"`
}

// NewTimelineMQ 创建一个新的 TimelineMQ 实例，接收一个 RabbitMQ 基础连接，创建一个独立的 Channel，并声明相关的 exchange、queue 和 binding。
func NewTimelineMQ(base *RabbitMQ) (*TimelineMQ, error) {
	if base == nil {
		return nil, errors.New("rabbitmq base is nil")
	}
	ch, err := base.NewChannel()
	if err != nil {
		return nil, err
	}
	if err := DeclareTopic(ch, timelineExchange, timelineQueue, timelineBindingKey); err != nil {
		ch.Close()
		return nil, err
	}
	return &TimelineMQ{ch: ch}, nil
}

// PublishVideo 发布视频事件到 Timeline MQ，供订阅者（如 Feed 服务）消费
func (t *TimelineMQ) PublishVideo(ctx context.Context, videoID uint, createTime time.Time) error {
	if t == nil || t.ch == nil {
		return errors.New("timeline mq is not initialized")
	}
	if videoID == 0 {
		return errors.New("videoID are required")
	}
	id, err := newEventID(16)
	if err != nil {
		return err
	}
	timeline := TimelineEvent{
		EventID:    id,
		VideoID:    videoID,
		CreateTime: createTime.UnixMilli(),
		OccurredAt: time.Now(),
	}
	return PublishJSON(ctx, t.ch, timelineExchange, timelinePublishRK, timeline)
}
