package worker

import (
	"context"
	"encoding/json"
	"feedsystem_video_go/internal/middleware/rabbitmq"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/video"
	"fmt"
	"log"
	"time"

	oredis "github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// StartOutboxPoller 启动一个轮询器，定期从数据库中查询状态为 "pending" 的消息，并将其投递到 Timeline MQ 中，确保视频发布事件能够被订阅者消费。
func StartOutboxPoller(db *gorm.DB, tmq *rabbitmq.TimelineMQ) {
	if db == nil || tmq == nil {
		log.Printf("Outbox poller disabled: timeline mq is not initialized")
		return
	}

	go func() {
		for {
			var messages []video.OutboxMsg

			// 这里Find方法会返回一个切片，包含所有状态为 "pending" 的消息，按创建时间升序排列，并限制返回数量为 100 条，赋值给 messages 变量
			err := db.Where("status = ?", "pending").Order("create_time ASC").Limit(100).Find(&messages).Error

			if err != nil || len(messages) == 0 {
				time.Sleep(1 * time.Second) // 设置一个合理的轮询间隔，避免频繁查询数据库
				continue
			}

			// 将消息进行序列化为 JSON 格式，并调用 PublishVideo 投递到消息队列，如果投递成功，则从数据库中删除该消息；如果投递失败，则记录日志，等待下一次轮询再尝试投递
			for _, msg := range messages {
				err := tmq.PublishVideo(context.Background(), msg.VideoID, msg.CreateTime)
				if err == nil {
					if err := db.Delete(&msg).Error; err != nil {
						log.Printf("删除 outbox 消息失败: id=%d, err=%v", msg.ID, err)
					}
				} else {
					log.Printf("投递MQ失败: VideoID: %d, err: %v", msg.VideoID, err)
				}
			}
		}
	}()
}

// StartConsumer 启动一个消费者，监听 Timeline MQ 中的视频发布事件，并将其写入 Redis 的 ZSet 中，供 Feed 服务使用。
func StartConsumer(tmq *rabbitmq.TimelineMQ, queueName string, redisClient *rediscache.Client, rmq *rabbitmq.RabbitMQ) {
	if tmq == nil || rmq == nil || rmq.Conn == nil {
		log.Printf("Timeline consumer disabled: rabbitmq is not initialized")
		return
	}
	if redisClient == nil {
		log.Printf("Timeline consumer disabled: redis is not initialized")
		return
	}

	go func() {
		for {
			// 每次重连创建独立的 Channel，不与发布者共用
			ch, err := rmq.NewChannel()
			if err != nil {
				log.Printf("Timeline consumer: 创建 Channel 失败: %v, 5秒后重试", err)
				time.Sleep(5 * time.Second)
				continue
			}

			if err := ch.Qos(10, 0, false); err != nil {
				log.Printf("Timeline consumer: QoS 设置失败: %v", err)
			}

			// 真正的消费逻辑，注册消费者，获取消息 channel
			msgs, err := ch.Consume(queueName, "", false, false, false, false, nil)
			if err != nil {
				log.Printf("Timeline consumer: 注册消费失败: %v, 5秒后重试", err)
				ch.Close()
				time.Sleep(5 * time.Second)
				continue
			}

			log.Printf("Timeline consumer 已启动, queue=%s", queueName)

			for msg := range msgs {
				var event rabbitmq.TimelineEvent
				if err := json.Unmarshal(msg.Body, &event); err != nil {
					log.Printf("Timeline consumer: 反序列化失败: %v", err)
					msg.Ack(false)
					continue
				}

				// Redis 写入逻辑，使用 ZSet 存储视频 ID 和发布时间戳，供 Feed 服务按时间顺序拉取视频
				ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
				timelineKey := redisClient.Key("feed:global_timeline")
				// err 直接赋值给 err 变量，避免重复声明
				err = redisClient.ZAdd(ctx, timelineKey, oredis.Z{
					Score:  float64(event.CreateTime),
					Member: fmt.Sprintf("%d", event.VideoID),
				})
				if err != nil { // 如果写入失败，记录日志，并将消息重新入队，等待下一次消费
					log.Printf("Timeline consumer: 写入Zset失败: %v", err)
					msg.Nack(false, true)
					cancel()
					continue
				}

				// 为了防止 ZSet 无限增长，限制其长度为 1000 条，超过的部分删除
				if err := redisClient.ZRemRangeByRank(ctx, timelineKey, 0, -1001); err != nil {
					log.Printf("Timeline consumer: ZRem失败: %v", err)
				}

				msg.Ack(false)
				cancel()
			}

			// msgs channel 关闭说明 AMQP Channel 断开，关闭并重连
			ch.Close()
			log.Printf("Timeline consumer: Channel 断开, 5秒后重连...")
			time.Sleep(5 * time.Second)
		}
	}()
}
