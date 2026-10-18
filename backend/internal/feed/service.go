package feed

import (
	"context"
	"encoding/json"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/video"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/patrickmn/go-cache"
	redis "github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

type FeedService struct {
	repo         *FeedRepository
	likeRepo     *video.LikeRepository
	rediscache   *rediscache.Client
	localcache   *cache.Cache
	cacheTTL     time.Duration
	requestGroup singleflight.Group
}

type CachedFeedData struct {
	PublicVideos []video.Video `json:"public_videos"`
}

func NewFeedService(repo *FeedRepository, likeRepo *video.LikeRepository, rediscache *rediscache.Client) *FeedService {
	return &FeedService{repo: repo, likeRepo: likeRepo, rediscache: rediscache, localcache: cache.New(3*time.Second, 5*time.Second), cacheTTL: 24 * time.Hour}
}

// GetVideoByIDs 批量获取视频信息，采用 L1(本地缓存) -> L2(Redis) -> L3(MySQL) 三级架构
func (f *FeedService) GetVideoByIDs(ctx context.Context, videoIDs []uint) ([]*video.Video, error) {
	if len(videoIDs) == 0 {
		return []*video.Video{}, nil
	}

	// map 的 key 是视频 ID，value 是指向 video.Video 的指针
	videoMap := make(map[uint]*video.Video)
	//L1:本地缓存
	var missedL1 []uint // missedL1 用于记录 L1 缓存未命中的视频 ID
	for _, id := range videoIDs {
		cacheKey := f.rediscache.Key("video:entity:%d", id) // 构建缓存 key
		// 尝试从本地缓存获取视频信息
		if f.localcache != nil {
			if v, found := f.localcache.Get(cacheKey); found {
				if data, ok := v.(video.Video); ok { // 类型断言，确保从缓存中取出的数据是 video.Video 类型
					videoMap[id] = &data
					continue
				}
			}
		}
		// 记录未命中的 ID，准备进入下一级缓存
		missedL1 = append(missedL1, id)
	}

	// 如果 L1 全部命中，直接返回结果
	if len(missedL1) == 0 {
		return buildOrderedResult(videoIDs, videoMap), nil
	}

	//L2:redis
	var missedL2 []uint
	// 判断missedL1是否为空，如果不为空，则尝试从 Redis 中获取视频信息
	if len(missedL1) > 0 {
		cacheKeys := make([]string, len(missedL1)) // 构建L1缓存未命中的视频 ID 对应的 Rediskey 切片
		for i, id := range missedL1 {
			cacheKeys[i] = f.rediscache.Key("video:entity:%d", id) // 构建 Redis key
		}

		// cacheCtx 用于设置 Redis 操作的超时时间，防止 Redis 请求阻塞过久
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		// 调用 MGet 方法批量获取 Redis 中的视频信息
		results, err := f.rediscache.MGet(cacheCtx, cacheKeys...)
		cancel() // cancel 用于释放 context 资源，防止内存泄漏

		if err == nil {
			for i, res := range results {
				id := missedL1[i]
				if res != nil {
					if str, ok := res.(string); ok {
						var v video.Video
						if err := json.Unmarshal([]byte(str), &v); err == nil {
							videoMap[id] = &v
							// 回写更新 L1 本地缓存
							if f.localcache != nil {
								f.localcache.Set(cacheKeys[i], v, 5*time.Second)
							}
							continue
						}
					}
				}
				missedL2 = append(missedL2, id)
			}
		} else {
			// 如果 Redis 挂了或者超时了，全部降级到 L3
			missedL2 = missedL1
			log.Printf("L2 Redis MGet 失败，全部降级到 MySQL: %v", err)
		}
	}

	if len(missedL2) == 0 {
		return buildOrderedResult(videoIDs, videoMap), nil
	}

	//L3:MySQL

	// 使用 WaitGroup 和 Mutex 来处理并发请求，确保线程安全
	var wg sync.WaitGroup
	var mu sync.Mutex
	// 遍历 L2 缓存未命中的视频 ID，使用单飞机制（singleflight）避免缓存击穿
	for _, id := range missedL2 {
		wg.Add(1)
		// 每个视频 ID 都会启动一个 goroutine 来处理数据库查询和缓存回写
		go func(videoID uint) {
			defer wg.Done()
			// 构建单飞锁的 key，确保同一个视频 ID 的请求只会有一个去数据库查询
			sfKey := f.rediscache.Key("sf:entity:%d", videoID)

			// 使用 singleflight 的 Do 方法，具体实现同一个视频 ID 的请求只会有一个去数据库查询
			v, err, _ := f.requestGroup.Do(sfKey, func() (interface{}, error) {
				videoList, err := f.repo.GetByIDs(ctx, []uint{videoID})

				if err != nil || len(videoList) == 0 {
					return nil, err
				}

				// safeCopy 是为了避免并发写入同一个 video.Video 对象，导致数据竞争问题
				safeCopy := *videoList[0]
				cachekey := f.rediscache.Key("video:entity:%d", safeCopy.ID)
				// Marshal 成 JSON 字符串，准备回写 Redis 缓存
				if b, err := json.Marshal(safeCopy); err == nil {
					// 匿名函数异步回写 Redis 缓存，避免阻塞主流程
					go func(k string, b []byte) {
						setCtx, setCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
						defer setCancel()

						f.rediscache.SetBytes(setCtx, k, b, time.Hour)
					}(cachekey, b)
				}
				// return 这个 video.Video 对象的副本，避免并发写入同一个对象
				return videoList[0], err
			})

			if err == nil && v != nil {
				// 解引用 v.(*video.Video) 得到 video.Video 对象，创建一个副本 safeCopy
				safeCopy := *(v.(*video.Video))
				// 使用 Mutex 锁保护 videoMap 的写入，确保线程安全
				mu.Lock()
				videoMap[id] = &safeCopy
				mu.Unlock()
				// 回写 L1 本地缓存，设置过期时间为 5 秒
				f.localcache.Set(f.rediscache.Key("video:entity:%d", safeCopy.ID), safeCopy, 5*time.Second)
			}
		}(id)
	}
	wg.Wait()
	return buildOrderedResult(videoIDs, videoMap), nil
}

// 查询最新视频 (冷热分离 + 游标分页)； 参数：ctx - 上下文，limit - 查询数量，latestBefore - 游标，viewerAccountID - 查看者账户 ID
func (f *FeedService) ListLatest(ctx context.Context, limit int, latestBefore time.Time, viewerAccountID uint) (ListLatestResponse, error) {

	// 如果 Redis 不可用，直接从数据库查询最新视频
	if f.rediscache == nil {
		return f.listLatestFromDB(ctx, limit, latestBefore, viewerAccountID)
	}

	// 获取 ZSET 中最老的一条数据，因为 ZRANGE 是从小到大排序，所以取索引 0 的元素就是最老的一条数据
	zsetTail, err := f.rediscache.ZRangeWithScores(ctx, f.rediscache.Key("feed:global_timeline"), 0, 0)
	if err != nil {
		return f.listLatestFromDB(ctx, limit, latestBefore, viewerAccountID)
	}

	// 判断 ZSET 是否为空，如果为空，则说明 Redis 中没有缓存数据，需要从数据库中查询最新视频
	isZsetEmpty := len(zsetTail) == 0
	if isZsetEmpty {
		//sfKey 是一个单飞锁的 key，用于防止缓存击穿，requestGroup.Do() 会确保同一个 sfKey 的请求只会有一个去数据库查询
		sfKey := f.rediscache.Key("sf:fallback:global_timeline_rebuild")
		v, err, _ := f.requestGroup.Do(sfKey, func() (interface{}, error) {
			// 无视游标，直接去 MySQL 捞最新的 1000 条
			dbVideos, err := f.repo.ListLatest(ctx, 1000, time.Time{})
			if err != nil {
				return nil, err
			}
			if len(dbVideos) == 0 {
				return "EMPTY_DB", nil // 防无限递归
			}

			// 重建 ZSET
			bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var zElements []redis.Z
			for _, vid := range dbVideos {
				zElements = append(zElements, redis.Z{
					Score:  float64(vid.CreateTime.UnixMilli()),
					Member: fmt.Sprintf("%d", vid.ID),
				})
			}
			// 写入 Redis ZSET，使用 ZADD 命令，设置过期时间为 24 小时
			f.rediscache.ZAdd(bgCtx, f.rediscache.Key("feed:global_timeline"), zElements...)
			return "SUCCESS", nil
		})
		if err != nil {
			return ListLatestResponse{}, err
		}
		// 如果数据库中没有数据，则返回空结果，避免无限递归/ 防止缓存穿透
		if v == "EMPTY_DB" {
			return ListLatestResponse{HasMore: false}, nil
		}

		// singleflight机制的补偿，让所有被阻塞的请求重新查一遍redis，避免缓存击穿
		return f.ListLatest(ctx, limit, latestBefore, viewerAccountID)
	}

	// watermark 是 ZSET 中最老的一条数据的时间戳，用于判断请求的游标是否已经落在冷数据区间
	watermark := int64(zsetTail[0].Score)
	reqTime := time.Now().UnixMilli()
	if !latestBefore.IsZero() {
		reqTime = latestBefore.UnixMilli()
	}

	// baseVideos 用于存储最终返回的视频列表，可能来自 Redis 或数据库
	var baseVideos []*video.Video

	// reqTime <= watermark，说明请求的游标已经落在冷数据区间，需要从数据库中查询最新视频
	if reqTime <= watermark {
		//冷数据降级查库,针对个别用户的防并发（此时可以用时间戳做锁，因为冷尾流量极小）
		sfKey := f.rediscache.Key("sf:cold:listLatest:%d:%d", limit, reqTime)
		v, err, _ := f.requestGroup.Do(sfKey, func() (interface{}, error) {
			return f.repo.ListLatest(ctx, limit, latestBefore)
		})
		if err != nil {
			return ListLatestResponse{}, err
		}
		baseVideos = v.([]*video.Video)
		// 不回写 ZSET，防止冷数据污染热点时间线,因为冷数据可能是历史数据，回写 ZSET 会导致热点时间线被污染，影响后续请求的性能和准确性

	} else { // reqTime > watermark，说明请求的游标落在热数据区间，可以直接从 Redis 中查询最新视频
		// 热数据直接查redis
		maxScore := "+inf" // 默认最大分数为正无穷，表示查询所有大于 watermark 的数据

		if !latestBefore.IsZero() {
			maxScore = fmt.Sprintf("%d", reqTime-1) // reqTime-1是为了避免下一页重复上一页最后一条数据
		}

		videoIDsStr, err := f.rediscache.ZRevRangeByScore(ctx, f.rediscache.Key("feed:global_timeline"), maxScore, "-inf", 0, int64(limit))
		if err != nil {
			return ListLatestResponse{}, err
		}

		var videoIDs []uint
		// 将字符串类型的 videoID 转换为 uint 类型，并存储在 videoIDs 切片中。因为 ZSET 中的 member 是字符串类型，而我们需要的是 uint 类型的 videoID
		for _, idStr := range videoIDsStr {
			if id, err := strconv.ParseUint(idStr, 10, 64); err == nil {
				videoIDs = append(videoIDs, uint(id))
			}
		}

		// videoIDs不为空，则调用 GetVideoByIDs 方法批量获取视频信息，可能来自 L1/L2/L3 缓存
		if len(videoIDs) > 0 {
			baseVideos, err = f.GetVideoByIDs(ctx, videoIDs)
			if err != nil {
				return ListLatestResponse{}, err
			}
		}

		// 如果从 Redis 中获取的视频数量不足 limit，则说明热数据区间已经没有足够的视频，需要从数据库中查询冷数据补充，要进行冷热数据的拼接，避免用户看到重复的视频
		if len(baseVideos) < limit {

			// remainLimit 是剩余需要查询的数量，等于 limit 减去从 Redis 中获取的视频数量
			remainLimit := limit - len(baseVideos)

			// coldCursor 是冷数据的游标，取 baseVideos 中最后一条视频的时间戳，如果 baseVideos 为空，则使用请求的 latestBefore 作为游标
			var coldCursor time.Time

			if len(baseVideos) > 0 {
				coldCursor = baseVideos[len(baseVideos)-1].CreateTime
			} else {
				coldCursor = latestBefore
			}

			// 这里的sf:stitch:listLatest 查的是不在热数据区间的冷数据，避免重复查热数据区间的数据
			sfKey := f.rediscache.Key("sf:stitch:listLatest:%d:%d", remainLimit, coldCursor.UnixMilli())
			v, err, _ := f.requestGroup.Do(sfKey, func() (interface{}, error) {
				return f.repo.ListLatest(ctx, remainLimit, coldCursor)
			})
			if err == nil {
				coldVideos := v.([]*video.Video)
				baseVideos = append(baseVideos, coldVideos...)
			}
		}
	}

	// nextTime 是下一页请求的游标，取 baseVideos 中最后一条视频的时间戳，如果 baseVideos 为空，则 nextTime 为 0
	var nextTime int64

	if len(baseVideos) > 0 {
		// 将本页最后一条视频的时间作为下一次请求的游标
		nextTime = baseVideos[len(baseVideos)-1].CreateTime.UnixMilli()
	}
	// 如果 baseVideos 中的视频数量等于 limit查询的数量，则说明还有更多数据，否则没有更多数据
	hasMore := len(baseVideos) == limit

	// 调用 buildFeedVideos 方法构建最终返回的视频列表，可能会对视频进行一些额外的处理，比如判断是否被当前用户点赞等
	feedVideos, err := f.buildFeedVideos(ctx, baseVideos, viewerAccountID)
	if err != nil {
		return ListLatestResponse{}, err
	}

	return ListLatestResponse{
		VideoList: feedVideos,
		NextTime:  nextTime,
		HasMore:   hasMore,
	}, nil
}

// Redis 不可用时，直接从数据库查询最新视频
func (f *FeedService) listLatestFromDB(ctx context.Context, limit int, latestBefore time.Time, viewerAccountID uint) (ListLatestResponse, error) {
	videos, err := f.repo.ListLatest(ctx, limit, latestBefore)
	if err != nil {
		return ListLatestResponse{}, err
	}
	feedVideos, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
	if err != nil {
		return ListLatestResponse{}, err
	}
	var nextTime int64
	if len(videos) > 0 {
		nextTime = videos[len(videos)-1].CreateTime.UnixMilli()
	}
	return ListLatestResponse{
		VideoList: feedVideos,
		NextTime:  nextTime,
		HasMore:   len(videos) == limit,
	}, nil
}

// 按照点赞数查询视频
func (f *FeedService) ListLikesCount(ctx context.Context, limit int, cursor *LikesCountCursor, viewerAccountID uint) (ListLikesCountResponse, error) {
	videos, err := f.repo.ListLikesCountWithCursor(ctx, limit, cursor)
	if err != nil {
		return ListLikesCountResponse{}, err
	}
	hasMore := len(videos) == limit
	feedVideos, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
	if err != nil {
		return ListLikesCountResponse{}, err
	}
	resp := ListLikesCountResponse{
		VideoList: feedVideos,
		HasMore:   hasMore,
	}
	if len(videos) > 0 {
		last := videos[len(videos)-1]
		nextLikesCountBefore := last.LikesCount
		nextIDBefore := last.ID
		resp.NextLikesCountBefore = &nextLikesCountBefore
		resp.NextIDBefore = &nextIDBefore
	}
	return resp, nil
}

// 按照关注列表查询视频
func (f *FeedService) ListByFollowing(ctx context.Context, limit int, latestBefore time.Time, viewerAccountID uint) (ListByFollowingResponse, error) {
	// doListByFollowingFromDB 是一个闭包函数，用于从数据库中查询按关注列表的视频，避免重复代码
	doListByFollowingFromDB := func() (ListByFollowingResponse, error) {
		videos, err := f.repo.ListByFollowing(ctx, limit, viewerAccountID, latestBefore)
		if err != nil {
			return ListByFollowingResponse{}, err
		}
		var nextTime int64
		if len(videos) > 0 {
			nextTime = videos[len(videos)-1].CreateTime.Unix()
		} else {
			nextTime = 0
		}
		hasMore := len(videos) == limit
		feedVideos, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
		if err != nil {
			return ListByFollowingResponse{}, err
		}
		resp := ListByFollowingResponse{
			VideoList: feedVideos,
			NextTime:  nextTime,
			HasMore:   hasMore,
		}
		return resp, nil
	}

	var cacheKey string
	if viewerAccountID != 0 && f.rediscache != nil {
		before := int64(0)
		if !latestBefore.IsZero() {
			before = latestBefore.Unix()
		}
		cacheKey = f.rediscache.Key("feed:listByFollowing:limit=%d:accountID=%d:before=%d", limit, viewerAccountID, before)
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		b, err := f.rediscache.GetBytes(cacheCtx, cacheKey)
		if err == nil { // 缓存命中，直接返回缓存数据
			var cached ListByFollowingResponse
			if err := json.Unmarshal(b, &cached); err == nil {
				return cached, nil
			}
		} else if rediscache.IsMiss(err) { // 缓存未命中
			lockKey := "lock:" + cacheKey
			// 缓存未命中，尝试加锁
			token, locked, _ := f.rediscache.Lock(cacheCtx, lockKey, 500*time.Millisecond)
			if locked {
				defer func() { _ = f.rediscache.Unlock(context.Background(), lockKey, token) }()
				// double check，拿到锁后再次检查缓存，防止缓存击穿
				if b, err := f.rediscache.GetBytes(cacheCtx, cacheKey); err == nil {
					var cached ListByFollowingResponse
					if err := json.Unmarshal(b, &cached); err == nil {
						return cached, nil
					}
				} else { // 缓存仍未命中，从数据库中查询
					resp, err := doListByFollowingFromDB()
					if err != nil {
						return ListByFollowingResponse{}, err
					}
					// Marshal 成 JSON 字符串，准备回写 Redis 缓存
					if b, err := json.Marshal(resp); err == nil {
						_ = f.rediscache.SetBytes(cacheCtx, cacheKey, b, f.cacheTTL)
					}
					return resp, nil
				}
			} else { // 没有拿到锁，说明其他请求正在查询数据库，等待一段时间后重试，从缓存中获取数据，spin-wait 自旋等待缓存回填
				for i := 0; i < 5; i++ {
					time.Sleep(20 * time.Millisecond)
					if b, err := f.rediscache.GetBytes(cacheCtx, cacheKey); err == nil {
						var cached ListByFollowingResponse
						if err := json.Unmarshal(b, &cached); err == nil {
							return cached, nil
						}
					}
				}
			}
		}
	}

	// 如果没有配置 Redis 缓存，或者缓存未命中且没有拿到锁，则直接从数据库中查询
	resp, err := doListByFollowingFromDB()
	if err != nil {
		return ListByFollowingResponse{}, err
	}
	// 回写缓存，避免缓存击穿
	if cacheKey != "" {
		if b, err := json.Marshal(resp); err == nil {
			cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
			_ = f.rediscache.SetBytes(cacheCtx, cacheKey, b, f.cacheTTL)
		}
	}
	return resp, nil
}

// ListByPopularity 获取按热度排序的视频列表，支持分页和游标机制，返回视频信息和分页信息。
func (f *FeedService) ListByPopularity(ctx context.Context, limit int, reqAsOf int64, offset int, viewerAccountID uint, latestPopularity int64, latestBefore time.Time, latestIDBefore uint) (ListByPopularityResponse, error) {
	// Redis 热榜（稳定分页：as_of + offset）
	if f.rediscache != nil {
		// asOf 是当前时间的 UTC 时间，精确到分钟，如果 reqAsOf > 0，则使用 reqAsOf 作为 asOf
		asOf := time.Now().UTC().Truncate(time.Minute)
		if reqAsOf > 0 {
			// Unix() 的作用是将时间戳转换为 time.Time 类型，UTC() 的作用是将时间转换为 UTC 时间，Truncate(time.Minute) 的作用是将时间精确到分钟
			asOf = time.Unix(reqAsOf, 0).UTC().Truncate(time.Minute)
		}

		// win 是热榜的时间窗口，单位为分钟，这里设置为 60 分钟，即热榜只保留最近 60 分钟的数据
		const win = 60
		keys := make([]string, 0, win)
		for i := 0; i < win; i++ {
			keys = append(keys, f.rediscache.Key("hot:video:1m:%s", asOf.Add(-time.Duration(i)*time.Minute).Format("200601021504")))
		}

		// dest 是合并后的热榜的 Redis key，格式为 hot:video:merge:1m:<as_of>，其中 <as_of> 是当前时间的 UTC 时间，精确到分钟
		dest := f.rediscache.Key("hot:video:merge:1m:%s", asOf.Format("200601021504")) // 快照key：同一个as_of页内复用
		// 不要在请求路径同步执行 ZUNIONSTORE。高并发下多个请求同时 miss
		// 会重复执行聚合，并把 Redis 的排队时间放大成请求超时。
		f.ensurePopularitySnapshot(dest, keys)

		start := int64(offset)
		stop := start + int64(limit) - 1
		readCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
		members, err := f.rediscache.ZRevRange(readCtx, dest, start, stop)
		cancel()
		if err == nil && len(members) == 0 {
			if offset > 0 {
				return ListByPopularityResponse{
					VideoList:  []FeedVideoItem{},
					AsOf:       asOf.Unix(),
					NextOffset: offset,
					HasMore:    false,
				}, nil
			}
		}

		if err == nil && len(members) > 0 {
			ids := make([]uint, 0, len(members))
			for _, m := range members {
				u, err := strconv.ParseUint(m, 10, 64)
				if err == nil && u > 0 {
					ids = append(ids, uint(u))
				}
			}

			videos, err := f.repo.GetByIDs(ctx, ids)
			if err == nil {
				byID := make(map[uint]*video.Video, len(videos))
				for _, v := range videos {
					byID[v.ID] = v
				}
				ordered := make([]*video.Video, 0, len(ids))
				for _, id := range ids {
					if v := byID[id]; v != nil {
						ordered = append(ordered, v)
					}
				}
				items, err := f.buildFeedVideos(ctx, ordered, viewerAccountID)
				if err != nil {
					return ListByPopularityResponse{}, err
				}
				resp := ListByPopularityResponse{
					VideoList:  items,
					AsOf:       asOf.Unix(),
					NextOffset: offset + len(items),
					HasMore:    len(items) == limit,
				}
				if len(ordered) > 0 {
					last := ordered[len(ordered)-1]
					nextPopularity := last.Popularity
					nextBefore := last.CreateTime
					nextID := last.ID
					resp.NextLatestPopularity = &nextPopularity
					resp.NextLatestBefore = &nextBefore
					resp.NextLatestIDBefore = &nextID
				}
				return resp, nil
			}
		}
	}

	videos, err := f.repo.ListByPopularity(ctx, limit, latestPopularity, latestBefore, latestIDBefore)
	if err != nil {
		return ListByPopularityResponse{}, err
	}
	items, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
	if err != nil {
		return ListByPopularityResponse{}, err
	}
	resp := ListByPopularityResponse{
		VideoList:  items,
		AsOf:       0,
		NextOffset: 0,
		HasMore:    len(items) == limit,
	}
	if len(videos) > 0 {
		last := videos[len(videos)-1]
		nextPopularity := last.Popularity
		nextBefore := last.CreateTime
		nextID := last.ID
		resp.NextLatestPopularity = &nextPopularity
		resp.NextLatestBefore = &nextBefore
		resp.NextLatestIDBefore = &nextID
	}
	return resp, nil
}

// ensurePopularitySnapshot schedules at most one snapshot build per Redis key.
// The current request falls back to MySQL while the snapshot is being built,
// instead of waiting for ZUNIONSTORE to finish.
func (f *FeedService) ensurePopularitySnapshot(dest string, keys []string) {
	checkCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	exists, err := f.rediscache.Exists(checkCtx, dest)
	if err != nil || exists {
		return
	}

	lockKey := "lock:" + dest
	token, locked, err := f.rediscache.Lock(checkCtx, lockKey, time.Second)
	if err != nil || !locked {
		return
	}

	go func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer unlockCancel()
		defer func() {
			if err := f.rediscache.Unlock(unlockCtx, lockKey, token); err != nil {
				log.Printf("feed popularity snapshot unlock failed for %s: %v", dest, err)
			}
		}()

		buildCtx, buildCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer buildCancel()

		if exists, err := f.rediscache.Exists(buildCtx, dest); err != nil {
			log.Printf("feed popularity snapshot existence check failed for %s: %v", dest, err)
			return
		} else if exists {
			return
		}
		if err := f.rediscache.ZUnionStore(buildCtx, dest, keys, "SUM"); err != nil {
			log.Printf("feed popularity snapshot build failed for %s: %v", dest, err)
			return
		}
		if err := f.rediscache.Expire(buildCtx, dest, 2*time.Minute); err != nil {
			log.Printf("feed popularity snapshot expiration failed for %s: %v", dest, err)
		}
	}()
}

