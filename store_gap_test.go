package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestNewStoreUnknownGap(t *testing.T) {
	now := time.Now()
	cfg := []MonitorConfig{
		{Name: "recent", Type: "http", URL: "http://x", Interval: Duration(30 * time.Second), Timeout: Duration(time.Second)},
		{Name: "stale", Type: "http", URL: "http://y", Interval: Duration(30 * time.Second), Timeout: Duration(time.Second)},
		{Name: "gone", Type: "http", URL: "http://z", Interval: Duration(30 * time.Second), Timeout: Duration(time.Second), Disabled: true},
	}

	last := map[string]time.Time{
		"recent": now.Add(-30 * time.Second), // within gapFactor*interval → pending
		"stale":  now.Add(-3 * time.Minute),  // far past gapFactor*interval → unknown
		"gone":   now.Add(-3 * time.Minute),  // disabled → must stay disabled
	}

	st := NewStore(cfg, 100, 3, last, now)
	snap := st.Snapshot(now)
	byName := map[string]MonitorSnapshot{}
	for _, m := range snap.Monitors {
		byName[m.Name] = m
	}

	if got := byName["recent"].Status; got != "pending" {
		t.Errorf("recent: want pending, got %q", got)
	}
	if got := byName["stale"].Status; got != "unknown" {
		t.Errorf("stale: want unknown, got %q", got)
	}
	if got := byName["stale"].Since; got == nil || !got.Equal(last["stale"]) {
		t.Errorf("stale.since: want %v, got %v", last["stale"], got)
	}
	if lc := byName["stale"].LastCheck; lc == nil || !lc.Equal(last["stale"]) {
		t.Errorf("stale.last_check: want %v, got %v", last["stale"], lc)
	}
	if got := byName["gone"].Status; got != "disabled" {
		t.Errorf("gone: want disabled, got %q", got)
	}
}

func TestStateFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	sf := NewStateFile(path)
	when := time.Now().Add(-time.Minute)
	if err := sf.Set("web", when); err != nil {
		t.Fatalf("Set: %v", err)
	}

	sf2 := NewStateFile(path)
	got := sf2.LastChecks()["web"]
	if got.IsZero() || !got.Equal(when) {
		t.Fatalf("round-trip: want %v, got %v", when, got)
	}
}

func TestUnknownDoesNotAlertOnFirstHealthyCheck(t *testing.T) {
	now := time.Now()
	cfg := []MonitorConfig{
		{Name: "s", Type: "http", URL: "http://x", Interval: Duration(30 * time.Second), Timeout: Duration(time.Second)},
	}
	st := NewStore(cfg, 100, 3, map[string]time.Time{"s": now.Add(-5 * time.Minute)}, now)
	if st.monitors["s"].status != "unknown" {
		t.Fatalf("precondition: want unknown, got %q", st.monitors["s"].status)
	}
	// First healthy check back must establish baseline silently (no transition).
	tr := st.Record("s", Result{Time: now, Up: true, Latency: time.Millisecond})
	if tr != nil {
		t.Fatalf("first healthy check after unknown should not emit a transition, got %+v", tr)
	}
	if st.monitors["s"].status != "up" {
		t.Fatalf("want up, got %q", st.monitors["s"].status)
	}
}
