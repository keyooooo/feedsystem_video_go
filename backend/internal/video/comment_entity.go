package video

import "time"

type Comment struct {
	ModerationFolded  bool       `json:"moderation_folded,omitempty" gorm:"not null;default:false"`
	ModerationVersion uint       `json:"-" gorm:"not null;default:0"`
	ID                uint       `gorm:"primaryKey;index:idx_comment_video_thread,priority:3;index:idx_comment_thread_removed,priority:3" json:"id"`
	Username          string     `gorm:"index" json:"username"`
	VideoID           uint       `gorm:"index;index:idx_comment_video_thread,priority:1" json:"video_id"`
	AuthorID          uint       `gorm:"index" json:"author_id"`
	Content           string     `gorm:"type:text" json:"content"`
	CreatedAt         time.Time  `gorm:"autoCreateTime" json:"created_at"`
	RootCommentID     *uint      `gorm:"index:idx_comment_video_thread,priority:2;index:idx_comment_thread_removed,priority:1" json:"root_comment_id,omitempty"`
	ReplyToCommentID  *uint      `json:"reply_to_comment_id,omitempty"`
	RemovedAt         *time.Time `gorm:"index:idx_comment_thread_removed,priority:2" json:"removed_at,omitempty"`
}

type PublishCommentRequest struct {
	VideoID          uint   `json:"video_id"`
	Content          string `json:"content"`
	ReplyToCommentID *uint  `json:"reply_to_comment_id"`
}

type DeleteCommentRequest struct {
	CommentID uint `json:"comment_id"`
}

type GetAllCommentsRequest struct {
	VideoID uint `json:"video_id"`
}
