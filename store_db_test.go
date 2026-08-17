package main

import (
	"path/filepath"
	"testing"
	"time"
)

// TestStoreSeedsAndAllTimeUptimeFromDB verifies the restart path: persisted
// history re-seeds the in-memory bar window and uptime_pct is all-time.
func TestStoreSeedsAndAllTimeUptimeFromDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// 10 past checks: 9 up, 1 down -> 90% all-time.
	now := time.Now().Add(-time.Hour)
	for i := 0; i < 10; i++ {
		db.Append("web", Result{Time: now.Add(time.Duration(i) * time.Minute), Up: i != 5, Latency: 10 * time.Millisecond})
	}

	cfg := []MonitorConfig{{Name: "web", Type: "http", URL: "http://x", Interval: Duration(time.Minute)}}
	// Simulate a restart: fresh store with no in-memory data, same DB.
	st := NewStore(cfg, 100, 3, map[string]time.Time{}, db, now.Add(2*time.Hour))

	snap := st.Snapshot(now.Add(2 * time.Hour))
	if len(snap.Monitors) != 1 {
		t.Fatalf("expected 1 monitor, got %d", len(snap.Monitors))
	}
	m := snap.Monitors[0]
	if m.UptimePct != 90 {
		t.Fatalf("all-time uptime = %v, want 90", m.UptimePct)
	}
	if len(m.History) != 10 {
		t.Fatalf("seeded history len = %d, want 10", len(m.History))
	}
}

func TestStoreUptimeWindowedWithoutDB(t *testing.T) {
	cfg := []MonitorConfig{{Name: "a", Type: "http", URL: "http://x", Interval: Duration(time.Minute)}}
	st := NewStore(cfg, 100, 3, map[string]time.Time{}, nil, time.Now())
	for i := 0; i < 4; i++ {
		st.Record("a", Result{Time: time.Now().Add(time.Duration(i) * time.Second), Up: i != 1})
	}
	snap := st.Snapshot(time.Now())
	// 3 up of 4 -> 75%, computed from the in-memory window (no DB).
	if snap.Monitors[0].UptimePct != 75 {
		t.Fatalf("windowed uptime = %v, want 75", snap.Monitors[0].UptimePct)
	}
}
