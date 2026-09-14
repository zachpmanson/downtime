package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestNotifyFalseSuppressesTransitions guards the canary use case: a monitor
// that is expected to be down (e.g. YouTube's RSS feed, 404 most of the day)
// must still flip status on the page, but must never emit alert transitions.
func TestNotifyFalseSuppressesTransitions(t *testing.T) {
	silent := false
	cfg := []MonitorConfig{
		{Name: "canary", Type: "http", URL: "http://x",
		 Interval: Duration(time.Minute), Timeout: Duration(time.Second),
		 Notify: &silent},
	}
	st := NewStore(cfg, 100, 3, map[string]time.Time{}, nil, time.Now())

	now := time.Now()
	// Baseline up is silent regardless.
	if tr := st.Record("canary", Result{Time: now, Up: true}); tr != nil {
		t.Fatalf("baseline up should not transition, got %+v", tr)
	}

	// Crossing the threshold flips the status...
	t1 := now.Add(time.Minute)
	t2 := t1.Add(time.Minute)
	t3 := t2.Add(time.Minute)
	for _, tt := range []time.Time{t1, t2, t3} {
		if tr := st.Record("canary", Result{Time: tt, Up: false, Err: "404"}); tr != nil {
			t.Fatalf("notify:false must suppress the down transition, got %+v", tr)
		}
	}
	if st.monitors["canary"].status != "down" {
		t.Fatalf("status must still flip to down, got %q", st.monitors["canary"].status)
	}
	if st.monitors["canary"].downSince.IsZero() {
		t.Fatalf("downSince must still be tracked for later downtime reporting")
	}

	// ...and recovery is silent too, though status still recovers.
	t4 := t3.Add(time.Minute)
	if tr := st.Record("canary", Result{Time: t4, Up: true}); tr != nil {
		t.Fatalf("notify:false must suppress the recovery transition, got %+v", tr)
	}
	if st.monitors["canary"].status != "up" {
		t.Fatalf("status must recover to up, got %q", st.monitors["canary"].status)
	}
}

// TestPerMonitorFailureThresholdOverridesGlobal covers the knob requested for
// sleepy canaries: one monitor flips (and alerts) on its first failure while
// others still need the global count.
func TestPerMonitorFailureThresholdOverridesGlobal(t *testing.T) {
	on := 1
	cfg := []MonitorConfig{
		{Name: "canary", Type: "http", URL: "http://x",
		 Interval: Duration(time.Minute), Timeout: Duration(time.Second),
		 FailureThreshold: &on},
		{Name: "normal", Type: "http", URL: "http://y",
		 Interval: Duration(time.Minute), Timeout: Duration(time.Second)},
	}
	st := NewStore(cfg, 100, 3, map[string]time.Time{}, nil, time.Now())

	now := time.Now()
	for _, name := range []string{"canary", "normal"} {
		if tr := st.Record(name, Result{Time: now, Up: true}); tr != nil {
			t.Fatalf("%q baseline up should not transition, got %+v", name, tr)
		}
	}

	// First failure: canary's own threshold is 1, so it flips and alerts...
	t1 := now.Add(time.Minute)
	if tr := st.Record("canary", Result{Time: t1, Up: false, Err: "404"}); tr == nil {
		t.Fatalf("canary: threshold 1 must flip on the first failure, got nil transition")
	}
	if st.monitors["canary"].status != "down" {
		t.Fatalf("canary must be down after one failure, got %q", st.monitors["canary"].status)
	}
	// ...while normal (no override) stays up below the global threshold.
	if tr := st.Record("normal", Result{Time: t1, Up: false, Err: "blip"}); tr != nil {
		t.Fatalf("normal: must not flip below the global threshold, got %+v", tr)
	}
	if st.monitors["normal"].status != "up" {
		t.Fatalf("normal must stay up after one failure (global threshold 3), got %q",
			st.monitors["normal"].status)
	}

	// Normal flips once its own three failures land.
	t2 := t1.Add(time.Minute)
	t3 := t2.Add(time.Minute)
	for _, tt := range []time.Time{t2, t3} {
		st.Record("normal", Result{Time: tt, Up: false, Err: "real outage"})
	}
	if st.monitors["normal"].status != "down" {
		t.Fatalf("normal must be down after the global threshold, got %q",
			st.monitors["normal"].status)
	}
}

// TestConfigParsesPerMonitorKnobs guards the JSON round-trip of the new
// monitor fields — a binary that predates them would hard-fail on the keys
// (DisallowUnknownFields), which is exactly the upgrade hazard the feature
// avoids for every existing deployment (the keys are optional).
func TestConfigParsesPerMonitorKnobs(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	body := `{
		"listen": ":0",
		"monitors": [
			{"name":"canary","type":"http","url":"http://x","interval":"30m",
			 "notify":false,"failure_threshold":1},
			{"name":"plain","type":"http","url":"http://y","interval":"30s"}
		],
		"xmpp": {"enabled":false}
	}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	canary := c.Monitors[0]
	if !(canary.Notify != nil && !*canary.Notify) {
		t.Fatalf("canary: want notify=false, got %v", canary.Notify)
	}
	if canary.Threshold(3) != 1 {
		t.Fatalf("canary: want threshold 1 (global 3), got %v", canary.Threshold(3))
	}
	if canary.ShouldNotify() {
		t.Fatalf("canary: ShouldNotify() must be false")
	}

	plain := c.Monitors[1]
	if !plain.ShouldNotify() {
		t.Fatalf("plain: omitted notify must default to true")
	}
	if plain.Threshold(3) != 3 {
		t.Fatalf("plain: want global threshold 3, got %v", plain.Threshold(3))
	}
}