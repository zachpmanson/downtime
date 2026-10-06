package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPageIncludesInitialStatusAndAPIStillServesUpdates(t *testing.T) {
	store := NewStore([]MonitorConfig{{
		Name: "monitor <one>", Type: "http", URL: "https://example.test",
		Interval: Duration(time.Minute),
	}}, 100, 3, nil, nil, time.Now())
	handler := newServer(store)

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest("GET", "/", nil))
	if page.Code != 200 {
		t.Fatalf("GET / status = %d, want 200", page.Code)
	}
	body := page.Body.String()
	start := strings.Index(body, `<script id="initial-status" type="application/json">`)
	if start < 0 {
		t.Fatal("HTML response does not contain embedded initial status")
	}
	start += len(`<script id="initial-status" type="application/json">`)
	end := strings.Index(body[start:], `</script>`)
	if end < 0 {
		t.Fatal("embedded initial status script is not closed")
	}
	var initial statusResponse
	if err := json.Unmarshal([]byte(body[start:start+end]), &initial); err != nil {
		t.Fatalf("decode embedded status: %v", err)
	}
	if len(initial.Monitors) != 1 || initial.Monitors[0].Name != "monitor <one>" || initial.Monitors[0].Status != "pending" {
		t.Fatalf("embedded monitors = %+v, want pending monitor", initial.Monitors)
	}

	api := httptest.NewRecorder()
	handler.ServeHTTP(api, httptest.NewRequest("GET", "/api/status", nil))
	if api.Code != 200 {
		t.Fatalf("GET /api/status status = %d, want 200", api.Code)
	}
	var updated statusResponse
	if err := json.Unmarshal(api.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode API status: %v", err)
	}
	if len(updated.Monitors) != 1 || updated.Monitors[0].Status != "pending" {
		t.Fatalf("API monitors = %+v, want pending monitor", updated.Monitors)
	}
}
