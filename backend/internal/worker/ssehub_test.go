package worker

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestSSEStreamFlushesBeforeAnEvent(t *testing.T) {
	hub := NewSSEHub(nil)
	router := gin.New()
	router.GET("/notification/stream", func(c *gin.Context) {
		c.Set("accountID", uint(42))
		hub.SSEHandler(c)
	})
	server := httptest.NewServer(router)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/notification/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("unexpected SSE response: %s, %v", response.Status, response.Header)
	}

	hub.Push(43, &Notification{ID: 1, RecipientID: 43})
	hub.Push(42, &Notification{ID: 2, RecipientID: 42})
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "data: ") || !strings.Contains(line, `"id":2`) {
		t.Fatalf("unexpected event %q: %v", line, err)
	}
}
