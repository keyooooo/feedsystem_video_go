package http

import (
	"encoding/json"
	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/auth"
	appdb "feedsystem_video_go/internal/db"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
	mysqlgorm "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestSecureCookieSession(t *testing.T) {
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
	const origin = "https://demo.example.invalid"
	t.Setenv("SESSION_COOKIE_ORIGIN", origin)
	password, err := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	a := account.Account{Username: fmt.Sprintf("session-test-%d", time.Now().UnixNano()), Password: string(password)}
	if err := db.Create(&a).Error; err != nil {
		t.Fatal(err)
	}
	defer db.Delete(&a)
	router := SetRouter(db, nil, nil, nil)
	req := func(path, from, body, bearer string, cookie *nethttp.Cookie) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", origin+path, strings.NewReader(body))
		request.Header.Set("Origin", from)
		request.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			request.AddCookie(cookie)
		}
		if bearer != "" {
			request.Header.Set("Authorization", "Bearer "+bearer)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	read := func(rec *httptest.ResponseRecorder) (account.LoginResponse, *nethttp.Cookie) {
		t.Helper()
		cookies := rec.Result().Cookies()
		var response account.LoginResponse
		if rec.Code != 200 || len(cookies) != 1 || json.Unmarshal(rec.Body.Bytes(), &response) != nil {
			t.Fatalf("unexpected authentication response, status %d", rec.Code)
		}
		cookie := cookies[0]
		if !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" ||
			cookie.SameSite != nethttp.SameSiteStrictMode || response.RefreshToken != "" ||
			strings.Contains(rec.Body.String(), "refresh_token") || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("incorrect cookie attributes or refresh data exposure")
		}
		return response, cookie
	}
	body := fmt.Sprintf(`{"username":%q,"password":"test-password"}`, a.Username)
	if got := req("/account/login", "https://different.invalid", body, "", nil); got.Code != 403 {
		t.Fatal("accepted a cross-origin login")
	}
	initial, previous := read(req("/account/login", origin, body, "", nil))
	var saved account.Account
	if err := db.First(&saved, a.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.RefreshToken != "" || saved.SessionRefreshDigest == nil || *saved.SessionRefreshDigest != auth.DigestToken(previous.Value) ||
		saved.Token != auth.DigestToken(initial.Token) {
		t.Fatal("session tokens were not stored as hashes")
	}
	if got := req("/account/refresh", "", "{}", "", previous); got.Code != 403 {
		t.Fatal("refresh accepted without matching Origin")
	}
	out := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() { out <- req("/account/refresh", origin, "{}", "", previous) }()
	}
	var rotated account.LoginResponse
	var current *nethttp.Cookie
	successes := 0
	for range 2 {
		rec := <-out
		switch rec.Code {
		case 200:
			rotated, current = read(rec)
			successes++
		case 401:
			if len(rec.Result().Cookies()) != 0 {
				t.Fatal("old refresh response must not clear the rotated cookie")
			}
		default:
			t.Fatalf("unexpected rotation status %d", rec.Code)
		}
	}
	if successes != 1 || current == nil || current.Value == previous.Value || !current.Expires.Equal(previous.Expires) {
		t.Fatal("refresh must rotate once with the same absolute expiry")
	}
	if got := req("/account/rename", origin, "{}", initial.Token, nil); got.Code != 401 {
		t.Fatal("previous access token survived rotation")
	}
	if got := req("/account/logout", "https://different.invalid", "{}", "", current); got.Code != 403 {
		t.Fatal("accepted a cross-origin logout")
	}
	logout := req("/account/logout", origin, "{}", "", current)
	if cookies := logout.Result().Cookies(); logout.Code != 200 || len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Fatal("logout did not clear the cookie")
	}
	if req("/account/refresh", origin, "{}", "", current).Code != 401 ||
		req("/account/rename", origin, "{}", rotated.Token, nil).Code != 401 {
		t.Fatal("logout did not revoke both credentials")
	}
	_, expiring := read(req("/account/login", origin, body, "", nil))
	if err := db.Model(&a).Update("session_expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if got := req("/account/refresh", origin, "{}", "", expiring); got.Code != 401 {
		t.Fatal("expired cookie issued new credentials")
	}
}
