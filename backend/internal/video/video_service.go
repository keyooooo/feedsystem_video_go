package video

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"feedsystem_video_go/internal/apierror"
	"feedsystem_video_go/internal/middleware/rabbitmq"
	rediscache "feedsystem_video_go/internal/middleware/redis"

	"gorm.io/gorm"
)

type VideoService struct {
	repo         *VideoRepository
	cache        *rediscache.Client
	cacheTTL     time.Duration
	popularityMQ *rabbitmq.PopularityMQ
}

func NewVideoService(repo *VideoRepository, cache *rediscache.Client, popularityMQ *rabbitmq.PopularityMQ) *VideoService {
	return &VideoService{repo: repo, cache: cache, cacheTTL: 5 * time.Minute, popularityMQ: popularityMQ}
}

// Publish 发布视频，包含视频信息写入数据库、消息写入本地消息表、标签提取与创建等操作，确保数据一致性。
func (vs *VideoService) Publish(ctx context.Context, video *Video) error {
	if video == nil {
		return errors.New("video is nil")
	}
	video.Title = strings.TrimSpace(video.Title)
	video.PlayURL = strings.TrimSpace(video.PlayURL)
	video.CoverURL = strings.TrimSpace(video.CoverURL)

	if video.Title == "" {
		return errors.New("title is required")
	}
	if video.PlayURL == "" {
		return errors.New("play url is required")
	}
	if video.CoverURL == "" {
		return errors.New("cover url is required")
	}

	//使用OutboxMsg模式，事务保证视频写入库和消息写入本地消息表的一致性
	err := vs.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 写入视频表
		if err := tx.Create(video).Error; err != nil {
			return err
		}

		// 2. 写入本地消息表
		msg := OutboxMsg{
			VideoID:    video.ID,
			EventType:  "video_published",
			Status:     "pending",
			CreateTime: video.CreateTime,
		}

		if err := tx.Create(&msg).Error; err != nil {
			return err
		}

		// 3. 提取并创建标签
		tags := ExtractTags(video.Title + " " + video.Description)
		for _, tagName := range tags {
			var tag Tag
			if err := tx.Where("name = ?", tagName).FirstOrCreate(&tag, Tag{Name: tagName}).Error; err != nil {
				return err
			}
			if err := tx.Create(&VideoTag{VideoID: video.ID, TagID: tag.ID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return err

}

func (vs *VideoService) Delete(ctx context.Context, id uint, authorID uint) error {
	video, err := vs.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if video == nil {
		return errors.New("video not found")
	}
	if video.AuthorID != authorID {
		return apierror.ErrUnauthorized
	}
	if err := vs.repo.DeleteVideo(ctx, id); err != nil {
		return err
	}
	if vs.cache != nil {
		cacheKey := vs.cache.Key("video:detail:id=%d", id)
		_ = vs.cache.Del(context.Background(), cacheKey)
	}
	return nil
}

func (vs *VideoService) ListByAuthorID(ctx context.Context, authorID uint) ([]Video, error) {
	videos, err := vs.repo.ListByAuthorID(ctx, int64(authorID))
	if err != nil {
		return nil, err
	}
	return videos, nil
}

// GetDetail 获取视频详情，采用redis缓存，支持缓存击穿保护和分布式锁机制，确保高并发下的数据一致性和性能。
func (vs *VideoService) GetDetail(ctx context.Context, id uint) (*Video, error) {

	// 如果没有配置 Redis 缓存，直接从数据库获取视频详情
	if vs.cache == nil {
		video, err := vs.repo.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		return video, nil
	}

	// 构建缓存 key
	cacheKey := vs.cache.Key("video:detail:id=%d", id)

	// 定义获取缓存的函数getCached，参数为上下文，返回视频对象和是否命中缓存的布尔值
	getCached := func() (*Video, bool) {
		opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		b, err := vs.cache.GetBytes(opCtx, cacheKey)
		if err != nil {
			return nil, false
		}
		var cached Video
		if err := json.Unmarshal(b, &cached); err != nil {
			return nil, false
		}
		return &cached, true
	}

	// 定义设置缓存的函数setCached，参数为视频对象
	setCached := func(video *Video) {
		b, err := json.Marshal(video)
		if err != nil {
			return
		}
		opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		_ = vs.cache.SetBytes(opCtx, cacheKey, b, vs.cacheTTL)
	}

	// 1. 尝试从redis缓存中获取视频详情
	if vs.cache != nil {
		// 如果缓存命中，直接返回缓存数据
		if v, ok := getCached(); ok {
			return v, nil
		}

		// 缓存未命中，尝试获取分布式锁，防止缓存击穿
		opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		b, err := vs.cache.GetBytes(opCtx, cacheKey)
		cancel()
		if err == nil {
			var cached Video
			if err := json.Unmarshal(b, &cached); err == nil {
				return &cached, nil
			}
		} else if rediscache.IsMiss(err) {
			// 缓存未命中，尝试获取分布式锁，lockKey 为锁的 key，格式为 "lock:video:detail:id={id}"
			lockKey := "lock:" + cacheKey

			lockCtx, lockCancel := context.WithTimeout(ctx, 50*time.Millisecond)
			token, locked, lockErr := vs.cache.Lock(lockCtx, lockKey, 2*time.Second)
			lockCancel()

			// 成功获取锁，确保在函数返回时释放锁
			if lockErr == nil && locked {
				defer func() { _ = vs.cache.Unlock(context.Background(), lockKey, token) }()

				// getCached 再次检查缓存，防止在获取锁期间其他请求已经回填了缓存
				if v, ok := getCached(); ok {
					return v, nil
				}

				// 获取数据库中的视频详情
				video, err := vs.repo.GetByID(ctx, id)
				if err != nil {
					return nil, err
				}
				// 将数据库中的视频详情写入缓存
				setCached(video)
				return video, nil
			}

			// 没拿到锁：等待别人回填缓存，最多等待 5 次，每次等待 20ms，期间检查缓存是否被回填
			for i := 0; i < 5; i++ {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(20 * time.Millisecond):
				}
				// 检查缓存是否被回填
				if v, ok := getCached(); ok {
					return v, nil
				}
			}
		}
	}

	// 最终还是没拿到锁，也没拿到缓存，直接从数据库获取
	video, err := vs.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// 将数据库中的视频详情写入redis缓存，供后续请求使用
	if vs.cache != nil {
		setCached(video)
	}
	return video, nil
}

func (vs *VideoService) UpdateLikesCount(ctx context.Context, id uint, likesCount int64) error {
	if err := vs.repo.UpdateLikesCount(ctx, id, likesCount); err != nil {
		return err
	}
	return nil
}

// UpdatePopularity 更新视频的热度值，支持数据库更新、消息队列通知和缓存更新，确保数据一致性和高性能。
func (vs *VideoService) UpdatePopularity(ctx context.Context, id uint, change int64) error {
	if err := vs.repo.UpdatePopularity(ctx, id, change); err != nil {
		return err
	}

	// 如果配置了消息队列，尝试将热度更新消息发送到消息队列，供其他服务消费，并且返回 nil，表示消息发送成功
	if vs.popularityMQ != nil {
		if err := vs.popularityMQ.Update(ctx, id, change); err == nil {
			return nil
		}
	}

	// 如果配置了 Redis 缓存，尝试更新缓存中的热度值，确保缓存与数据库的一致性
	if vs.cache != nil {
		// 1) 详情缓存：直接失效（最简单靠谱）
		_ = vs.cache.Del(context.Background(), vs.cache.Key("video:detail:id=%d", id))

		// 2) 热榜：写到“时间窗ZSET”，不要用 detail key
		now := time.Now().UTC().Truncate(time.Minute)
		windowKey := vs.cache.Key("hot:video:1m:%s", now.Format("200601021504"))
		member := strconv.FormatUint(uint64(id), 10)

		opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		_ = vs.cache.ZincrBy(opCtx, windowKey, member, float64(change))
		_ = vs.cache.Expire(opCtx, windowKey, 2*time.Hour)
	}
	// 如果没有配置消息队列和 Redis 缓存，直接返回 nil，表示热度更新成功
	return nil
}
