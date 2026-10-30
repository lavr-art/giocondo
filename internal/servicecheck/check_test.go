package servicecheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWaitPanelRejectsWrongResponse(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(map[bool]string{false: "wrong server", true: "ready"}[valid], func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if valid {
					_, _ = w.Write([]byte(r.URL.Query().Get("challenge")))
				} else {
					_, _ = w.Write([]byte("wrong"))
				}
			}))
			defer s.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			err := WaitPanel(ctx, strings.TrimPrefix(s.URL, "http://"))
			if (err == nil) != valid {
				t.Fatalf("valid=%v: %v", valid, err)
			}
		})
	}
}
