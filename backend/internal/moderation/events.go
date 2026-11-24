package moderation

import (
	"context"
	"encoding/json"
	"time"

	rediscache "feedsystem_video_go/internal/middleware/redis"
)

const reviewTopic = "moderation:review-events"

type reviewEvent struct {
	CaseID  uint   `json:"case_id"`
	Version uint   `json:"version"`
	Type    string `json:"type"`
}

func publishReviewEvent(ctx context.Context, cache *rediscache.Client, event reviewEvent) {
	if cache == nil || event.CaseID == 0 {
		return
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	publish, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	// This signal accelerates refresh. MySQL retains the committed state.
	_ = cache.Publish(publish, reviewTopic, payload)
}
