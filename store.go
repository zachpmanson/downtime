package main

import (
	"sync"
	"time"
)

// Transition describes a status flip worth notifying about.
type Transition struct {
	Monitor  string
	Up       bool          // true = recovered, false = went down
	Err      string        // last error (when going down)
	Downtime time.Duration // how long it was down (on recovery)
	Time     time.Time
}

// monitorState is the mutable per-monitor record held in memory.
type monitorState struct {
	cfg             MonitorConfig
	status          string // "up", "down", "pending"
	history         []Result
	consecutiveFail int
	lastCheck       time.Time
	since           time.Time // when the current status began
	downSince       time.Time // set while down, used to compute downtime
}

type Store struct {
	mu          sync.RWMutex
	monitors    map[string]*monitorState
	order       []string // preserves config order for display
	historySize int
	threshold   int
}

func NewStore(cfgs []MonitorConfig, historySize, threshold int) *Store {
	s := &Store{
		monitors:    make(map[string]*monitorState),
		historySize: historySize,
		threshold:   threshold,
	}
	for _, c := range cfgs {
		s.monitors[c.Name] = &monitorState{cfg: c, status: "pending"}
		s.order = append(s.order, c.Name)
	}
	return s
}

// Record stores a result and returns a Transition if the check caused a
// confirmed status flip (nil otherwise).
func (s *Store) Record(name string, r Result) *Transition {
	s.mu.Lock()
	defer s.mu.Unlock()

	ms := s.monitors[name]
	if ms == nil {
		return nil
	}

	ms.lastCheck = r.Time
	ms.history = append(ms.history, r)
	if len(ms.history) > s.historySize {
		ms.history = ms.history[len(ms.history)-s.historySize:]
	}

	if r.Up {
		ms.consecutiveFail = 0
		// A single success clears a down state (fast recovery signal).
		if ms.status != "up" {
			var downtime time.Duration
			if !ms.downSince.IsZero() {
				downtime = r.Time.Sub(ms.downSince)
			}
			ms.status = "up"
			ms.since = r.Time
			ms.downSince = time.Time{}
			return &Transition{Monitor: name, Up: true, Downtime: downtime, Time: r.Time}
		}
		return nil
	}

	// Failure.
	ms.consecutiveFail++
	if ms.downSince.IsZero() {
		ms.downSince = r.Time
	}
	// Only flip to "down" (and notify) once the threshold is crossed, and only
	// on the transition itself — not on every subsequent failure.
	if ms.status != "down" && ms.consecutiveFail >= s.threshold {
		ms.status = "down"
		ms.since = r.Time
		return &Transition{Monitor: name, Up: false, Err: r.Err, Time: r.Time}
	}
	return nil
}

// --- Snapshot types for the JSON API ---

type ResultSnapshot struct {
	Time      time.Time `json:"time"`
	Up        bool      `json:"up"`
	LatencyMs float64   `json:"latency_ms"`
	Err       string    `json:"err,omitempty"`
}

type MonitorSnapshot struct {
	Name          string           `json:"name"`
	Type          string           `json:"type"`
	Target        string           `json:"target"`
	Status        string           `json:"status"`
	UptimePct     float64          `json:"uptime_pct"`
	LastLatencyMs float64          `json:"last_latency_ms"`
	LastCheck     *time.Time       `json:"last_check,omitempty"`
	Since         *time.Time       `json:"since,omitempty"`
	History       []ResultSnapshot `json:"history"`
}

type Snapshot struct {
	Generated time.Time         `json:"generated"`
	Monitors  []MonitorSnapshot `json:"monitors"`
}

// Snapshot returns a read-only view suitable for JSON encoding.
func (s *Store) Snapshot(now time.Time) Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := Snapshot{Generated: now, Monitors: make([]MonitorSnapshot, 0, len(s.order))}
	for _, name := range s.order {
		ms := s.monitors[name]
		snap := MonitorSnapshot{
			Name:    ms.cfg.Name,
			Type:    ms.cfg.Type,
			Target:  ms.cfg.Endpoint(),
			Status:  ms.status,
			History: make([]ResultSnapshot, 0, len(ms.history)),
		}
		if !ms.lastCheck.IsZero() {
			t := ms.lastCheck
			snap.LastCheck = &t
		}
		if !ms.since.IsZero() {
			t := ms.since
			snap.Since = &t
		}

		var up int
		for _, r := range ms.history {
			if r.Up {
				up++
			}
			snap.History = append(snap.History, ResultSnapshot{
				Time:      r.Time,
				Up:        r.Up,
				LatencyMs: float64(r.Latency.Microseconds()) / 1000,
				Err:       r.Err,
			})
		}
		if n := len(ms.history); n > 0 {
			snap.UptimePct = float64(up) / float64(n) * 100
			snap.LastLatencyMs = snap.History[n-1].LatencyMs
		}
		out.Monitors = append(out.Monitors, snap)
	}
	return out
}
