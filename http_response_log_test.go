package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestScheduledHTTPResponseLog(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Retry-After", "900")
				w.Header().Add("X-Multi", "one")
				w.Header().Add("X-Multi", "two")
				w.Header().Add("Set-Cookie", "secret-cookie-one")
				w.Header().Add("Set-Cookie", "secret-cookie-two")
				w.Header().Set("X-Api-Key", "secret-api-key")
				w.Header().Set("WWW-Authenticate", "secret-auth-challenge")
				w.Header().Set("Location", "https://example.test/?key=secret-location")
				w.Header().Set("Link", "<https://example.test/?key=secret-link>; rel=next")
				w.Header().Set("Reporting-Endpoints", `default="https://example.test/?key=secret-report"`)
				w.WriteHeader(status)
			}))
			defer srv.Close()

			var m MonitorConfig
			if err := json.Unmarshal([]byte(`{"name":"feed","type":"http","interval":"1h","timeout":"5s","notify":false,"log_response_headers":true}`), &m); err != nil {
				t.Fatal(err)
			}
			m.URL = srv.URL
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out := &cancelOnHTTPLog{cancel: cancel}
			previous := log.Writer()
			log.SetOutput(out)
			t.Cleanup(func() { log.SetOutput(previous) })

			store := NewStore([]MonitorConfig{m}, 10, 1, nil, nil, time.Now())
			runOne(ctx, m, store, nil, NewStateFile(""))

			if got := requests.Load(); got != 1 {
				t.Fatalf("requests = %d, want 1", got)
			}
			prefix := fmt.Sprintf(`[HTTP RESPONSE] monitor="feed" status=%d headers=`, status)
			var headers http.Header
			count := 0
			for _, line := range strings.Split(out.String(), "\n") {
				if _, encoded, ok := strings.Cut(line, prefix); ok {
					count++
					if err := json.Unmarshal([]byte(encoded), &headers); err != nil {
						t.Fatal(err)
					}
				}
			}
			if count != 1 || strings.Contains(out.String(), "secret-") {
				t.Fatalf("expected one redacted entry, got %q", out.String())
			}
			for name, want := range map[string][]string{
				"Retry-After": {"900"}, "X-Multi": {"one", "two"},
				"Set-Cookie": {"[redacted]", "[redacted]"}, "X-Api-Key": {"[redacted]"},
				"WWW-Authenticate": {"[redacted]"}, "Location": {"[redacted]"},
				"Link": {"[redacted]"}, "Reporting-Endpoints": {"[redacted]"},
			} {
				if got := headers.Values(name); !reflect.DeepEqual(got, want) {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
		})
	}
}

type cancelOnHTTPLog struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelOnHTTPLog) Write(b []byte) (int, error) {
	if bytes.Contains(b, []byte("[HTTP RESPONSE]")) {
		w.cancel()
	}
	return w.Buffer.Write(b)
}

func TestLogHTTPResponseWithoutResponse(t *testing.T) {
	var out bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(previous) })

	logHTTPResponse("tcp", time.Time{}, nil)
	if out.Len() != 0 {
		t.Fatal("non-HTTP probe produced a response log")
	}
	logHTTPResponse("feed", time.Time{}, &HTTPResponse{})
	if !strings.Contains(out.String(), `[HTTP RESPONSE] monitor="feed" status=0 headers=null`) {
		t.Fatalf("missing no-response marker: %q", out.String())
	}
}

func TestLargeHTTPResponseLogCanBeReassembled(t *testing.T) {
	var out bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(previous) })

	value := strings.Repeat("世", responseLogChunkSize)
	metadata := &HTTPResponse{Status: 200, Headers: http.Header{"X-Large": {value}, "Set-Cookie": {"private"}}}
	logHTTPResponse("feed", time.Unix(0, 42), metadata)
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) <= 1 {
		t.Fatal("expected multiple chunks")
	}
	var joined strings.Builder
	for i, line := range lines {
		key := fmt.Sprintf(`[HTTP RESPONSE CHUNK] monitor="feed" status=200 check=42 part=%d/%d headers_base64=`, i+1, len(lines))
		_, chunk, ok := strings.Cut(line, key)
		if !ok || len(chunk) > responseLogChunkSize {
			t.Fatalf("invalid chunk %d", i+1)
		}
		joined.WriteString(chunk)
	}
	decoded, err := base64.StdEncoding.DecodeString(joined.String())
	if err != nil {
		t.Fatal(err)
	}
	var headers http.Header
	if err := json.Unmarshal(decoded, &headers); err != nil {
		t.Fatal(err)
	}
	if headers.Get("X-Large") != value || headers.Get("Set-Cookie") != "[redacted]" || metadata.Headers.Get("Set-Cookie") != "private" {
		t.Fatal("chunked log lost data or did not redact safely")
	}
}
