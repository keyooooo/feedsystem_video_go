package video

import (
	"context"
	"errors"
	"feedsystem_video_go/internal/middleware/rabbitmq"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"time"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

type LikeService struct {
	repo         *LikeRepository
	VideoRepo    *VideoRepository
	cache        *rediscache.Client
	likeMQ       *rabbitmq.LikeMQ
	popularityMQ *rabbitmq.PopularityMQ
}

func NewLikeService(repo *LikeRepository, videoRepo *VideoRepository, cache *rediscache.Client, likeMQ *rabbitmq.LikeMQ, popularityMQ *rabbitmq.PopularityMQ) *LikeService {
	return &LikeService{repo: repo, VideoRepo: videoRepo, cache: cache, likeMQ: likeMQ, popularityMQ: popularityMQ}
}

func isDupKey(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

// Like 处理用户点赞视频的逻辑，首先检查视频是否存在，然后检查用户是否已经点赞过该视频，如果没有，则尝试通过 Like MQ 和 Popularity MQ 发布消息，如果发布失败，则回退到直接写入 MySQL 和 Redis 的方式，确保数据一致性和最终一致性。
func (s *LikeService) Like(ctx context.Context, like *Like) error {
	if like == nil {
		return errors.New("like is nil")
	}
	if like.VideoID == 0 || like.AccountID == 0 {
		return errors.New("video_id and account_id are required")
	}

	if s.VideoRepo != nil {
		ok, err := s.VideoRepo.IsExist(ctx, like.VideoID)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("video not found")
		}
	}

	isLiked, err := s.repo.IsLiked(ctx, like.VideoID, like.AccountID)
	if err != nil {
		return err
	}
	if isLiked {
		return errors.New("user has liked this video")
	}

	like.CreatedAt = time.Now()
	mysqlEnqueued := false
	redisEnqueued := false
	if s.likeMQ != nil {
		if err := s.likeMQ.Like(ctx, like.AccountID, like.VideoID); err == nil {
			mysqlEnqueued = true
		}
	}
	if s.popularityMQ != nil {
		if err := s.popularityMQ.Update(ctx, like.VideoID, 1); err == nil {
			redisEnqueued = true
		}
	}
	if mysqlEnqueued && redisEnqueued {
		return nil
	}

	// Fallback: direct MySQL write when like MQ publish fails.
	if !mysqlEnqueued {
		err := s.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Select("id").First(&Video{}, like.VideoID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errors.New("video not found")
				}
				return err
			}
			if err := tx.Create(like).Error; err != nil {
				if isDupKey(err) {
					return errors.New("user has liked this video")
				}
				return err
			}
			if err := tx.Model(&Video{}).Where("id = ?", like.VideoID).
				UpdateColumn("likes_count", gorm.Expr("likes_count + 1")).Error; err != nil {
				return err
			}
			return tx.Model(&Video{}).Where("id = ?", like.VideoID).
				UpdateColumn("popularity", gorm.Expr("popularity + 1")).Error
		})
		if err != nil {
			return err
		}
	}

	// Fallback: direct Redis update when popularity MQ publish fails.
	if !redisEnqueued {
		UpdatePopularityCache(ctx, s.cache, like.VideoID, 1)
	}
	return nil
}

// Unlike 处理用户取消点赞视频的逻辑，首先检查视频是否存在，然后检查用户是否已经点赞过该视频，如果已经点赞，则尝试通过 Like MQ 和 Popularity MQ 发布消息，如果发布失败，则回退到直接写入 MySQL 和 Redis 的方式，确保数据一致性和最终一致性。
func (s *LikeService) Unlike(ctx context.Context, like *Like) error {
	if like == nil {
		return errors.New("like is nil")
	}
	if like.VideoID == 0 || like.AccountID == 0 {
		return errors.New("video_id and account_id are required")
	}

	if s.VideoRepo != nil {
		ok, err := s.VideoRepo.IsExist(ctx, like.VideoID)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("video not found")
		}
	}

	isLiked, err := s.repo.IsLiked(ctx, like.VideoID, like.AccountID)
	if err != nil {
		return err
	}
	if !isLiked {
		return errors.New("user has not liked this video")
	}

	mysqlEnqueued := false
	redisEnqueued := false
	if s.likeMQ != nil {
		if err := s.likeMQ.Unlike(ctx, like.AccountID, like.VideoID); err == nil {
			mysqlEnqueued = true
		}
	}
	if s.popularityMQ != nil {
		if err := s.popularityMQ.Update(ctx, like.VideoID, -1); err == nil {
			redisEnqueued = true
		}
	}
	if mysqlEnqueued && redisEnqueued {
		return nil
	}

	// Fallback: direct MySQL write when like MQ publish fails.
	if !mysqlEnqueued {
		err := s.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			del := tx.Where("video_id = ? AND account_id = ?", like.VideoID, like.AccountID).Delete(&Like{})
			if del.Error != nil {
				return del.Error
			}
			if del.RowsAffected == 0 {
				return errors.New("user has not liked this video")
			}

			if err := tx.Model(&Video{}).Where("id = ?", like.VideoID).
				UpdateColumn("likes_count", gorm.Expr("GREATEST(likes_count - 1, 0)")).Error; err != nil {
				return err
			}
			return tx.Model(&Video{}).Where("id = ?", like.VideoID).
				UpdateColumn("popularity", gorm.Expr("GREATEST(popularity - 1, 0)")).Error
		})
		if err != nil {
			return err
		}
	}

	// Fallback: direct Redis update when popularity MQ publish fails.
	if !redisEnqueued {
		UpdatePopularityCache(ctx, s.cache, like.VideoID, -1)
	}
	return nil
}

// IsLiked 检查用户是否已经点赞过某个视频，返回布尔值和可能的错误。
func (s *LikeService) IsLiked(ctx context.Context, videoID, accountID uint) (bool, error) {
	return s.repo.IsLiked(ctx, videoID, accountID)
}

// ListLikedVideos 获取用户点赞过的视频列表，返回视频对象的切片和可能的错误。
func (s *LikeService) ListLikedVideos(ctx context.Context, accountID uint) ([]Video, error) {
	return s.repo.ListLikedVideos(ctx, accountID)
}