// buildFeedVideos 构建 FeedVideoItem 列表，包含视频信息和当前用户是否点赞的信息，根据各种List方法获取的视频列表，调用 likeRepo 批量查询当前用户是否点赞，并将结果封装到 FeedVideoItem 中返回
func (f *FeedService) buildFeedVideos(ctx context.Context, videos []*video.Video, viewerAccountID uint) ([]FeedVideoItem, error) {
	feedVideos := make([]FeedVideoItem, 0, len(videos))
	videoIDs := make([]uint, len(videos))
	for i, v := range videos {
		videoIDs[i] = v.ID
	}
	likedMap, err := f.likeRepo.BatchGetLiked(ctx, videoIDs, viewerAccountID)
	if err != nil {
		return nil, err
	}

	// 构建 FeedVideoItem 列表，包含视频信息和当前用户是否点赞的信息
	for _, video := range videos {
		feedVideos = append(feedVideos, FeedVideoItem{
			ID:          video.ID,
			Author:      FeedAuthor{ID: video.AuthorID, Username: video.Username},
			Title:       video.Title,
			Description: video.Description,
			PlayURL:     video.PlayURL,
			CoverURL:    video.CoverURL,
			CreateTime:  video.CreateTime.Unix(),
			LikesCount:  video.LikesCount,
			IsLiked:     likedMap[video.ID],
		})
	}
	return feedVideos, nil
}

func buildOrderedResult(orderedIDs []uint, dataMap map[uint]*video.Video) []*video.Video {
	res := make([]*video.Video, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		if v, exits := dataMap[id]; exits && v != nil {
			res = append(res, v)
		}
	}
	return res
}

func (f *FeedService) ListByTag(ctx context.Context, tagName string, limit int, viewerAccountID uint) ([]FeedVideoItem, error) {
	videos, err := f.repo.ListByTag(ctx, tagName, limit)
	if err != nil {
		return nil, err
	}
	return f.buildFeedVideos(ctx, videos, viewerAccountID)
}
