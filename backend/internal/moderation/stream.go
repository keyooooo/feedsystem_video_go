package moderation

import (
	"context"
	"encoding/json"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"fmt"
	goredis "github.com/redis/go-redis/v9"
	"net/http"
	"strings"
	"time"

	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/auth"
	"feedsystem_video_go/internal/middleware/jwt"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func reviewStream(db *gorm.DB, cache *rediscache.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, _ := jwt.GetAccountID(c)
		parts := strings.Fields(c.GetHeader("Authorization"))
		if len(parts) != 2 {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		token := parts[1]
		claims, err := auth.ParseToken(token)
		if err != nil || claims.AccountID != id {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		ctx := c.Request.Context()
		tx := db.WithContext(ctx)
		valid := func() bool {
			var user account.Account
			if claims.ExpiresAt == nil || !claims.ExpiresAt.After(time.Now()) ||
				tx.Select("token", "session_expires_at").First(&user, id).Error != nil {
				return false
			}
			if auth.CookieOrigin() != "" {
				if user.SessionExpiresAt == nil || !user.SessionExpiresAt.After(time.Now()) || user.Token != auth.DigestToken(token) {
					return false
				}
			} else if user.Token != token {
				return false
			}
			return tx.First(&Reviewer{}, "account_id = ?", id).Error == nil
		}
		if !valid() {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-store")
		c.Header("X-Accel-Buffering", "no")
		var events <-chan *goredis.Message
		interval := 2 * time.Second
		if cache != nil {
			subscription := cache.Subscribe(ctx, reviewTopic)
			defer subscription.Close()
			ack, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			_, err := subscription.Receive(ack)
			cancel()
			if err == nil {
				events = subscription.Channel()
				interval = 15 * time.Second
			}
		}
		changes := time.NewTicker(interval)
		defer changes.Stop()
		var last struct{ CaseID, DecisionID, RunID uint }
		for {
			var revision struct{ CaseID, DecisionID, RunID uint }
			if err := tx.Raw("SELECT (SELECT COALESCE(MAX(id),0) FROM moderation_cases) AS case_id, (SELECT COALESCE(MAX(id),0) FROM moderation_decisions) AS decision_id, (SELECT COALESCE(MAX(id),0) FROM moderation_shadow_runs) AS run_id").Scan(&revision).Error; err != nil {
				return
			}
			if !valid() {
				return
			}
			if revision != last || !c.Writer.Written() {
				if _, err := fmt.Fprint(c.Writer, "event: changed\ndata: {}\n\n"); err != nil {
					return
				}
				last = revision
			} else if _, err := fmt.Fprint(c.Writer, ": keepalive\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
			select {
			case <-ctx.Done():
				return
			case <-changes.C:
			case message, ok := <-events:
				if !ok {
					events = nil
					changes.Reset(2 * time.Second)
					continue
				}
				var change reviewEvent
				if json.Unmarshal([]byte(message.Payload), &change) != nil || change.CaseID == 0 {
					continue
				}
				if !valid() {
					return
				}
				payload, err := json.Marshal(change)
				if err != nil {
					return
				}
				if _, err := fmt.Fprintf(c.Writer, "event: changed\ndata: %s\n\n", payload); err != nil {
					return
				}
				c.Writer.Flush()
				// The following snapshot check also recovers missed messages.
			}
		}
	}
}
