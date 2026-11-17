package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func diagnosticDB(t *testing.T) *DB {
	t.Helper()
	db, err := OpenDB(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func diagnosticCount(t *testing.T, db *DB) int {
	t.Helper()
	var n int
	if err := db.db.QueryRow(`SELECT count(*) FROM http_failures`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestHTTPFailureHeadersPrivateAndIndependent(t *testing.T) {
	var journal bytes.Buffer
	old := log.Writer()
	log.SetOutput(&journal)
	t.Cleanup(func() { log.SetOutput(old) })
	secret := "secret-cookie-value"
	want := http.Header{"Set-Cookie": {secret, "second=value"}, "X-Multi": {"one", "two"}, "Retry-After": {"900"}}
	for _, status := range []int{404, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, values := range want {
					for _, v := range values {
						w.Header().Add(k, v)
					}
				}
				w.WriteHeader(status)
			}))
			defer srv.Close()
			for _, notifyEnabled := range []bool{false, true} {
				db := diagnosticDB(t)
				m := MonitorConfig{Name: "feed", Type: "http", URL: srv.URL, Notify: &notifyEnabled, Timeout: Duration(time.Second)}
				store := NewStore([]MonitorConfig{m}, 10, 3, nil, db, time.Now())
				// A success establishes a non-startup baseline. Three failures cross
				// the alert threshold; the fourth is repeated-down, not a transition.
				store.Record(m.Name, Result{Time: time.Now(), Up: true})
				for i := 0; i < 4; i++ {
					r := check(context.Background(), m)
					if r.Up || r.HTTP.Status != status {
						t.Fatalf("result: %+v", r)
					}
					transition := store.Record(m.Name, r)
					if (transition != nil) != (notifyEnabled && i == 2) {
						t.Fatalf("alert changed at %d: %+v", i, transition)
					}
					var monitor, headerJSON, errText string
					var ts int64
					var code int
					if err := db.db.QueryRow(`SELECT monitor, ts, status, err, headers FROM http_failures ORDER BY id DESC LIMIT 1`).Scan(&monitor, &ts, &code, &errText, &headerJSON); err != nil {
						t.Fatal(err)
					}
					var got http.Header
					if err := json.Unmarshal([]byte(headerJSON), &got); err != nil {
						t.Fatal(err)
					}
					for k, values := range want {
						if !reflect.DeepEqual(got[k], values) {
							t.Fatalf("%s: %q != %q", k, got[k], values)
						}
					}
					if monitor != m.Name || ts != r.Time.UnixNano() || code != status || errText != r.Err {
						t.Fatal("lost check identity")
					}
				}
				if n := diagnosticCount(t, db); n != 4 {
					t.Fatalf("diagnostics = %d", n)
				}
				// Baseline failure is recorded even though startup suppresses alerts.
				store2 := NewStore([]MonitorConfig{m}, 10, 1, nil, db, time.Now())
				store2.Record(m.Name, check(context.Background(), m))
				if n := diagnosticCount(t, db); n != 5 {
					t.Fatalf("baseline diagnostics = %d", n)
				}
				for _, path := range []string{"/", "/api/status"} {
					w := httptest.NewRecorder()
					newServer(store).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
					if strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), "Set-Cookie") {
						t.Fatalf("public header leak at %s", path)
					}
				}
				for _, r := range store.monitors[m.Name].history {
					if r.HTTP != nil {
						t.Fatal("headers retained in ring buffer")
					}
				}
				recent, err := db.Recent(m.Name, 10)
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range recent {
					if r.HTTP != nil {
						t.Fatal("headers seeded into public history")
					}
				}
			}
		})
	}
	if strings.Contains(journal.String(), secret) {
		t.Fatal("journal header leak")
	}
}

type brokenBody struct {
	closed bool
	body   string
}

func (b *brokenBody) Read(p []byte) (int, error) { return copy(p, b.body), io.ErrUnexpectedEOF }
func (b *brokenBody) Close() error               { b.closed = true; return nil }

func TestHTTPResponseErrorMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, body, keyword string
		requestErr          error
		wantUp              bool
	}{
		{"request error with response", "", "", errors.New("response handling failed"), false},
		{"body read error missing keyword", "partial", "missing", nil, false},
		{"body read error matching keyword preserves health", "partial", "partial", nil, true},
		{"keyword mismatch", "body", "missing", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &brokenBody{body: tc.body}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"X-Multi": {"one", "two"}}, Body: body}
			metadata, err := inspectHTTPResponse(resp, tc.requestErr, MonitorConfig{Keyword: tc.keyword})
			if (err == nil) != tc.wantUp || !body.closed || metadata.Status != 200 || !reflect.DeepEqual(metadata.Headers, resp.Header) {
				t.Fatalf("metadata=%+v err=%v closed=%v", metadata, err, body.closed)
			}
			resp.Header.Set("X-Multi", "mutated")
			if metadata.Headers.Get("X-Multi") != "one" {
				t.Fatal("headers were not cloned")
			}
			db := diagnosticDB(t)
			r := Result{Time: time.Now(), Up: err == nil, HTTP: metadata}
			db.recordHTTPFailure("feed", r)
			want := 1
			if tc.wantUp {
				want = 0
			}
			if diagnosticCount(t, db) != want {
				t.Fatal("wrong capture selection")
			}
		})
	}
	metadata, err := inspectHTTPResponse(nil, errors.New("dial failed"), MonitorConfig{})
	if err == nil || metadata.Status != 0 || metadata.Headers != nil {
		t.Fatalf("invented response: %+v", metadata)
	}
	db := diagnosticDB(t)
	db.recordHTTPFailure("no-response", Result{Time: time.Now(), HTTP: metadata})
	db.recordHTTPFailure("tcp", Result{Time: time.Now(), Err: "tcp failed"})
	var headers string
	if err := db.db.QueryRow(`SELECT headers FROM http_failures`).Scan(&headers); err != nil {
		t.Fatal(err)
	}
	if diagnosticCount(t, db) != 1 || headers != "null" {
		t.Fatal("no-response or TCP mishandled")
	}
}

