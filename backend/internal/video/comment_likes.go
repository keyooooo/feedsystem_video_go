package video

import (
	"context"

	"feedsystem_video_go/internal/apierror"
	"feedsystem_video_go/internal/middleware/jwt"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CommentLike struct {
	CommentID uint `gorm:"primaryKey;autoIncrement:false"`
	AccountID uint `gorm:"primaryKey;autoIncrement:false"`
}

type CommentLikeState struct {
	LikesCount int64 `json:"likes_count"`
	IsLiked    bool  `json:"is_liked"`
}

type CommentPresentation struct {
	AvatarURL string `json:"avatar_url,omitempty"`
	CommentLikeState
}

// Both aliases are static names from the corresponding page queries.
func commentPresentationSQL(alias string) string {
	return ", COALESCE(author.avatar_url, '') AS avatar_url, " +
		"CASE WHEN " + alias + ".removed_at IS NULL THEN (SELECT COUNT(*) FROM comment_likes WHERE comment_id = " + alias + ".id) ELSE 0 END AS likes_count, " +
		"(" + alias + ".removed_at IS NULL AND EXISTS (SELECT 1 FROM comment_likes WHERE comment_id = " + alias + ".id AND account_id = ?)) AS is_liked"
}

func (r *CommentRepository) setLike(ctx context.Context, commentID, accountID uint, liked bool) (CommentLikeState, error) {
	var state CommentLikeState
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var comment Comment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id").Where("removed_at IS NULL").First(&comment, commentID).Error; err != nil {
			return err
		}
		relation := CommentLike{CommentID: commentID, AccountID: accountID}
		if liked {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&relation).Error; err != nil {
				return err
			}
		} else if err := tx.Delete(&relation).Error; err != nil {
			return err
		}
		state.IsLiked = liked
		return tx.Model(&CommentLike{}).Where("comment_id = ?", commentID).Count(&state.LikesCount).Error
	})
	return state, err
}

func (h *CommentHandler) SetLike(c *gin.Context) {
	var req struct {
		CommentID uint  `json:"comment_id" binding:"required"`
		Liked     *bool `json:"liked" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "comment_id and liked are required"})
		return
	}
	accountID, err := jwt.GetAccountID(c)
	if err != nil {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}
	state, err := h.service.repo.setLike(c.Request.Context(), req.CommentID, accountID, *req.Liked)
	if err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": "could not update comment like"})
		return
	}
	c.JSON(200, state)
}
