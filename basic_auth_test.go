package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// authServer serves, returning 401 unless the request carries the Basic creds.
func requireBasicAuth(user, pass string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != user || p != pass {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func TestCheckHTTPWithBasicAuth(t *testing.T) {
	srv := requireBasicAuth("zach", "sekrit")
	t.Cleanup(srv.Close)

	cases := []struct {
		name string
		auth *BasicAuthConfig
		up   bool
	}{
		{"correct creds are accepted", &BasicAuthConfig{User: "zach", Password: "sekrit"}, true},
		{"wrong password rejected", &BasicAuthConfig{User: "zach", Password: "nope"}, false},
		{"wrong user rejected", &BasicAuthConfig{User: "alice", Password: "sekrit"}, false},
		{"no creds gets 401", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := MonitorConfig{
				Name:      "protected",
				Type:      "http",
				URL:       srv.URL,
				Interval:  Duration(30 * time.Second),
				Timeout:   Duration(5 * time.Second),
				BasicAuth: tc.auth,
			}
			r := check(context.Background(), m)
			if r.Up != tc.up {
				t.Fatalf("Up = %v, want %v (err=%q)", r.Up, tc.up, r.Err)
			}
		})
	}
}

func TestBasicAuthEnvResolution(t *testing.T) {
	os.Setenv("AUTH_PASSWORD", "envresolved")
	t.Cleanup(func() { os.Unsetenv("AUTH_PASSWORD") })

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	body := `{
		"listen": ":0",
		"monitors": [
			{"name":"m","type":"http","url":"http://example.com","interval":"30s",
			 "basic_auth":{"user":"zach","password":"env:AUTH_PASSWORD"}}
		]
	}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Monitors[0].BasicAuth.Password; got != "envresolved" {
		t.Fatalf("password = %q, want env-resolved %q", got, "envresolved")
	}
}
