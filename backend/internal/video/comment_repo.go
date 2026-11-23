package video

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CommentRepository struct {
	db *gorm.DB
}

func NewCommentRepository(db *gorm.DB) *CommentRepository {
	return &CommentRepository{db: db}
}

func (r *CommentRepository) CreateComment(ctx context.Context, comment *Comment) error {
	return r.db.WithContext(ctx).Create(comment).Error
}

func (r *CommentRepository) DeleteComment(ctx context.Context, comment *Comment) (int64, error) {
	db := r.db.WithContext(ctx)
	updates := map[string]any{"removed_at": time.Now().UTC(), "content": ""}
	if comment.RootCommentID != nil {
		result := db.Model(&Comment{}).Where("id = ? AND removed_at IS NULL", comment.ID).Updates(updates)
		return result.RowsAffected, result.Error
	}
	var removed int64
	err := db.Transaction(func(tx *gorm.DB) error {
		// Reply creation locks this same root before writing any reply.
		var root Comment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&root, comment.ID).Error; err != nil {
			return err
		}
		result := tx.Model(&Comment{}).
			Where("video_id = ? AND (id = ? OR root_comment_id = ?) AND removed_at IS NULL", comment.VideoID, root.ID, root.ID).
			Updates(updates)
		removed = result.RowsAffected
		return result.Error
	})
	return removed, err
}

func (r *CommentRepository) GetAllComments(ctx context.Context, videoID uint) ([]Comment, error) {
	var comments []Comment
	err := r.db.WithContext(ctx).
		Where("video_id = ? AND root_comment_id IS NULL AND removed_at IS NULL", videoID).
		Order("created_at asc").
		Limit(200).
		Find(&comments).Error
	return comments, err
}

func (r *CommentRepository) IsExist(ctx context.Context, id uint) (bool, error) {
	var comment Comment
	if err := r.db.WithContext(ctx).First(&comment, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *CommentRepository) GetByID(ctx context.Context, id uint) (*Comment, error) {
	var comment Comment
	if err := r.db.WithContext(ctx).First(&comment, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &comment, nil
}
