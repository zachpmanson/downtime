package main

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Result is the outcome of a single check.
type Result struct {
	Time    time.Time
	Up      bool
	Latency time.Duration
	Err     string
	// HTTP is transient diagnostic data, never part of public history or alerts.
	HTTP *HTTPResponse `json:"-"`
}

// HTTPResponse contains the final parsed response, if any. Status zero and nil
// Headers mean no response was received, not an empty successful response.
type HTTPResponse struct {
	Status  int
	Headers http.Header
}

// Client.Do can discard a parsed response (for example an invalid redirect
// Location). Capture at the transport boundary before Client handles it.
type capturingTransport struct {
	*http.Transport
	response *HTTPResponse
}

func (t *capturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.Transport.RoundTrip(req)
	if resp != nil {
		t.response = snapshotHTTPResponse(resp)
	}
	return resp, err
}

// check runs one probe for the monitor and returns its Result. It never blocks
// longer than the monitor's timeout.
func check(ctx context.Context, m MonitorConfig) Result {
	ctx, cancel := context.WithTimeout(ctx, m.Timeout.D())
	defer cancel()

	start := time.Now()
	var err error
	var response *HTTPResponse
	switch m.Type {
	case "http":
		response, err = checkHTTP(ctx, m)
	case "tcp":
		err = checkTCP(ctx, m)
	default:
		err = fmt.Errorf("unknown type %q", m.Type)
	}

	r := Result{Time: start, Latency: time.Since(start), Up: err == nil, HTTP: response}
	if err != nil {
		r.Err = err.Error()
	}
	return r
}

func checkHTTP(ctx context.Context, m MonitorConfig) (*HTTPResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return &HTTPResponse{}, safeHTTPError(err, false)
	}
	if m.BasicAuth != nil && m.BasicAuth.User != "" {
		req.SetBasicAuth(m.BasicAuth.User, m.BasicAuth.Password)
	}
	req.Header.Set("User-Agent", "downtime/1.0")
	// Negotiate gzip as before, but decode it ourselves after capturing headers.
	req.Header.Set("Accept-Encoding", "gzip")

	transport := &capturingTransport{Transport: &http.Transport{
		// Automatic decompression removes Content-Encoding/Length from
		// responses. Preserve them and decode only for keyword matching.
		DisableCompression: true,
		TLSClientConfig:    &tls.Config{MinVersion: tls.VersionTLS12},
	}}
	client := &http.Client{
		// Timeout is enforced via the context; don't follow redirect loops forever.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: transport,
	}

	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if resp == nil && err != nil && transport.response != nil {
		// Client error strings may quote sensitive response header values.
		return transport.response, safeHTTPError(err, true)
	}
	return inspectHTTPResponse(resp, err, m)
}

// Capture headers before inspecting errors or reading the body: those failures
// must not discard an otherwise useful response. Also tolerate response+error.
func inspectHTTPResponse(resp *http.Response, requestErr error, m MonitorConfig) (*HTTPResponse, error) {
	response := &HTTPResponse{}
	if resp != nil {
		response = snapshotHTTPResponse(resp)
		if resp.Body != nil {
			defer resp.Body.Close()
		}
	}
	if requestErr != nil {
		return response, safeHTTPError(requestErr, resp != nil)
	}
	if resp == nil {
		return response, fmt.Errorf("HTTP check returned no response")
	}
	if !statusOK(resp.StatusCode, m.ExpectStatus) {
		return response, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	if m.Keyword != "" {
		var reader io.Reader = resp.Body
		if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
			decoded, err := gzip.NewReader(resp.Body)
			if err != nil {
				return response, fmt.Errorf("keyword %q not found in body", m.Keyword)
			}
			defer decoded.Close()
			reader = decoded
		}
		// Cap the decoded body read so a huge response can't blow up memory.
		// Preserve existing keyword-check semantics for partial/read-error bodies.
		body, _ := io.ReadAll(io.LimitReader(reader, 1<<20))
		if !strings.Contains(string(body), m.Keyword) {
			return response, fmt.Errorf("keyword %q not found in body", m.Keyword)
		}
	}
	return response, nil
}

// net/http moves these fields out of Header. Preserve its parsed information,
// while documenting that original wire grouping/casing is not available.
func snapshotHTTPResponse(resp *http.Response) *HTTPResponse {
	headers := resp.Header.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	if len(resp.TransferEncoding) > 0 {
		headers["Transfer-Encoding"] = append([]string(nil), resp.TransferEncoding...)
	}
	if len(resp.Trailer) > 0 && headers.Get("Trailer") == "" {
		keys := make([]string, 0, len(resp.Trailer))
		for key := range resp.Trailer {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		headers["Trailer"] = []string{strings.Join(keys, ", ")}
	}
	return &HTTPResponse{Status: resp.StatusCode, Headers: headers}
}

// Even errors without a returned response can quote malformed header values.
// Public history and transition logs get fixed categories, never raw HTTP errors.
func safeHTTPError(err error, receivedResponse bool) error {
	if receivedResponse {
		return fmt.Errorf("HTTP response handling failed")
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("HTTP request canceled")
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return fmt.Errorf("HTTP request timed out")
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Errorf("HTTP DNS lookup failed")
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return fmt.Errorf("HTTP network connection failed")
	}
	return fmt.Errorf("HTTP request failed")
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
