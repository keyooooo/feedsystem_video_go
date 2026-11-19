package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"feedsystem_video_go/internal/config"
	"fmt"
	"strconv"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// 封装 Redis 客户端，提供更高层次的缓存操作接口，支持本地缓存、分布式锁、单飞机制等功能。
type Client struct {
	rdb       *redis.Client
	keyPrefix string
}

// v1是版本前缀，方便后续升级时区分不同版本的缓存数据，避免新旧缓存数据冲突。
const defaultKeyPrefix = "v1:"

// NewClient 创建一个新的 Redis 客户端实例，接收一个 redis.Client 和一个 key 前缀。
func NewClient(rdb *redis.Client, keyPrefix string) *Client {
	return &Client{rdb: rdb, keyPrefix: keyPrefix}
}

// NewFromEnv 从环境变量中读取 Redis 配置，并调用 NewClient 创建一个新的 Redis 客户端实例。
func NewFromEnv(cfg *config.RedisConfig) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Host + ":" + strconv.Itoa(cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	return &Client{rdb: rdb, keyPrefix: defaultKeyPrefix}, nil
}

// Close 关闭 Redis 客户端连接。
func (c *Client) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

// Ping 测试 Redis 连接是否可用。
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.rdb == nil {
		return errors.New("redis client not initialized")
	}
	return c.rdb.Ping(ctx).Err()
}

// IsMiss 判断 Redis 错误是否为缓存未命中（redis.Nil）。
func IsMiss(err error) bool {
	return err == redis.Nil
}

// Key 生成带有前缀的 Redis key，避免不同模块之间的 key 冲突。
func (c *Client) Key(format string, args ...any) string {
	prefix := ""
	if c != nil {
		prefix = c.keyPrefix
	}
	return prefix + fmt.Sprintf(format, args...)
}

// randToken 生成一个随机的十六进制字符串，用于分布式锁的 token。
func randToken(n int) (string, error) {
	b := make([]byte, n)
	// Read 随机字节，如果失败则返回错误
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// 将随机字节编码为十六进制字符串并返回
	return hex.EncodeToString(b), nil
}

// Lock 尝试获取分布式锁，返回锁的 token、是否成功获取锁以及可能的错误。
func (c *Client) Lock(ctx context.Context, key string, ttl time.Duration) (token string, ok bool, err error) {
	if c == nil || c.rdb == nil {
		return "", false, nil
	}
	token, err = randToken(16)
	if err != nil {
		return "", false, err
	}
	ok, err = c.rdb.SetNX(ctx, key, token, ttl).Result()
	return token, ok, err
}

// UnlockScript 是一个 Lua 脚本，用于安全地释放分布式锁，确保只有持有锁的客户端才能释放锁。
var unlockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
else
  return 0
end
`)

// IncrementWithExpireScript 是一个 Lua 脚本，用于在 Redis 中原子地增加计数器并设置过期时间，适用于限流场景。
var incrementWithExpireScript = redis.NewScript(`
local count = redis.call("INCR", KEYS[1])
if count == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
return count
`)

// Unlock 释放分布式锁，只有当锁的 token 与传入的 token 匹配时才会释放锁，确保锁的安全性。
func (c *Client) Unlock(ctx context.Context, key string, token string) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	_, err := unlockScript.Run(ctx, c.rdb, []string{key}, token).Result()
	return err
}

func (c *Client) IncrementWithExpire(ctx context.Context, key string, expire time.Duration) (int64, error) {
	if c == nil || c.rdb == nil {
		return 0, nil
	}
	return incrementWithExpireScript.Run(
		ctx,
		c.rdb,
		[]string{key},
		expire.Milliseconds(),
	).Int64()
}
