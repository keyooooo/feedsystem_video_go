package video

import (
	"context"
	"net/http"
	"strings"
	"time"

	"feedsystem_video_go/internal/middleware/jwt"
	"feedsystem_video_go/internal/upload"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type publishAssetsRequest struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	VideoAssetID string `json:"video_asset_id"`
	CoverAssetID string `json:"cover_asset_id"`
	PlayURL      string `json:"play_url"`
	CoverURL     string `json:"cover_url"`
}

func PublishAssets(uploads *upload.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req publishAssetsRequest
		if err := upload.BindJSON(c, &req); err != nil {
			upload.WriteError(c, err)
			return
		}
		owner, err := jwt.GetAccountID(c)
		username, usernameErr := jwt.GetUsername(c)
		if err != nil || usernameErr != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "sign in required"})
			return
		}
		video, err := publishAssets(c.Request.Context(), uploads, owner, username, req)
		if err != nil {
			upload.WriteError(c, err)
			return
		}
		c.JSON(http.StatusOK, video)
	}
}

func publishAssets(ctx context.Context, uploads *upload.Service, owner uint, username string, req publishAssetsRequest) (Video, error) {
	var result Video
	title, desc := strings.TrimSpace(req.Title), strings.TrimSpace(req.Description)
	if title == "" || len([]rune(title)) > 255 || len([]rune(desc)) > 255 || req.PlayURL != "" || req.CoverURL != "" ||
		req.VideoAssetID == "" || req.CoverAssetID == "" || req.VideoAssetID == req.CoverAssetID {
		return result, upload.ErrRequest
	}
	err := uploads.Transaction(ctx, func(tx *gorm.DB) error {
		video, err := upload.LockOwned(tx, owner, req.VideoAssetID)
		if err != nil {
			return err
		}
		if video.Kind != upload.Video {
			return upload.ErrConflict
		}
		cover, err := upload.LockOwned(tx, owner, req.CoverAssetID)
		if err != nil {
			return err
		}
		if cover.Kind != upload.Cover {
			return upload.ErrConflict
		}
		if video.Status == upload.Published && cover.Status == upload.Published && video.VideoID != 0 && video.VideoID == cover.VideoID {
			if err := tx.First(&result, video.VideoID).Error; err != nil {
				return err
			}
			if result.Title != title || result.Description != desc ||
				result.PlayURL != video.PublicPath() || result.CoverURL != cover.PublicPath() {
				return upload.ErrConflict
			}
			return nil
		}
		now := time.Now()
		if video.State(now) == upload.Expired || cover.State(now) == upload.Expired {
			return upload.ErrExpired
		}
		if video.Status != upload.Ready || cover.Status != upload.Ready {
			return upload.ErrConflict
		}
		result = Video{
			AuthorID: owner, Username: username, Title: title, Description: desc,
			PlayURL: video.PublicPath(), CoverURL: cover.PublicPath(),
		}
		if err := publishVideo(tx, &result); err != nil {
			return err
		}
		return tx.Model(&upload.Session{}).Where("id IN ?", []string{video.ID, cover.ID}).
			Updates(map[string]any{"status": upload.Published, "video_id": result.ID}).Error
	})
	return result, err
}
