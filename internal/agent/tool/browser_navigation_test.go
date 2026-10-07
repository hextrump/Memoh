package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestBrowserNavigationOutcome(t *testing.T) {
	for _, tc := range []struct {
		name       string
		navigation string
		ready      string
		urlResult  string
		wantError  string
	}{
		{"success", `{"frameId":"f"}`, "complete", `{"result":{"type":"string","value":"https://example.com/redirected"}}`, ""},
		{"dns failure", `{"errorText":"net::ERR_NAME_NOT_RESOLVED"}`, "complete", "", "net::ERR_NAME_NOT_RESOLVED"},
		{"connection refused", `{"errorText":"net::ERR_CONNECTION_REFUSED"}`, "complete", "", "net::ERR_CONNECTION_REFUSED"},
		{"download", `{"isDownload":true}`, "complete", "", "download instead"},
		{"load timeout", `{"frameId":"f"}`, "loading", "", "page load timed out"},
		{"URL verification failed", `{"frameId":"f"}`, "complete", `{"exceptionDetails":{"text":"Execution context destroyed"}}`, "verify browser navigation URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upgrader := websocket.Upgrader{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				for {
					var req struct {
						ID     int    `json:"id"`
						Method string `json:"method"`
						Params struct {
							Expression string `json:"expression"`
						} `json:"params"`
					}
					if err := conn.ReadJSON(&req); err != nil {
						return
					}
					result := tc.navigation
					if req.Method == "Runtime.evaluate" {
						if strings.Contains(req.Params.Expression, "document.readyState") {
							result = `{"result":{"type":"string","value":"` + tc.ready + `"}}`
						} else {
							result = tc.urlResult
						}
					}
					if err := conn.WriteJSON(map[string]any{"id": req.ID, "result": json.RawMessage(result)}); err != nil {
						return
					}
				}
			}))
			defer server.Close()
			conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if response != nil && response.Body != nil {
				defer func() { _ = response.Body.Close() }()
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			page := cdpPage{conn: &cdpConn{conn: conn}}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			result, err := page.navigate(ctx, "https://example.com", 0)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("got result=%v error=%v; want %q", result, err, tc.wantError)
				}
				if result != nil {
					t.Fatalf("failure returned success payload: %v", result)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result["url"] != "https://example.com/redirected" {
				t.Fatalf("wrong final URL: %v", result)
			}
		})
	}
}

func TestLooksBlocked(t *testing.T) {
	for _, tc := range []struct {
		status      int
		title, text string
		want        bool
	}{
		{200, "Example Domain", "This domain is for use in documentation examples.", false},
		{403, "", "", true},
		{429, "Too Many Requests", "", true},
		{200, "Just a moment...", "Checking your browser", true},
		{200, "Shop", "Access denied. Your browser has been identified as an automated bot.", true},
		{200, "News", "Court ruling: the merger was blocked by regulators. " + strings.Repeat("Long article text. ", 60), false},
		{404, "Not Found", "The page does not exist.", false},
	} {
		if got := looksBlocked(tc.status, tc.title, tc.text); got != tc.want {
			t.Errorf("looksBlocked(%d, %q, %.30q) = %v, want %v", tc.status, tc.title, tc.text, got, tc.want)
		}
	}
}
