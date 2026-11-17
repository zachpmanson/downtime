package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	httpFailureRetention = 7 * 24 * time.Hour
	httpFailureMaxRows   = 10000
	httpFailureMaxBytes  = 64 << 20
)

// privateDBFiles protects the main DB and pre-existing SQLite sidecars. The
// containing directory must also be trusted/private (the NixOS module sets it).
func privateDBFiles(path string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		flags := os.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
		if suffix == "" {
			flags |= os.O_CREATE
		}
		f, err := os.OpenFile(path+suffix, flags, 0o600)
		if os.IsNotExist(err) && suffix != "" {
			continue
		}
		if err != nil {
			return fmt.Errorf("protecting database file: %w", err)
		}
		info, err := f.Stat()
		if err == nil && !info.Mode().IsRegular() {
			err = fmt.Errorf("database path is not a regular file")
		}
		if err == nil {
			err = f.Chmod(0o600)
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func (d *DB) initHTTPFailures() error {
	_, err := d.db.Exec(`CREATE TABLE IF NOT EXISTS http_failures (
		id INTEGER PRIMARY KEY,
		monitor TEXT NOT NULL,
		ts INTEGER NOT NULL,
		status INTEGER NOT NULL,
		err TEXT NOT NULL,
		headers TEXT NOT NULL
	)`)
	if err != nil {
		return err
	}
	return d.pruneHTTPFailures(time.Now())
}

// pruneHTTPFailures bounds logical retention, not forensic erasure of SQLite
// free pages, WAL or external backups. Never prune ordinary uptime history.
func (d *DB) pruneHTTPFailures(now time.Time) error {
	_, err := d.db.Exec(`DELETE FROM http_failures WHERE id IN (
		SELECT id FROM (
			SELECT id, ts,
				ROW_NUMBER() OVER (ORDER BY id DESC) AS n,
				SUM(length(CAST(headers AS BLOB)) + length(CAST(err AS BLOB)) +
					length(CAST(monitor AS BLOB))) OVER (ORDER BY id DESC) AS bytes
			FROM http_failures
		) WHERE ts < ? OR n > ? OR bytes > ?
	)`, now.Add(-httpFailureRetention).UnixNano(), httpFailureMaxRows, httpFailureMaxBytes)
	if err == nil {
		d.lastDiagnosticPrune = now
	}
	return err
}

// encodeHTTPHeaders keeps ordinary values readable, and losslessly encodes HTTP
// obs-text bytes that JSON strings would otherwise replace with U+FFFD.
func encodeHTTPHeaders(headers http.Header) ([]byte, error) {
	if headers == nil {
		return []byte("null"), nil
	}
	out := make(map[string][]any, len(headers))
	for name, values := range headers {
		out[name] = make([]any, 0, len(values))
		for _, value := range values {
			if utf8.ValidString(value) {
				out[name] = append(out[name], value)
			} else {
				out[name] = append(out[name], struct {
					Base64 string `json:"base64"`
				}{
					Base64: base64.StdEncoding.EncodeToString([]byte(value)),
				})
			}
		}
	}
	return json.Marshal(out)
}

// recordHTTPFailure is deliberately separate from transitions/notifications.
// Values go only to the private DB, never the journal, public API or notifier.
// Call on every live check so old sensitive data expires even after recovery.
func (d *DB) recordHTTPFailure(monitor string, r Result) {
	if d == nil || d.db == nil {
		if !r.Up && r.HTTP != nil {
			log.Print("HTTP failure diagnostics unavailable: database persistence disabled")
		}
		return
	}
	d.diagnosticMu.Lock()
	defer d.diagnosticMu.Unlock()
	now := time.Now()
	failedHTTP := !r.Up && r.HTTP != nil
	if failedHTTP {
		headers, err := encodeHTTPHeaders(r.HTTP.Headers)
		if err == nil {
			_, err = d.db.Exec(`INSERT INTO http_failures(monitor, ts, status, err, headers)
				VALUES (?, ?, ?, ?, ?)`, monitor, r.Time.UnixNano(), r.HTTP.Status, r.Err, string(headers))
		}
		if err != nil {
			// Driver errors can contain data. Do not echo them into wider logs.
			log.Print("HTTP failure diagnostic write failed (private details withheld)")
		}
	}
	if failedHTTP || now.Sub(d.lastDiagnosticPrune) >= time.Minute {
		if err := d.pruneHTTPFailures(now); err != nil {
			log.Print("HTTP failure diagnostic retention failed (private details withheld)")
		}
	}
}
