package upload

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/middleware/jwt"
	rediscache "feedsystem_video_go/internal/middleware/redis"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
	cache   *rediscache.Client
}

func NewHandler(service *Service, cache *rediscache.Client) *Handler {
	return &Handler{service: service, cache: cache}
}

func (h *Handler) Register(group *gin.RouterGroup) {
	group.POST("", h.create)
	group.POST("/:id/credentials", h.renew)
	group.GET("/:id", h.status)
	group.POST("/:id/complete", h.complete)
	group.POST("/:id/cancel", h.cancel)
}

func WriteError(c *gin.Context, err error) {
	status, code, message := http.StatusServiceUnavailable, "upload_unavailable", "upload service temporarily unavailable"
	switch {
	case errors.Is(err, ErrRequest):
		status, code, message = http.StatusBadRequest, "invalid_request", "invalid upload or publication parameters"
	case errors.Is(err, ErrNotFound):
		status, code, message = http.StatusNotFound, "not_found", "upload or media not found"
	case errors.Is(err, ErrExpired):
		status, code, message = http.StatusGone, "upload_expired", "upload session expired or too close to expiry"
	case errors.Is(err, ErrObjectPending), errors.Is(err, ErrConflict):
		status, code, message = http.StatusConflict, "upload_conflict", "upload conflicts with its current state or object is not complete"
	case errors.Is(err, ErrInvalidMedia):
		status, code, message = http.StatusUnprocessableEntity, "invalid_media", "object size or media format is invalid"
	case errors.Is(err, ErrLimit):
		status, code, message = http.StatusTooManyRequests, "upload_limit", "upload quota, rate or concurrency limit reached"
	default:
		// Cloud errors can contain signed URLs or temporary credential metadata.
		log.Printf("direct upload %s failed (%T)", c.FullPath(), err)
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"code": code, "error": message})
}

func BindJSON(c *gin.Context, dst any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.ShouldBindJSON(dst); err != nil {
		return ErrRequest
	}
	return nil
}

func ownerID(c *gin.Context) (uint, bool) {
	owner, err := jwt.GetAccountID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "sign in required"})
		return 0, false
	}
	return owner, true
}

func (h *Handler) create(c *gin.Context) {
	owner, ok := ownerID(c)
	if !ok {
		return
	}
	var req struct {
		Kind        string `json:"kind"`
		Size        int64  `json:"size"`
		ContentType string `json:"content_type"`
	}
	if err := BindJSON(c, &req); err != nil {
		WriteError(c, err)
		return
	}
	session, grant, err := h.service.Create(c.Request.Context(), owner, req.Kind, req.ContentType, req.Size)
	if err != nil {
		WriteError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, gin.H{"upload": session, "grant": grant})
}

func (h *Handler) renew(c *gin.Context) {
	owner, ok := ownerID(c)
	if !ok {
		return
	}
	if h.cache == nil {
		WriteError(c, errors.New("upload renewal requires Redis"))
		return
	}
	count, err := h.cache.IncrementWithExpire(c.Request.Context(), h.cache.Key("oss_renew:%d", owner), time.Minute)
	if err != nil {
		WriteError(c, err)
		return
	}
	if count > 10 {
		WriteError(c, ErrLimit)
		return
	}
	session, grant, err := h.service.Renew(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		WriteError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"upload": session, "grant": grant})
}

func reply(c *gin.Context, session Session, err error) {
	if err != nil {
		WriteError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"upload": session})
}

func (h *Handler) status(c *gin.Context) {
	if owner, ok := ownerID(c); ok {
		session, err := h.service.Status(c.Request.Context(), owner, c.Param("id"))
		reply(c, session, err)
	}
}

func (h *Handler) complete(c *gin.Context) {
	if owner, ok := ownerID(c); ok {
		session, err := h.service.Complete(c.Request.Context(), owner, c.Param("id"))
		reply(c, session, err)
	}
}

func (h *Handler) cancel(c *gin.Context) {
	if owner, ok := ownerID(c); ok {
		session, err := h.service.Cancel(c.Request.Context(), owner, c.Param("id"))
		reply(c, session, err)
	}
}

func (h *Handler) ApplyAvatar(c *gin.Context) {
	owner, ok := ownerID(c)
	if !ok {
		return
	}
	var req struct {
		AssetID string `json:"asset_id"`
	}
	if err := BindJSON(c, &req); err != nil {
		WriteError(c, err)
		return
	}
	avatar, err := h.service.ApplyAvatar(c.Request.Context(), owner, req.AssetID)
	if err != nil {
		WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"avatar_url": avatar})
}

// UpdateBio accepts the current avatar in profile forms. Applying an uploaded
// avatar goes through ApplyAvatar, which checks ownership and validation state.
func (h *Handler) UpdateBio(c *gin.Context) {
	owner, ok := ownerID(c)
	if !ok {
		return
	}
	var req struct {
		Bio       *string `json:"bio"`
		AvatarURL string  `json:"avatar_url"`
	}
	if err := BindJSON(c, &req); err != nil {
		WriteError(c, err)
		return
	}
	db := h.service.db.WithContext(c.Request.Context())
	if req.AvatarURL != "" {
		var count int64
		if err := db.Model(&account.Account{}).Where("id = ? AND avatar_url = ?", owner, req.AvatarURL).Count(&count).Error; err != nil {
			WriteError(c, err)
			return
		}
		if count == 0 {
			WriteError(c, ErrRequest)
			return
		}
	}
	if req.Bio != nil {
		if bio := strings.TrimSpace(*req.Bio); len([]rune(bio)) <= 255 {
			if err := db.Model(&account.Account{}).Where("id = ?", owner).Update("bio", bio).Error; err != nil {
				WriteError(c, err)
				return
			}
		} else {
			WriteError(c, ErrRequest)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "profile updated"})
}

func (h *Handler) Media(c *gin.Context) {
	address, err := h.service.playback(c.Request.Context(), c.Param("id"), c.Request.Method)
	if err != nil {
		WriteError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Redirect(http.StatusTemporaryRedirect, address)
}

func (s *Service) playback(ctx context.Context, id, method string) (string, error) {
	if !validID(id) {
		return "", ErrNotFound
	}
	db := s.db.WithContext(ctx)
	var asset Session
	if err := db.Where("id = ? AND status = ?", id, Published).Find(&asset).Error; err != nil {
		return "", err
	}
	if asset.ID == "" {
		return "", ErrNotFound
	}
	var count int64
	switch asset.Kind {
	case Avatar:
		if err := db.Model(&account.Account{}).Where("id = ? AND avatar_url = ?", asset.AccountID, asset.PublicPath()).Count(&count).Error; err != nil {
			return "", err
		}
	case Video, Cover:
		condition := "id = ? AND author_id = ? AND play_url = ?"
		if asset.Kind == Cover {
			condition = "id = ? AND author_id = ? AND cover_url = ?"
		}
		if err := db.Table("videos").Where(condition, asset.VideoID, asset.AccountID, asset.PublicPath()).Count(&count).Error; err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("invalid stored asset kind")
	}
	if count == 0 {
		return "", ErrNotFound
	}
	return s.cloud.Sign(ctx, asset.FinalKey, method)
}
