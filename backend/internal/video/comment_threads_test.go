package video

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	mysqlgorm "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestCommentThreadsMySQL(t *testing.T) {
	dsn := os.Getenv("COMMENT_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("COMMENT_TEST_MYSQL_DSN required for MySQL integration")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasSuffix(cfg.DBName, "_test") {
		t.Fatal("use a dedicated MySQL test database ending in _test")
	}
	db, err := gorm.Open(mysqlgorm.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Comment{}, &CommentLike{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	videoID := uint(time.Now().UnixNano())
	t.Cleanup(func() {
		db.Where("comment_id IN (?)", db.Model(&Comment{}).Select("id").Where("video_id = ?", videoID)).Delete(&CommentLike{})
		db.Where("video_id = ?", videoID).Delete(&Comment{})
	})
	repo := NewCommentRepository(db)
	service := NewCommentService(repo, nil, nil, nil, nil)
	roots := make([]Comment, threadPageSize+1)
	for i := range roots {
		roots[i] = Comment{VideoID: videoID, AuthorID: 1, Username: "root", Content: "old comment"}
	}
	if err := db.Create(&roots).Error; err != nil {
		t.Fatal(err)
	}
	root := roots[len(roots)-1]
	reply := func(id uint, video uint) (commentReply, error) {
		return service.publishReply(ctx, &Comment{VideoID: video, AuthorID: 2, Username: "reply", Content: "reply text"}, id)
	}
	first, err := reply(root.ID, videoID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := reply(first.ID, videoID)
	if err != nil || second.RootCommentID == nil || *second.RootCommentID != root.ID ||
		second.ReplyToCommentID == nil || *second.ReplyToCommentID != first.ID {
		t.Fatalf("nested reply did not stay in the original thread: %v", err)
	}
	if _, err := reply(root.ID, videoID+1); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("accepted cross-video reply: %v", err)
	}
	if _, err := service.Delete(ctx, first.ID, 1); err == nil {
		t.Fatal("accepted deletion by another account")
	}
	page, err := repo.listThreads(ctx, videoID, 0, 0)
	if err != nil || len(page.Comments) != threadPageSize || page.NextCursor == 0 || page.Comments[0].ReplyCount != 2 {
		t.Fatalf("unexpected first page: %#v, %v", page, err)
	}
	likeResults := make(chan error, 2)
	for range 2 {
		go func() {
			state, err := repo.setLike(ctx, root.ID, 2, true)
			if err == nil && (!state.IsLiked || state.LikesCount != 1) {
				err = errors.New("duplicate like changed the count")
			}
			likeResults <- err
		}()
	}
	for range 2 {
		if err := <-likeResults; err != nil {
			t.Fatal(err)
		}
	}
	if state, err := repo.setLike(ctx, first.ID, 1, true); err != nil || state.LikesCount != 1 {
		t.Fatalf("reply like failed: %#v, %v", state, err)
	}
	for _, viewer := range []uint{0, 1, 2} {
		got, err := repo.listThreads(ctx, videoID, 0, viewer)
		if err != nil || got.Comments[0].LikesCount != 1 || got.Comments[0].IsLiked != (viewer == 2) ||
			got.TotalCount == nil || *got.TotalCount != int64(len(roots)+2) {
			t.Fatalf("unexpected viewer state (%d): %#v, %v", viewer, got, err)
		}
	}
	for range 2 {
		if state, err := repo.setLike(ctx, root.ID, 2, false); err != nil || state.IsLiked || state.LikesCount != 0 {
			t.Fatalf("unexpected unlike state: %#v, %v", state, err)
		}
	}
	next, err := repo.listThreads(ctx, videoID, page.NextCursor, 0)
	if err != nil || len(next.Comments) != 1 || next.Comments[0].ID != roots[0].ID || next.NextCursor != 0 {
		t.Fatalf("unexpected next page: %#v, %v", next, err)
	}
	other := roots[1]
	for range replyPageSize + 1 {
		if _, err := reply(other.ID, videoID); err != nil {
			t.Fatal(err)
		}
	}
	firstReplies, err := repo.listReplies(ctx, videoID, other.ID, 0, 0)
	if err != nil || len(firstReplies.Comments) != replyPageSize || firstReplies.NextCursor == 0 {
		t.Fatalf("unexpected replies page: %#v, %v", firstReplies, err)
	}
	nextReplies, err := repo.listReplies(ctx, videoID, other.ID, firstReplies.NextCursor, 0)
	if err != nil || len(nextReplies.Comments) != 1 || nextReplies.NextCursor != 0 {
		t.Fatalf("unexpected next replies page: %#v, %v", nextReplies, err)
	}
	for _, id := range []uint{root.ID, firstReplies.Comments[0].ID} {
		if err := db.Model(&Comment{}).Where("id = ?", id).Update("moderation_folded", true).Error; err != nil {
			t.Fatal(err)
		}
	}
	page, err = repo.listThreads(ctx, videoID, 0, 0)
	if err != nil || len(page.Comments) != threadPageSize || page.NextCursor != roots[0].ID {
		t.Fatalf("folded root displaced a visible root: %+v (%v)", page, err)
	}
	next, err = repo.listThreads(ctx, videoID, page.NextCursor, 0)
	if err != nil || len(next.Comments) != 1 || next.Comments[0].ID != root.ID || next.Comments[0].Content != "" || !next.Comments[0].ModerationFolded {
		t.Fatalf("expected masked folded root on final page: %+v (%v)", next, err)
	}
	firstReplies, err = repo.listReplies(ctx, videoID, other.ID, 0, 0)
	if err != nil || len(firstReplies.Comments) != replyPageSize || firstReplies.Comments[0].ModerationFolded {
		t.Fatalf("folded reply displaced a visible reply: %+v (%v)", firstReplies, err)
	}
	nextReplies, err = repo.listReplies(ctx, videoID, other.ID, firstReplies.NextCursor, 0)
	if err != nil || len(nextReplies.Comments) != 1 || nextReplies.Comments[0].Content != "" || !nextReplies.Comments[0].ModerationFolded {
		t.Fatalf("expected masked folded reply on final page: %+v (%v)", nextReplies, err)
	}
	if err := db.Model(&Comment{}).Where("id = ?", root.ID).Update("moderation_folded", false).Error; err != nil {
		t.Fatal(err)
	}
	raceRoot := roots[2]
	done := make(chan error, 1)
	go func() {
		_, err := reply(raceRoot.ID, videoID)
		done <- err
	}()
	_, deleteErr := service.Delete(ctx, raceRoot.ID, 1)
	replyErr := <-done
	if deleteErr != nil || (replyErr != nil && !errors.Is(replyErr, gorm.ErrRecordNotFound)) {
		t.Fatalf("concurrent reply and delete: reply=%v delete=%v", replyErr, deleteErr)
	}
	if _, err := reply(raceRoot.ID, videoID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("deleted thread remained writable: %v", err)
	}
	if removed, err := service.Delete(ctx, first.ID, 2); err != nil || removed != 1 {
		t.Fatalf("delete reply: %d, %v", removed, err)
	}
	replies, err := repo.listReplies(ctx, videoID, root.ID, 0, 0)
	if err != nil || len(replies.Comments) != 1 || replies.Comments[0].ID != second.ID ||
		!replies.Comments[0].ReplyTargetDeleted || replies.Comments[0].ReplyToUsername != "reply" {
		t.Fatalf("unexpected attribution after reply deletion: %#v, %v", replies, err)
	}
	if removed, err := service.Delete(ctx, root.ID, 1); err != nil || removed != 2 {
		t.Fatalf("delete root and remaining reply: %d, %v", removed, err)
	}
	if removed, err := service.Delete(ctx, root.ID, 1); err != nil || removed != 0 {
		t.Fatalf("delete already removed root: %d, %v", removed, err)
	}
	var stored Comment
	if err := db.First(&stored, second.ID).Error; err != nil || stored.RemovedAt == nil || stored.Content != "" {
		t.Fatalf("reply not removed by cascade: %#v, %v", stored, err)
	}
	if _, err := repo.setLike(ctx, second.ID, 2, true); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("accepted a like for a cascaded reply: %v", err)
	}
	if _, err := reply(second.ID, videoID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("accepted reply to a deleted thread: %v", err)
	}

	// Simulate data written before root deletion cascaded.
	legacyRoot := roots[3]
	legacyReply, err := reply(legacyRoot.ID, videoID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&legacyRoot).Update("removed_at", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.setLike(ctx, legacyReply.ID, 2, true); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("accepted like on legacy orphan reply: %v", err)
	}
	for _, id := range []uint{root.ID, raceRoot.ID, legacyRoot.ID} {
		if _, err := repo.listReplies(ctx, videoID, id, 0, 0); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("deleted thread %d still readable: %v", id, err)
		}
	}
	page, err = repo.listThreads(ctx, videoID, 0, 0)
	wantRoots := len(roots) - 3
	if err != nil || len(page.Comments) != wantRoots || page.TotalCount == nil || *page.TotalCount != int64(wantRoots+replyPageSize+1) {
		t.Fatalf("deleted threads or replies remained visible: %#v, %v", page, err)
	}
	legacy, err := repo.GetAllComments(ctx, videoID)
	if err != nil || len(legacy) != wantRoots {
		t.Fatalf("legacy list should return only visible roots: %d, %v", len(legacy), err)
	}
}
