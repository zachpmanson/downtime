package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Result is the outcome of a single check.
type Result struct {
	Time    time.Time
	Up      bool
	Latency time.Duration
	Err     string
}

// check runs one probe for the monitor and returns its Result. It never blocks
// longer than the monitor's timeout.
func check(ctx context.Context, m MonitorConfig) Result {
	ctx, cancel := context.WithTimeout(ctx, m.Timeout.D())
	defer cancel()

	start := time.Now()
	var err error
	switch m.Type {
	case "http":
		err = checkHTTP(ctx, m)
	case "tcp":
		err = checkTCP(ctx, m)
	default:
		err = fmt.Errorf("unknown type %q", m.Type)
	}

	r := Result{Time: start, Latency: time.Since(start), Up: err == nil}
	if err != nil {
		r.Err = err.Error()
	}
	return r
}

func checkHTTP(ctx context.Context, m MonitorConfig) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "downtime/1.0")

	client := &http.Client{
		// Timeout is enforced via the context; don't follow redirect loops forever.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if !statusOK(resp.StatusCode, m.ExpectStatus) {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	if m.Keyword != "" {
		// Cap the body read so a huge response can't blow up memory.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if !strings.Contains(string(body), m.Keyword) {
			return fmt.Errorf("keyword %q not found in body", m.Keyword)
		}
	}
	return nil
}

// statusOK reports whether code is acceptable. With no expected list, any
// 2xx/3xx is considered up.
func statusOK(code int, expect []int) bool {
	if len(expect) == 0 {
		return code >= 200 && code < 400
	}
	for _, e := range expect {
		if code == e {
			return true
		}
	}
	return false
}

func checkTCP(ctx context.Context, m MonitorConfig) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", m.Target)
	if err != nil {
		return err
	}
	return conn.Close()
}
