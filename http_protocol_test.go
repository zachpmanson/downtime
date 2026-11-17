package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPProtocolErrorsDoNotLeakHeaders(t *testing.T) {
	secret := "secret-header-value"
	for _, tc := range []struct {
		name, response string
		status         int
	}{
		{"invalid redirect", "HTTP/1.1 302 Found\r\nLocation: http://[" + secret + "\r\nSet-Cookie: " + secret + "\r\nContent-Length: 0\r\n\r\n", 302},
		{"invalid content length", "HTTP/1.1 200 OK\r\nContent-Length: " + secret + "\r\n\r\n", 0},
		{"malformed header", "HTTP/1.1 200 OK\r\n" + secret + "\r\n\r\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				_, _ = fmt.Fprint(conn, tc.response)
			}))
			defer srv.Close()
			r := check(context.Background(), MonitorConfig{Type: "http", URL: srv.URL, Timeout: Duration(time.Second)})
			if r.Up || strings.Contains(r.Err, secret) || r.HTTP.Status != tc.status {
				t.Fatalf("error/header boundary: %+v", r)
			}
			if tc.status != 0 && r.HTTP.Headers.Get("Set-Cookie") != secret {
				t.Fatal("lost discarded redirect response")
			}
		})
	}
}

func TestHTTPChunkedHeadersPreserved(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Trailer", "X-End")
		w.WriteHeader(500)
		_, _ = w.Write([]byte("failure"))
		w.Header().Set("X-End", "trailer-value")
	}))
	defer srv.Close()
	r := check(context.Background(), MonitorConfig{Type: "http", URL: srv.URL, Timeout: Duration(time.Second)})
	if r.Up || r.HTTP.Headers.Get("Transfer-Encoding") != "chunked" || r.HTTP.Headers.Get("Trailer") != "X-End" {
		t.Fatalf("missing parsed transport fields: %+v", r.HTTP)
	}
}

func TestHTTPNonUTF8ValuesRoundTrip(t *testing.T) {
	value := "raw-" + string([]byte{0xff, 0x80})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("X-Bytes", value)
		w.Header().Add("X-Bytes", "ordinary")
		w.WriteHeader(500)
	}))
	defer srv.Close()
	r := check(context.Background(), MonitorConfig{Type: "http", URL: srv.URL, Timeout: Duration(time.Second)})
	if r.HTTP.Headers.Get("X-Bytes") != value {
		t.Fatal("transport changed obs-text")
	}
	db := diagnosticDB(t)
	db.recordHTTPFailure("feed", r)
	var data string
	if err := db.db.QueryRow(`SELECT headers FROM http_failures`).Scan(&data); err != nil {
		t.Fatal(err)
	}
	var headers map[string][]json.RawMessage
	if err := json.Unmarshal([]byte(data), &headers); err != nil {
		t.Fatal(err)
	}
	var encoded struct {
		Base64 string `json:"base64"`
	}
	if err := json.Unmarshal(headers["X-Bytes"][0], &encoded); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded.Base64)
	if err != nil || string(decoded) != value {
		t.Fatal("JSON changed header bytes")
	}
	if string(headers["X-Bytes"][1]) != `"ordinary"` {
		t.Fatal("ordinary value changed")
	}
}
