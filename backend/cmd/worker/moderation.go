package main

import (
	"context"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"log"
	"os"
	"strings"
	"time"

	"feedsystem_video_go/internal/moderation"
	"gorm.io/gorm"
)

func startModerationShadow(ctx context.Context, db *gorm.DB, cache *rediscache.Client) {
	modelID := strings.TrimSpace(os.Getenv("MODERATION_MODEL"))
	if modelID == "" {
		return
	}
	chat, modelID, err := moderation.ModelFromEnv(ctx)
	if err != nil {
		log.Printf("Moderation shadow disabled: %v", err)
		return
	}
	evaluator := moderation.NewShadowEvaluator(db, chat, modelID)
	go func() {
		for ctx.Err() == nil {
			err := moderation.RunShadow(ctx, db, evaluator, cache)
			if ctx.Err() != nil {
				return
			}
			log.Printf("Moderation shadow stopped: %T", err)
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}
