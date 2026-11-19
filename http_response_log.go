package main

import (
	"encoding/base64"
	"log"
	"strings"
	"time"
)

// Keep each diagnostic well under journald's usual stream line limit.
const responseLogChunkSize = 20 << 10

func logHTTPResponse(monitor string, started time.Time, response *HTTPResponse) {
	if response == nil {
		return // not an HTTP check
	}
	headers := response.Headers.Clone()
	for name, values := range headers {
		if !sensitiveResponseHeader(name) {
			continue
		}
		for i := range values {
			values[i] = "[redacted]"
		}
	}
	encoded, err := encodeHTTPHeaders(headers)
	if err != nil {
		log.Printf("[HTTP RESPONSE] monitor=%q status=%d header encoding failed", monitor, response.Status)
		return
	}
	if len(encoded) <= responseLogChunkSize {
		log.Printf("[HTTP RESPONSE] monitor=%q status=%d headers=%s", monitor, response.Status, encoded)
		return
	}
	// Use ASCII so line boundaries never split a multi-byte header value.
	payload := base64.StdEncoding.EncodeToString(encoded)
	total := (len(payload) + responseLogChunkSize - 1) / responseLogChunkSize
	for i := 0; i < total; i++ {
		start := i * responseLogChunkSize
		end := min(start+responseLogChunkSize, len(payload))
		log.Printf("[HTTP RESPONSE CHUNK] monitor=%q status=%d check=%d part=%d/%d headers_base64=%s",
			monitor, response.Status, started.UnixNano(), i+1, total, payload[start:end])
	}
}

// Preserve sensitive field names and value counts, not the values. URL-bearing
// headers can also carry credentials. Opt in only for trusted URLs:
// arbitrary custom header names are not guaranteed to signal sensitive data.
func sensitiveResponseHeader(name string) bool {
	name = strings.ToLower(name)
	switch name {
	case "location", "content-location", "refresh", "link", "report-to", "reporting-endpoints", "referer", "referrer":
		return true
	}
	for _, part := range []string{"cookie", "auth", "credential", "token", "secret", "api-key", "apikey", "password", "session"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	return false
}
