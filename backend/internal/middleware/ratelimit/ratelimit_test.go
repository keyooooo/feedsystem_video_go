package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestMissingCacheBlocksProductionWrites(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler gin.HandlerFunc
		want    int
	}{
		{"production", LimitStrict(nil, "login", 10, time.Minute, KeyByIP), http.StatusServiceUnavailable},
		{"development", Limit(nil, "login", 10, time.Minute, KeyByIP), http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.POST("/login", tc.handler, func(c *gin.Context) { c.Status(http.StatusNoContent) })
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/login", nil))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
