package video

import (
	"context"
	"fmt"
	"strings"

	"feedsystem_video_go/internal/apierror"
	"feedsystem_video_go/internal/middleware/jwt"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const threadPageSize = 20
const replyPageSize = 10

type commentThread struct {
	Comment
	CommentPresentation
	ReplyCount int `json:"reply_count"`
}

type commentReply struct {
	Comment
	CommentPresentation
	ReplyToUsername    string `json:"reply_to_username,omitempty"`
	ReplyTargetDeleted bool   `json:"reply_target_deleted"`
}

type commentPage[T any] struct {
	Comments   []T    `json:"comments"`
	NextCursor uint   `json:"next_cursor"`
	TotalCount *int64 `json:"total_count,omitempty"`
}

func (s *CommentService) publishReply(ctx context.Context, reply *Comment, targetID uint) (commentReply, error) {
	reply.Content = strings.TrimSpace(reply.Content)
	if targetID == 0 || reply.Content == "" {
		return commentReply{}, fmt.Errorf("%w: reply target and content are required", apierror.ErrValidation)
	}
	db := s.repo.db.WithContext(ctx)
	var target Comment
	if err := db.Where("video_id = ?", reply.VideoID).First(&target, targetID).Error; err != nil {
		return commentReply{}, err
	}
	rootID := target.ID
	if target.RootCommentID != nil {
		rootID = *target.RootCommentID
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		var root Comment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("video_id = ? AND root_comment_id IS NULL AND removed_at IS NULL", reply.VideoID).
			First(&root, rootID).Error; err != nil {
			return err
		}
		target = root
		if targetID != rootID {
			var parent Comment
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("video_id = ? AND root_comment_id = ? AND removed_at IS NULL", reply.VideoID, rootID).
				First(&parent, targetID).Error; err != nil {
				return err
			}
			target = parent
		}
		reply.RootCommentID = &rootID
		reply.ReplyToCommentID = &targetID
		return tx.Create(reply).Error
	})
	return commentReply{Comment: *reply, ReplyToUsername: target.Username}, err
}

func (r *CommentRepository) listThreads(ctx context.Context, videoID, cursor, viewer uint) (commentPage[commentThread], error) {
	page := commentPage[commentThread]{Comments: []commentThread{}}
	query := r.db.WithContext(ctx).Table("comments AS root").
		Select("root.*, (SELECT COUNT(*) FROM comments AS reply WHERE reply.root_comment_id = root.id AND reply.removed_at IS NULL) AS reply_count"+commentPresentationSQL("root"), viewer).
		Joins("LEFT JOIN accounts AS author ON author.id = root.author_id").
		Where("root.video_id = ? AND root.root_comment_id IS NULL AND root.moderation_deleted = FALSE", videoID).
		Where("root.removed_at IS NULL OR EXISTS (SELECT 1 FROM comments AS reply WHERE reply.root_comment_id = root.id AND reply.removed_at IS NULL)")
	if cursor != 0 {
		query = query.Where(`(root.moderation_folded > COALESCE((SELECT moderation_folded FROM comments WHERE id = ?), 0)) OR
 (root.moderation_folded = COALESCE((SELECT moderation_folded FROM comments WHERE id = ?), 0) AND root.id < ?)`, cursor, cursor, cursor)
	}
	if err := query.Order("root.moderation_folded ASC, root.id DESC").Limit(threadPageSize + 1).Scan(&page.Comments).Error; err != nil {
		return page, err
	}
	if len(page.Comments) > threadPageSize {
		page.Comments = page.Comments[:threadPageSize]
		page.NextCursor = page.Comments[threadPageSize-1].ID
	}
	if cursor == 0 {
		var total int64
		if err := r.db.WithContext(ctx).Table("comments AS visible").Where("visible.video_id = ? AND visible.removed_at IS NULL", videoID).Where("NOT EXISTS (SELECT 1 FROM comments AS root WHERE root.id = visible.root_comment_id AND root.moderation_deleted = TRUE)").Count(&total).Error; err != nil {
			return page, err
		}
		page.TotalCount = &total
	}
	for i := range page.Comments {
		if page.Comments[i].RemovedAt != nil || page.Comments[i].ModerationFolded {
			page.Comments[i].Content = ""
		}
	}
	return page, nil
}

func (r *CommentRepository) listReplies(ctx context.Context, videoID, rootID, cursor, viewer uint) (commentPage[commentReply], error) {
	page := commentPage[commentReply]{Comments: []commentReply{}}
	var root Comment
	if err := r.db.WithContext(ctx).Select("id").
		Where("video_id = ? AND root_comment_id IS NULL AND moderation_deleted = FALSE", videoID).First(&root, rootID).Error; err != nil {
		return page, err
	}
	query := r.db.WithContext(ctx).Table("comments AS reply").
		Select(`reply.*, CASE WHEN target.removed_at IS NULL THEN COALESCE(target.username, '') ELSE '' END AS reply_to_username,
			CASE WHEN target.id IS NULL OR target.removed_at IS NOT NULL THEN TRUE ELSE FALSE END AS reply_target_deleted`+commentPresentationSQL("reply"), viewer).
		Joins("LEFT JOIN accounts AS author ON author.id = reply.author_id").
		Joins("LEFT JOIN comments AS target ON target.id = reply.reply_to_comment_id").
		Where("reply.root_comment_id = ? AND reply.removed_at IS NULL", rootID)
	if cursor != 0 {
		query = query.Where(`(reply.moderation_folded > COALESCE((SELECT moderation_folded FROM comments WHERE id = ?), 0)) OR
 (reply.moderation_folded = COALESCE((SELECT moderation_folded FROM comments WHERE id = ?), 0) AND reply.id > ?)`, cursor, cursor, cursor)
	}
	err := query.Order("reply.moderation_folded ASC, reply.id ASC").Limit(replyPageSize + 1).Scan(&page.Comments).Error
	if len(page.Comments) > replyPageSize {
		page.Comments = page.Comments[:replyPageSize]
		page.NextCursor = page.Comments[replyPageSize-1].ID
	}
	for i := range page.Comments {
		if page.Comments[i].ModerationFolded {
			page.Comments[i].Content = ""
		}
	}
	return page, err
}

type listCommentPageRequest struct {
	VideoID       uint `json:"video_id" binding:"required"`
	RootCommentID uint `json:"root_comment_id"`
	Cursor        uint `json:"cursor"`
}

func (h *CommentHandler) ListThreads(c *gin.Context) {
	var req listCommentPageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "valid video_id required"})
		return
	}
	viewer, _ := jwt.GetAccountID(c)
	page, err := h.service.repo.listThreads(c.Request.Context(), req.VideoID, req.Cursor, viewer)
	if err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": "could not load comments"})
		return
	}
	c.JSON(200, page)
}

func (h *CommentHandler) ListReplies(c *gin.Context) {
	var req listCommentPageRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.RootCommentID == 0 {
		c.JSON(400, gin.H{"error": "valid video_id and root_comment_id required"})
		return
	}
	viewer, _ := jwt.GetAccountID(c)
	page, err := h.service.repo.listReplies(c.Request.Context(), req.VideoID, req.RootCommentID, req.Cursor, viewer)
	if err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": "could not load replies"})
		return
	}
	c.JSON(200, page)
}
