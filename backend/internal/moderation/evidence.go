package moderation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"feedsystem_video_go/internal/video"
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
)

type Evidence struct {
	ID          string    `json:"id"`
	Source      string    `json:"source"`
	Status      string    `json:"status"`
	Text        string    `json:"text,omitempty"`
	CollectedAt time.Time `json:"collected_at"`
}

type ToolTrace struct {
	Round       int      `json:"round"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	Reason      string   `json:"reason,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	DurationMS  int64    `json:"duration_ms,omitempty"`
}

type Snapshot struct {
	Decision        *DecisionIntent `json:"decision_intent,omitempty"`
	Tools           []ToolTrace     `json:"tool_trace,omitempty"`
	VerifiedChannel bool            `json:"verified_channel,omitempty"`
	Version         int             `json:"version"`
	TargetDigest    string          `json:"target_digest,omitempty"`
	TargetVersion   uint            `json:"target_version"`
	TargetRemoved   bool            `json:"target_removed,omitempty"`
	NeedsContext    bool            `json:"needs_context,omitempty"`
	Items           []Evidence      `json:"items"`
	target          video.Comment
}

var contactPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)https?://[^\s]+|www\.[^\s]+`), "[链接]"},
	{regexp.MustCompile(`(?i)[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}`), "[邮箱]"},
	{regexp.MustCompile(`[0-9][0-9 .()+-]{6,}[0-9]`), "[号码]"},
	{regexp.MustCompile(`(?i)(?:微信|加微|wechat|vx|qq|tg|telegram)\s*[:：]?\s*[a-z0-9_-]{4,}`), "[联系方式]"},
}

func evidenceText(raw string) string {
	for _, contact := range contactPatterns {
		raw = contact.pattern.ReplaceAllString(raw, contact.replacement)
	}
	chars := []rune(strings.TrimSpace(raw))
	if len(chars) > 512 {
		return string(chars[:512])
	}
	return string(chars)
}

func collectSource(id, source string, load func() (string, error)) Evidence {
	item := Evidence{ID: id, Source: source, CollectedAt: time.Now().UTC()}
	raw, err := load()
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		item.Status = "empty"
	case err != nil:
		item.Status = "unavailable"
	default:
		item.Text = evidenceText(raw)
		item.Status = "ok"
		if item.Text == "" {
			item.Status = "empty"
		}
	}
	return item
}

func CollectEvidence(ctx context.Context, db *gorm.DB, report Report) (Snapshot, error) {
	snapshot := Snapshot{Version: 1}
	err := db.WithContext(ctx).Select("id", "video_id", "author_id", "content", "root_comment_id", "reply_to_comment_id", "removed_at", "moderation_version").First(&snapshot.target, report.CommentID).Error
	if err != nil {
		return snapshot, err
	}
	target := snapshot.target
	snapshot.TargetVersion = target.ModerationVersion
	snapshot.TargetRemoved = target.RemovedAt != nil
	if snapshot.TargetRemoved {
		return snapshot, nil
	}
	digest := sha256.Sum256([]byte(target.Content))
	snapshot.TargetDigest = hex.EncodeToString(digest[:])
	snapshot.VerifiedChannel = hasOriginalChannel(target.Content)
	snapshot.NeedsContext = target.ReplyToCommentID != nil
	snapshot.Items = make([]Evidence, 6)
	snapshot.Items[0] = collectSource("target:1", "target", func() (string, error) { return target.Content, nil })
	snapshot.Items[1] = collectSource("report:1", "report", func() (string, error) { return report.ReasonCode + " " + report.Detail, nil })
	group, parallel := errgroup.WithContext(ctx)
	sources := []struct {
		id, source string
		fetch      func(*gorm.DB) (string, error)
	}{
		{"video:1", "video", func(tx *gorm.DB) (string, error) {
			var row struct{ Title, Description string }
			err := tx.Table("videos").Select("title", "description").Where("id = ?", target.VideoID).Take(&row).Error
			return strings.TrimSpace(row.Title + "\n" + row.Description), err
		}},
		{"author:1", "author", func(tx *gorm.DB) (string, error) {
			var row struct{ Bio string }
			err := tx.Table("accounts").Select("bio").Where("id = ?", target.AuthorID).Take(&row).Error
			return row.Bio, err
		}},
		{"parent:1", "parent", func(tx *gorm.DB) (string, error) {
			if target.ReplyToCommentID == nil {
				return "", nil
			}
			var parent video.Comment
			err := threadScope(tx, target).Select("content").Where("id = ?", *target.ReplyToCommentID).Take(&parent).Error
			return parent.Content, err
		}},
		{"history:1", "history", func(tx *gorm.DB) (string, error) {
			var actions []string
			err := tx.Table("moderation_decisions d").Joins("JOIN moderation_cases c ON c.id = d.case_id AND c.state = 'resolved'").
				Joins("JOIN moderation_reports r ON r.id = c.report_id").
				Where("r.comment_id = ? AND r.id <> ? AND d.id = (SELECT MAX(latest.id) FROM moderation_decisions latest WHERE latest.case_id = c.id)", target.ID, report.ID).
				Order("d.id DESC").Limit(5).Pluck("d.action", &actions).Error
			return strings.Join(actions, ", "), err
		}},
	}
	for i, source := range sources {
		group.Go(func() error {
			query, cancel := context.WithTimeout(parallel, 2*time.Second)
			defer cancel()
			snapshot.Items[i+2] = collectSource(source.id, source.source, func() (string, error) { return source.fetch(db.WithContext(query)) })
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return snapshot, err
	}
	return snapshot, ctx.Err()
}

func threadScope(db *gorm.DB, target video.Comment) *gorm.DB {
	root := target.ID
	if target.RootCommentID != nil {
		root = *target.RootCommentID
	}
	return db.Model(&video.Comment{}).Where("video_id = ? AND removed_at IS NULL AND ((id = ? AND root_comment_id IS NULL) OR root_comment_id = ?)", target.VideoID, root, root)
}

// ThreadContext derives its scope from the loaded comment. The caller can
// choose the context shape and limit but cannot supply database identifiers.
func (s Snapshot) ThreadContext(ctx context.Context, db *gorm.DB, scope string, limit int) ([]Evidence, error) {
	if s.target.ID == 0 || s.TargetRemoved || limit < 1 || limit > 5 {
		return nil, errors.New("invalid thread context")
	}
	tx := threadScope(db.WithContext(ctx), s.target)
	var rows []video.Comment
	switch scope {
	case "parent_chain":
		parent := s.target.ReplyToCommentID
		seen := map[uint]bool{s.target.ID: true}
		for parent != nil && len(rows) < limit && !seen[*parent] {
			seen[*parent] = true
			var row video.Comment
			err := tx.Session(&gorm.Session{}).Select("id", "content", "reply_to_comment_id").Where("id = ?", *parent).Take(&row).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				break
			}
			if err != nil {
				return nil, err
			}
			rows = append(rows, row)
			parent = row.ReplyToCommentID
		}
	case "neighbors":
		if err := tx.Select("id", "content").Where("id < ?", s.target.ID).Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("unknown thread scope")
	}
	items := make([]Evidence, 0, len(rows))
	for _, row := range rows {
		items = append(items, collectSource(fmt.Sprintf("%s:%d", scope, row.ID), "thread", func() (string, error) { return row.Content, nil }))
	}
	return items, nil
}
