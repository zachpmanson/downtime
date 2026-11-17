package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestGzipHeaderCapturePreservesKeywordSemantics(t *testing.T) {
	var encoded bytes.Buffer
	zw := gzip.NewWriter(&encoded)
	_, _ = zw.Write([]byte("expected keyword"))
	_ = zw.Close()
	for _, status := range []int{200, 404, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept-Encoding") != "gzip" {
					t.Error("compression negotiation changed")
				}
				w.Header().Set("Content-Encoding", "GZip")
				w.Header().Set("Content-Length", strconv.Itoa(encoded.Len()))
				w.WriteHeader(status)
				_, _ = w.Write(encoded.Bytes())
			}))
			defer srv.Close()
			for _, keyword := range []string{"keyword", "missing"} {
				r := check(context.Background(), MonitorConfig{Type: "http", URL: srv.URL, Keyword: keyword, Timeout: Duration(time.Second)})
				if r.Up != (status == 200 && keyword == "keyword") {
					t.Fatalf("health changed: %+v", r)
				}
				if r.HTTP.Headers.Get("Content-Encoding") != "GZip" || r.HTTP.Headers.Get("Content-Length") != strconv.Itoa(encoded.Len()) {
					t.Fatal("Go stripped response headers")
				}
			}
		})
	}
}

func TestFailedHTTPNoResponseFromCheck(t *testing.T) {
	for _, url := range []string{":invalid", "bad-scheme://endpoint"} {
		r := check(context.Background(), MonitorConfig{Type: "http", URL: url, Timeout: Duration(time.Second)})
		if r.Up || r.HTTP == nil || r.HTTP.Status != 0 || r.HTTP.Headers != nil {
			t.Fatalf("invented headers for %q: %+v", url, r)
		}
	}
}
