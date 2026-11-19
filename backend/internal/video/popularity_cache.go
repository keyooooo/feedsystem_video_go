package video

import (
	"context"
	"strconv"
	"time"

	rediscache "feedsystem_video_go/internal/middleware/redis"
)

// 更新视频流行度缓存
func UpdatePopularityCache(ctx context.Context, cache *rediscache.Client, id uint, change int64) {
	if cache == nil || id == 0 || change == 0 {
		return
	}

	// 删除视频详情缓存和视频实体缓存，确保下次获取时从数据库重新加载，因为视频对象中包含的流行度字段可能已经改变
	// context使用background表示不继承上游请求取消信号
	_ = cache.Del(context.Background(), cache.Key("video:detail:id=%d", id))
	_ = cache.Del(context.Background(), cache.Key("video:entity:%d", id))

	now := time.Now().UTC().Truncate(time.Minute)
	windowKey := cache.Key("hot:video:1m:%s", now.Format("200601021504"))
	member := strconv.FormatUint(uint64(id), 10)

	opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	// 没有组成事务，直接写入ZSet，增加视频的流行度分数，并设置ZSet的过期时间为2小时，确保热榜数据不会无限增长
	_ = cache.ZincrBy(opCtx, windowKey, member, float64(change))
	_ = cache.Expire(opCtx, windowKey, 2*time.Hour)
}
