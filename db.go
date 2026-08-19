package main

import (
	"database/sql"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

// DB persists check history to SQLite so uptime percentages are all-time and
// survive restarts, instead of resetting on each process start.
//
// It uses modernc.org/sqlite (a pure-Go, cgo-free driver) so the binary stays
// a single static build and the Nix package needs no native sqlite.
type DB struct {
	db *sql.DB
}

// OpenDB opens (creating if needed) the SQLite history database. A nil return
// means persistence is disabled (empty path).
func OpenDB(path string) (*DB, error) {
	if path == "" {
		return nil, nil
	}
	handle, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := handle.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		// WAL is a nicety for reads-while-writing; non-fatal if unavailable.
		log.Printf("db: WAL unavailable: %v", err)
	}
	if _, err := handle.Exec(`CREATE TABLE IF NOT EXISTS checks (
		monitor    TEXT    NOT NULL,
		ts         INTEGER NOT NULL, -- UnixNano
		up         INTEGER NOT NULL,
		latency_ms REAL    NOT NULL,
		err        TEXT
	)`); err != nil {
		return nil, err
	}
	if _, err := handle.Exec(`CREATE INDEX IF NOT EXISTS idx_checks_monitor_ts ON checks(monitor, ts)`); err != nil {
		return nil, err
	}
	return &DB{db: handle}, nil
}

// Close closes the underlying database handle.
func (d *DB) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

// Stats is an all-time rolling summary for one monitor.
type Stats struct {
	Total int64
	Up    int64
}

// AllTime returns the all-time total and up check counts for a monitor.
func (d *DB) AllTime(monitor string) (Stats, error) {
	if d == nil || d.db == nil {
		return Stats{}, nil
	}
	var s Stats
	if err := d.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(up), 0) FROM checks WHERE monitor = ?`,
		monitor,
	).Scan(&s.Total, &s.Up); err != nil {
		return s, err
	}
	return s, nil
}

// Recent returns the most recent n results for a monitor in ascending time
// order (oldest first), used to re-seed the in-memory bar window on startup.
func (d *DB) Recent(monitor string, n int) ([]Result, error) {
	if d == nil || d.db == nil {
		return nil, nil
	}
	rows, err := d.db.Query(
		`SELECT ts, up, latency_ms, err FROM checks WHERE monitor = ? ORDER BY ts DESC LIMIT ?`,
		monitor, n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Result, 0)
	for rows.Next() {
		var ts int64
		var up int64
		var latMs float64
		var errText sql.NullString
		if err := rows.Scan(&ts, &up, &latMs, &errText); err != nil {
			return nil, err
		}
		r := Result{
			Time:    time.Unix(0, ts),
			Up:      up == 1,
			Latency: time.Duration(latMs * float64(time.Millisecond)),
		}
		if errText.Valid {
			r.Err = errText.String
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse to ascending order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// DailyWindowDays is how many trailing days each monitor's daily bar strip
// covers. 30 bars at one per day reads as a "much bigger window" than the old
// ~40 per-check strip.
const dailyWindowDays = 30

// Daily aggregates a monitor's persisted checks into per-day buckets for the
// trailing DailyWindowDays, oldest first. Days are bucketed in the process's
// local timezone. Returns nil when persistence is disabled.
func (d *DB) Daily(monitor string, now time.Time) ([]DayBucket, error) {
	if d == nil || d.db == nil {
		return nil, nil
	}
	cutoff := dayStart(now).AddDate(0, 0, -(dailyWindowDays - 1)).UnixNano()
	rows, err := d.db.Query(
		`SELECT date(ts / 1000000000, 'unixepoch', 'localtime') AS day,
		        COUNT(*), COALESCE(SUM(up), 0)
		 FROM checks
		 WHERE monitor = ? AND ts >= ?
		 GROUP BY day
		 ORDER BY day ASC`,
		monitor, cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]DayBucket, 0)
	for rows.Next() {
		var day string
		var total, up int64
		if err := rows.Scan(&day, &total, &up); err != nil {
			return nil, err
		}
		pct := 0.0
		if total > 0 {
			pct = float64(up) / float64(total) * 100
		}
		out = append(out, DayBucket{Day: day, Up: up, Total: total, Pct: pct})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Append records one check result. Failures are logged and never propagate:
// persistence must not disrupt live monitoring.
func (d *DB) Append(monitor string, r Result) {
	if d == nil || d.db == nil {
		return
	}
	var errText *string
	if r.Err != "" {
		errText = &r.Err
	}
	up := int64(0)
	if r.Up {
		up = 1
	}
	latMs := float64(r.Latency) / float64(time.Millisecond)
	if _, err := d.db.Exec(
		`INSERT INTO checks(monitor, ts, up, latency_ms, err) VALUES(?, ?, ?, ?, ?)`,
		monitor, r.Time.UnixNano(), up, latMs, errText,
	); err != nil {
		log.Printf("db append failed for %s: %v", monitor, err)
	}
}
