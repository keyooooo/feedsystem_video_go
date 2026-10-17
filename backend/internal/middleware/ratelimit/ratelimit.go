package ratelimit

import (
	jwt "feedsystem_video_go/internal/middleware/jwt"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type KeyFunc func(*gin.Context) (string, bool)

// Limit 是一个 Gin 中间件工厂函数，用于创建一个基于redis的限流中间件。它接收 Redis 客户端、键前缀、最大请求数、时间窗口和一个用于生成唯一键的函数作为参数。
func Limit(
	cache *rediscache.Client,
	keyPrefix string,
	maxRequests int64,
	window time.Duration,
	keyFunc KeyFunc,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cache == nil || keyFunc == nil || maxRequests <= 0 || window <= 0 {
			c.Next()
			return
		}
		subject, ok := keyFunc(c)
		if !ok {
			c.Next()
			return
		}
		key := buildKey(keyPrefix, subject)
		count, err := cache.IncrementWithExpire(c.Request.Context(), key, window)
		// redis 错误，限流失败，允许请求通过fail-open
		if err != nil {
			c.Next()
			return
		}
		if count > maxRequests {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "too many requests",
			})
			return
		}
		c.Next()
	}
}

// buildKey 根据给定的键前缀和主题生成一个唯一的 Redis 键，用于限流计数。它会去除前后空格，并在前缀为空时使用默认值 "default"。
func buildKey(keyPrefix, subject string) string {
	keyPrefix = strings.TrimSpace(keyPrefix)
	if keyPrefix == "" {
		keyPrefix = "default"
	}
	return fmt.Sprintf("feedsystem:ratelimit:%s:%s", keyPrefix, strings.TrimSpace(subject))
}

// KeyByIP 是一个 KeyFunc 实现，用于根据客户端的 IP 地址生成唯一键。它会从 Gin 上下文中获取客户端 IP，并返回该 IP 作为键。如果无法获取 IP，则返回 false。
func KeyByIP(c *gin.Context) (string, bool) {
	ip := strings.TrimSpace(c.ClientIP())
	if ip == "" {
		return "", false
	}
	return ip, true
}

// KeyByAccount 是一个 KeyFunc 实现，用于根据用户的账户 ID 生成唯一键。它会从 Gin 上下文中获取 JWT 中的账户 ID，并返回该 ID 作为键。如果无法获取账户 ID 或账户 ID 为零，则返回 false。
func KeyByAccount(c *gin.Context) (string, bool) {
	accountID, err := jwt.GetAccountID(c)
	if err != nil || accountID == 0 {
		return "", false
	}
	return strconv.FormatUint(uint64(accountID), 10), true
}
