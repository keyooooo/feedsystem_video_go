package redis

import (
	"context"
	"errors"
	"time"
)

// GetBytes 从 Redis 中获取指定 key 的字节数据，如果 key 不存在则返回 redis.Nil 错误。
func (c *Client) GetBytes(ctx context.Context, key string) ([]byte, error) {
	if c == nil || c.rdb == nil {
		return nil, errors.New("redis client not initialized")
	}
	return c.rdb.Get(ctx, key).Bytes()
}

// SetBytes 将字节数据存储到 Redis 中指定的 key，并设置过期时间 ttl。
func (c *Client) SetBytes(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if c == nil || c.rdb == nil {
		return errors.New("redis client not initialized")
	}
	return c.rdb.Set(ctx, key, value, ttl).Err()
}

// Del 删除 Redis 中指定的 key，如果 key 不存在则返回 nil。
func (c *Client) Del(ctx context.Context, key string) error {
	if c == nil || c.rdb == nil {
		return errors.New("redis client not initialized")
	}
	return c.rdb.Del(ctx, key).Err()
}

func (c *Client) DelByPattern(ctx context.Context, pattern string) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	iter := c.rdb.Scan(ctx, 0, pattern, 0).Iterator()
	for iter.Next(ctx) {
		_ = c.rdb.Del(ctx, iter.Val())
	}
	return iter.Err()
}

// MGet 批量获取 Redis 中指定的多个 key 的值，返回一个包含所有 key 对应值的切片，如果某个 key 不存在则对应位置为 nil。
func (c *Client) MGet(cacheCtx context.Context, cacheKeys ...string) ([]interface{}, error) {
	if c == nil || c.rdb == nil {
		return nil, errors.New("redis client not initialized")
	}
	return c.rdb.MGet(cacheCtx, cacheKeys...).Result()
}
