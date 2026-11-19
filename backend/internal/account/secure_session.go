package account

import (
	"context"
	"encoding/hex"
	"errors"
	"feedsystem_video_go/internal/auth"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const refreshCookie = "__Host-feedsystem-refresh"
const sessionLifetime = 7 * 24 * time.Hour

var errInvalidSession = errors.New("invalid session")

func acceptSessionOrigin(c *gin.Context) bool {
	if c.GetHeader("Origin") != auth.CookieOrigin() {
		c.JSON(http.StatusForbidden, gin.H{"error": "session origin not allowed"})
		return false
	}
	c.Header("Cache-Control", "no-store")
	return true
}

func setRefreshCookie(c *gin.Context, value string, expires time.Time) {
	maxAge := int(time.Until(expires).Seconds())
	if value == "" || maxAge < 1 {
		maxAge = -1
		expires = time.Unix(1, 0)
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: refreshCookie, Value: value, Path: "/", HttpOnly: true, Secure: true,
		SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: maxAge,
	})
}

func cookieToken(c *gin.Context) string {
	value, err := c.Cookie(refreshCookie)
	if err != nil || len(value) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}

func newSessionTokens(a Account) (access, refresh string, err error) {
	access, err = auth.GenerateToken(a.ID, a.Username)
	if err != nil {
		return "", "", err
	}
	refresh, err = auth.GenerateRefreshToken(a.ID)
	return
}

func secureTokens(access, refresh string, expires time.Time) map[string]interface{} {
	return map[string]interface{}{
		"token": auth.DigestToken(access), "refresh_token": "",
		"session_refresh_digest": auth.DigestToken(refresh), "session_expires_at": expires,
	}
}

func endSessionFields() map[string]interface{} {
	return map[string]interface{}{
		"token": "", "refresh_token": "",
		"session_refresh_digest": nil, "session_expires_at": nil,
	}
}

func (h *AccountHandler) loginCookie(c *gin.Context, req LoginRequest) {
	a, err := h.accountService.FindByUsername(c.Request.Context(), req.Username)
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && bcrypt.CompareHashAndPassword([]byte(a.Password), []byte(req.Password)) != nil) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "session storage unavailable"})
		return
	}
	access, refresh, err := newSessionTokens(*a)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	expires := time.Now().UTC().Add(sessionLifetime).Truncate(time.Second)
	result := h.accountService.accountRepository.db.WithContext(c.Request.Context()).
		Model(&Account{}).Where("id = ? AND password = ? AND username = ?", a.ID, a.Password, a.Username).
		Updates(secureTokens(access, refresh, expires))
	if result.Error != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "session storage unavailable"})
		return
	}
	if result.RowsAffected != 1 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "credentials changed"})
		return
	}
	setRefreshCookie(c, refresh, expires)
	c.JSON(http.StatusOK, LoginResponse{Token: access, AccountID: a.ID, Username: a.Username})
}

func (as *AccountService) rotateCookie(ctx context.Context, refresh string) (Account, string, string, error) {
	if refresh == "" {
		return Account{}, "", "", errInvalidSession
	}
	var a Account
	var access, next string
	err := as.accountRepository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("session_refresh_digest = ? AND session_expires_at > ?", auth.DigestToken(refresh), time.Now().UTC()).
			Take(&a).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errInvalidSession
		}
		if err != nil {
			return err
		}
		access, next, err = newSessionTokens(a)
		if err != nil {
			return err
		}
		return tx.Model(&Account{}).Where("id = ?", a.ID).Updates(secureTokens(access, next, *a.SessionExpiresAt)).Error
	})
	return a, access, next, err
}

func (h *AccountHandler) refreshCookie(c *gin.Context) {
	a, access, next, err := h.accountService.rotateCookie(c.Request.Context(), cookieToken(c))
	if errors.Is(err, errInvalidSession) {
		// A late response for an older cookie must not clear the newer cookie.
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid refresh session"})
		return
	}
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "session storage unavailable"})
		return
	}
	setRefreshCookie(c, next, *a.SessionExpiresAt)
	c.JSON(http.StatusOK, LoginResponse{Token: access, AccountID: a.ID, Username: a.Username})
}

func (h *AccountHandler) LogoutCookie(c *gin.Context) {
	if !acceptSessionOrigin(c) {
		return
	}
	if value := cookieToken(c); value != "" {
		if err := h.accountService.accountRepository.db.WithContext(c.Request.Context()).
			Model(&Account{}).Where("session_refresh_digest = ?", auth.DigestToken(value)).
			Updates(endSessionFields()).Error; err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "session storage unavailable"})
			return
		}
	}
	setRefreshCookie(c, "", time.Time{})
	c.JSON(http.StatusOK, gin.H{"message": "account logged out"})
}

func (as *AccountService) renameCookie(ctx context.Context, id uint, current, name string) (string, error) {
	if name == "" {
		return "", ErrNewUsernameRequired
	}
	token, err := auth.GenerateToken(id, name)
	if err != nil {
		return "", err
	}
	update := as.accountRepository.db.WithContext(ctx).Model(&Account{}).
		Where("id = ? AND token = ? AND session_expires_at > ?", id, auth.DigestToken(current), time.Now().UTC()).
		Updates(map[string]interface{}{"username": name, "token": auth.DigestToken(token)})
	var duplicate *mysql.MySQLError
	if errors.As(update.Error, &duplicate) && duplicate.Number == 1062 {
		return "", ErrUsernameTaken
	}
	if update.Error != nil {
		return "", update.Error
	}
	if update.RowsAffected != 1 {
		return "", errInvalidSession
	}
	return token, nil
}