func TestHTTPFailureRetention(t *testing.T) {
	db := diagnosticDB(t)
	now := time.Now()
	// Prune expired diagnostics but not ordinary all-time uptime checks.
	db.Append("feed", Result{Time: now.Add(-30 * 24 * time.Hour), Up: true})
	insert := func(ts time.Time, headers string) {
		t.Helper()
		if _, err := db.db.Exec(`INSERT INTO http_failures(monitor,ts,status,err,headers) VALUES('feed',?,500,'',?)`, ts.UnixNano(), headers); err != nil {
			t.Fatal(err)
		}
	}
	insert(now.Add(-httpFailureRetention-time.Second), `{}`)
	insert(now, `{}`)
	if err := db.pruneHTTPFailures(now); err != nil {
		t.Fatal(err)
	}
	if diagnosticCount(t, db) != 1 {
		t.Fatal("age retention failed")
	}
	stats, err := db.AllTime("feed")
	if err != nil || stats.Total != 1 {
		t.Fatal("uptime history affected")
	}
	// Row cap, including equal timestamps, keeps newest inserts.
	tx, err := db.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < httpFailureMaxRows+1; i++ {
		if _, err := tx.Exec(`INSERT INTO http_failures(monitor,ts,status,err,headers) VALUES('feed',?,500,'','{}')`, now.UnixNano()); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.pruneHTTPFailures(now); err != nil {
		t.Fatal(err)
	}
	if diagnosticCount(t, db) != httpFailureMaxRows {
		t.Fatal("count retention failed")
	}
	// Byte cap evicts whole old records, never truncates a value.
	if _, err := db.db.Exec(`DELETE FROM http_failures`); err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("x", httpFailureMaxBytes/2)
	insert(now, large)
	insert(now, large)
	if err := db.pruneHTTPFailures(now); err != nil {
		t.Fatal(err)
	}
	if diagnosticCount(t, db) != 1 {
		t.Fatal("byte retention failed")
	}
	var got string
	if err := db.db.QueryRow(`SELECT headers FROM http_failures`).Scan(&got); err != nil || got != large {
		t.Fatal("value truncated")
	}
}

func TestHTTPFailureRetentionOnRecoveryAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	insertOld := func() {
		t.Helper()
		if _, err := db.db.Exec(`INSERT INTO http_failures(monitor,ts,status,err,headers) VALUES('feed',?,500,'','{}')`, time.Now().Add(-8*24*time.Hour).UnixNano()); err != nil {
			t.Fatal(err)
		}
	}
	insertOld()
	db.lastDiagnosticPrune = time.Time{}
	db.recordHTTPFailure("feed", Result{Time: time.Now(), Up: true})
	if diagnosticCount(t, db) != 0 {
		t.Fatal("recovery did not prune")
	}
	insertOld()
	_ = db.Close()
	db, err = OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if diagnosticCount(t, db) != 0 {
		t.Fatal("startup did not prune")
	}
}

func TestHTTPFailurePermissionsConcurrencyAndWriteFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if os.IsNotExist(err) && suffix != "" {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %o", suffix, info.Mode().Perm())
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db.recordHTTPFailure("feed", Result{Time: time.Now(), HTTP: &HTTPResponse{Status: 500, Headers: http.Header{"Set-Cookie": {"secret"}}}})
		}()
	}
	wg.Wait()
	if diagnosticCount(t, db) != 12 {
		t.Fatal("lost concurrent failures")
	}
	link := filepath.Join(t.TempDir(), "link.db")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if unsafe, err := OpenDB(link); err == nil {
		_ = unsafe.Close()
		t.Fatal("followed database symlink")
	}
	_ = db.Close()
	store := NewStore([]MonitorConfig{{Name: "feed"}}, 10, 1, nil, db, time.Now())
	// Runtime I/O errors must not prevent status updates or leak headers.
	store.Record("feed", Result{Time: time.Now(), HTTP: &HTTPResponse{Status: 500, Headers: http.Header{"Set-Cookie": {"secret"}}}})
	if store.monitors["feed"].status != "down" {
		t.Fatal("diagnostic failure changed monitoring")
	}
}
