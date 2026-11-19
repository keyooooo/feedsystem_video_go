package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
)

const LiveSessionCheck = "feedsystem.liveSessionCheck"

func CookieOrigin() string {
	return os.Getenv("SESSION_COOKIE_ORIGIN")
}

func ValidateCookieOrigin() error {
	origin := CookieOrigin()
	if origin == "" {
		return nil
	}
	address, err := url.Parse(origin)
	if err != nil || address.Scheme != "https" || address.Hostname() == "" || address.User != nil ||
		address.Path != "" || address.RawQuery != "" || address.ForceQuery || address.Fragment != "" ||
		address.String() != origin {
		return errors.New("SESSION_COOKIE_ORIGIN must be an exact HTTPS origin")
	}
	return nil
}

func DigestToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
