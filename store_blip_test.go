package main

import (
	"testing"
	"time"
)

// TestSubThresholdBlipDoesNotInflateDowntime guards the bug where a transient
// (sub-threshold) failure set downSince, a subsequent success while already
// "up" failed to clear it, and a later genuine outage then reported downtime
// that included the stale blip. Observed live as "Nextcloud recovered after
// 14h50m" for what was actually a ~3m outage.
func TestSubThresholdBlipDoesNotInflateDowntime(t *testing.T) {
	cfg := []MonitorConfig{{Name: "web", Type: "http", URL: "http://x", Interval: Duration(time.Minute), Timeout: Duration(time.Second)}}
	st := NewStore(cfg, 100, 3, map[string]time.Time{}, nil, time.Now())

	now := time.Now()

	// 1. First healthy check establishes "up" silently.
	if tr := st.Record("web", Result{Time: now, Up: true}); tr != nil {
		t.Fatalf("baseline up should not transition, got %+v", tr)
	}
	if st.monitors["web"].status != "up" {
		t.Fatalf("precondition: want status up, got %q", st.monitors["web"].status)
	}

	// 2. A sub-threshold blip: 1 failure (threshold is 3). Status must stay
	//    "up", but downSince gets set (the old behaviour).
	blip := now.Add(time.Minute)
	if tr := st.Record("web", Result{Time: blip, Up: false, Err: "blip"}); tr != nil {
		t.Fatalf("sub-threshold failure should not flip to down, got %+v", tr)
	}
	if st.monitors["web"].status != "up" {
		t.Fatalf("status should stay up through a sub-threshold blip, got %q", st.monitors["web"].status)
	}

	// 3. Recovery: success while already "up" must clear downSince so the blip
	//    doesn't leak into a later outage's start time.
	after := blip.Add(time.Minute)
	if tr := st.Record("web", Result{Time: after, Up: true}); tr != nil {
		t.Fatalf("success while up should not transition, got %+v", tr)
	}
	if !st.monitors["web"].downSince.IsZero() {
		t.Fatalf("downSince should be cleared after the blip recovers, got %v", st.monitors["web"].downSince)
	}

	// 4. Later, a genuine outage: three consecutive failures cross the
	//    threshold and flip to "down".
	t1 := after.Add(time.Minute)
	t2 := t1.Add(time.Minute)
	t3 := t2.Add(time.Minute)
	for _, tt := range []time.Time{t1, t2, t3} {
		st.Record("web", Result{Time: tt, Up: false, Err: "real outage"})
	}
	if st.monitors["web"].status != "down" {
		t.Fatalf("want status down after threshold is crossed, got %q", st.monitors["web"].status)
	}

	// 5. On recovery the reported downtime must reflect only the genuine
	//    outage (~3 minutes), not the stale blip added on top.
	rec := t3.Add(time.Minute)
	rep := st.Record("web", Result{Time: rec, Up: true})
	if rep == nil || !rep.Up {
		t.Fatalf("want a recovery transition, got %+v", rep)
	}
	if got := rep.Downtime.Round(time.Minute); got != 3 * time.Minute {
		t.Fatalf("reported downtime = %v, want 3m (genuine outage only)", got)
	}
}