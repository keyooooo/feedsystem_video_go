package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/auth"
	appdb "feedsystem_video_go/internal/db"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/upload"
	"feedsystem_video_go/internal/video"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-sql-driver/mysql"
	goredis "github.com/redis/go-redis/v9"
	mysqlgorm "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type uploadCloud struct {
	verifyMu sync.Mutex
	modes    map[string]error
	calls    int
}

func (fake *uploadCloud) Grant(_ context.Context, key, kind string) (upload.Grant, error) {
	return upload.Grant{Key: key, ExpiresAt: time.Now().UTC().Add(15 * time.Minute)}, nil
}

func (fake *uploadCloud) Verify(_ context.Context, staging, final, kind, contentType string, size int64) error {
	fake.verifyMu.Lock()
	defer fake.verifyMu.Unlock()
	fake.calls++
	return fake.modes[staging]
}

func (fake *uploadCloud) Sign(_ context.Context, key, method string) (string, error) {
	return "https://bucket.example.invalid/" + key, nil
}

func (fake *uploadCloud) Delete(_ context.Context, key string) error { return nil }

type response struct {
	Upload upload.Session
	Grant  upload.Grant
	ID     uint
	Value  string `json:"play_url"`
}

func TestOSSUploadAPI(t *testing.T) {
	dsn := os.Getenv("OSS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("OSS_TEST_MYSQL_DSN required for MySQL integration")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasSuffix(cfg.DBName, "_test") {
		t.Fatal("use a dedicated MySQL test database ending in _test")
	}
	db, err := gorm.Open(mysqlgorm.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := appdb.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	cache := rediscache.NewClient(goredis.NewClient(&goredis.Options{Addr: miniredis.RunT(t).Addr()}), "test:")
	defer cache.Close()
	username := fmt.Sprintf("oss-test-%d", time.Now().UnixNano())
	var owner, peer account.Account
	tokens := make(map[uint]string)
	for index, user := range []*account.Account{&owner, &peer} {
		user.Username = fmt.Sprintf("%s-%d", username, index)
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
		token, err := auth.GenerateToken(user.ID, user.Username)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Model(user).Update("token", token).Error; err != nil {
			t.Fatal(err)
		}
		tokens[user.ID] = token
	}
	cloud := &uploadCloud{modes: make(map[string]error)}
	router := SetRouter(db, cache, nil, upload.NewService(db, cloud))
	req := func(method, path, body string, owner uint) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if owner != 0 {
			request.Header.Set("Authorization", "Bearer "+tokens[owner])
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, request)
		return rec
	}
	expect := func(method, path, body string, owner uint, status int) response {
		t.Helper()
		rec := req(method, path, body, owner)
		if rec.Code != status {
			t.Fatalf("%s %s returned %d, wanted %d: %s", method, path, rec.Code, status, rec.Body.String())
		}
		var result response
		if rec.Body.Len() > 0 && strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	const videoBody = `{"kind":"video","content_type":"video/mp4","size":128}`
	const imageBody = `{"kind":"cover","content_type":"image/png","size":10}`
	expect("POST", "/uploads", videoBody, 0, 401)
	expect("POST", "/uploads", `{"kind":"video","content_type":"video/mp4","size":999999999}`, owner.ID, 400)
	create := func(body string) response { return expect("POST", "/uploads", body, owner.ID, 201) }
	film, cover := create(videoBody), create(imageBody)
	base := "/uploads/" + film.Upload.ID
	expect("GET", base, "", peer.ID, 404)
	expect("POST", base+"/credentials", "", peer.ID, 404)
	expect("POST", base+"/complete", "", peer.ID, 404)
	expect("POST", base+"/cancel", "", peer.ID, 404)
	expect("GET", "/media/assets/"+film.Upload.ID, "", 0, 404)
	expect("GET", "/media/assets/"+film.Upload.ID+"?resolve=1", "", 0, 404)
	if want := expect("POST", base+"/credentials", "", owner.ID, 200); want.Grant.Key != film.Grant.Key {
		t.Fatal("renewed key differs from session")
	}
	cloud.modes[film.Grant.Key] = upload.ErrObjectPending
	expect("POST", base+"/complete", "", owner.ID, 409)
	delete(cloud.modes, film.Grant.Key)
	expect("POST", base+"/complete", "", owner.ID, 200)
	expect("POST", base+"/complete", "", owner.ID, 200)
	if cloud.calls != 2 {
		t.Fatalf("unexpected repeated verification, calls=%d", cloud.calls)
	}
	expect("POST", "/uploads/"+cover.Upload.ID+"/complete", "", owner.ID, 200)
	publish := fmt.Sprintf(`{"title":"demo #oss","video_asset_id":%q,"cover_asset_id":%q}`, film.Upload.ID, cover.Upload.ID)
	expect("POST", "/video/publish", `{"title":"bad","play_url":"https://other.example/v.mp4"}`, owner.ID, 400)
	expect("POST", "/video/publish", publish, peer.ID, 404)
	result := expect("POST", "/video/publish", publish, owner.ID, 200)
	if retry := expect("POST", "/video/publish", publish, owner.ID, 200); retry.ID != result.ID || result.Value != "/api/media/assets/"+film.Upload.ID {
		t.Fatalf("unstable publication %#v", result)
	}
	expect("POST", base+"/cancel", "", owner.ID, 409)
	for _, method := range []string{"GET", "HEAD"} {
		rec := req(method, "/media/assets/"+film.Upload.ID, "", 0)
		if rec.Code != 307 || !strings.HasPrefix(rec.Header().Get("Location"), "https://bucket.example.invalid/direct-upload/assets/video/") ||
			rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("invalid media redirect: %d %v", rec.Code, rec.Header())
		}
	}
	resolved := req("GET", "/media/assets/"+film.Upload.ID+"?resolve=1", "", 0)
	if resolved.Code != 200 || resolved.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(resolved.Body.String(), "https://bucket.example.invalid/direct-upload/assets/video/") {
		t.Fatalf("invalid resolved media address: %d %v", resolved.Code, resolved.Header())
	}
	failed := create(videoBody)
	cloud.modes[failed.Grant.Key] = upload.ErrInvalidMedia
	expect("POST", "/uploads/"+failed.Upload.ID+"/complete", "", owner.ID, 422)
	if got := expect("GET", "/uploads/"+failed.Upload.ID, "", owner.ID, 200).Upload.Status; got != upload.Failed {
		t.Fatalf("failed asset status = %s", got)
	}
	replacement := create(imageBody)
	expect("POST", "/uploads/"+replacement.Upload.ID+"/complete", "", owner.ID, 200)
	expect("POST", "/video/publish", fmt.Sprintf(`{"title":"broken","video_asset_id":%q,"cover_asset_id":%q}`, failed.Upload.ID, replacement.Upload.ID), owner.ID, 409)
	expired := create(videoBody)
	if err := db.Model(&expired.Upload).Update("expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	expect("POST", "/uploads/"+expired.Upload.ID+"/credentials", "", owner.ID, 410)
	expect("POST", "/uploads/"+expired.Upload.ID+"/complete", "", owner.ID, 410)
	newFilm := create(videoBody)
	expect("POST", "/uploads/"+newFilm.Upload.ID+"/complete", "", owner.ID, 200)
	racePublish := fmt.Sprintf(`{"title":"race","video_asset_id":%q,"cover_asset_id":%q}`, newFilm.Upload.ID, replacement.Upload.ID)
	results := make(chan int, 2)
	go func() { results <- req("POST", "/video/publish", racePublish, owner.ID).Code }()
	go func() { results <- req("POST", "/uploads/"+newFilm.Upload.ID+"/cancel", "", owner.ID).Code }()
	a, b := <-results, <-results
	if a+b != 609 || (a != 200 && b != 200) {
		t.Fatalf("cancel and publish must have one winner, got %d and %d", a, b)
	}
	const large = `{"kind":"video","content_type":"video/mp4","size":209715200}`
	create(large)
	create(large)
	expect("POST", "/uploads", large, owner.ID, 429)
	avatar := create(`{"kind":"avatar","content_type":"image/png","size":200}`)
	expect("POST", "/account/applyAvatar", fmt.Sprintf(`{"asset_id":%q}`, avatar.Upload.ID), owner.ID, 409)
	expect("POST", "/uploads/"+avatar.Upload.ID+"/complete", "", owner.ID, 200)
	expect("POST", "/account/applyAvatar", fmt.Sprintf(`{"asset_id":%q}`, avatar.Upload.ID), owner.ID, 200)
	expect("GET", "/media/assets/"+avatar.Upload.ID, "", 0, 307)
	legacy := video.Video{AuthorID: owner.ID, Username: owner.Username, Title: "legacy", PlayURL: "/static/videos/1/old.mp4", CoverURL: "https://cdn.example/old.jpg"}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	expect("POST", "/video/getDetail", fmt.Sprintf(`{"id":%d}`, legacy.ID), 0, 200)
	var saved video.Video
	if err := db.First(&saved, legacy.ID).Error; err != nil || saved.PlayURL != legacy.PlayURL {
		t.Fatal("legacy media reference was overwritten", err)
	}
}
