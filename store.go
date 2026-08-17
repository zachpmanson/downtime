package main

import (
	"log"
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
	status          string // "up", "down", "pending", "unknown", "disabled"
	history         []Result
	consecutiveFail int
	lastCheck       time.Time
	since           time.Time // when the current status began
	downSince       time.Time // set while down, used to compute downtime
}

// gapFactor is how many check intervals may pass before a monitor is judged
// to have an unaccounted gap (i.e. downtime itself was likely down). Tolerates
// small scheduling jitter while still catching a real outage window.
const gapFactor = 2

type Store struct {
	mu          sync.RWMutex
	monitors    map[string]*monitorState
	order       []string // preserves config order for display
	historySize int
	threshold   int
	db          *DB // optional SQLite persistence for all-time history
}

// NewStore builds the in-memory store. lastChecks holds the persisted
// pre-restart last-check timestamps (nil/empty if none); any monitor whose
// last check is older than gapFactor check-intervals is seeded as "unknown"
// so the crossed window is shown as an honest gap rather than a guessed state.
// db seeds the in-memory bar window from persisted history (may be nil).
func NewStore(cfgs []MonitorConfig, historySize, threshold int, lastChecks map[string]time.Time, db *DB, now time.Time) *Store {
	s := &Store{
		monitors:    make(map[string]*monitorState),
		historySize: historySize,
		threshold:   threshold,
		db:          db,
	}
	for _, c := range cfgs {
		status := "pending"
		if c.Disabled {
			status = "disabled"
		}
		ms := &monitorState{cfg: c, status: status}
		if !c.Disabled {
			// Seed the last check from the persisted heartbeat so a coverage
			// gap can be reconstructed across the restart.
			if lc, ok := lastChecks[c.Name]; ok && !lc.IsZero() {
				ms.lastCheck = lc
				gap := now.Sub(lc)
				if gap > gapFactor*c.Interval.D() {
					ms.status = "unknown"
					ms.since = lc
				}
			}
		}
		s.monitors[c.Name] = ms
		s.order = append(s.order, c.Name)
	}

	// Re-seed the in-memory bar window from persisted history so the page and
	// status survive restarts. Monitors flagged "unknown" (a real coverage gap
	// when downtime itself was down) are deliberately left seeded-empty so the
	// gap stays visible until a fresh check arrives.
	if db != nil {
		for _, name := range s.order {
			ms := s.monitors[name]
			if ms.cfg.Disabled || ms.status == "unknown" {
				continue
			}
			rows, err := db.Recent(name, s.historySize)
			if err != nil {
				log.Printf("db: seed %s failed: %v", name, err)
				continue
			}
			for _, r := range rows {
				_ = s.applyLocked(name, r)
			}
		}
	}
	return s
}

// Record stores a result, persists it to the optional SQLite history, and
// returns a Transition if the check caused a confirmed status flip (nil
// otherwise).
func (s *Store) Record(name string, r Result) *Transition {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.monitors[name] == nil {
		return nil
	}
	t := s.applyLocked(name, r)
	if s.db != nil {
		s.db.Append(name, r)
	}
	return t
}

// applyLocked applies a result to the in-memory state and derives any
// transition. The caller must hold the lock. It is shared by live recording
// and startup seeding (seeding never persists, so history can't duplicate).
func (s *Store) applyLocked(name string, r Result) *Transition {
	ms := s.monitors[name]

	ms.lastCheck = r.Time
	ms.history = append(ms.history, r)
	if len(ms.history) > s.historySize {
		ms.history = ms.history[len(ms.history)-s.historySize:]
	}

	if r.Up {
		ms.consecutiveFail = 0
		// A single success clears a down state (fast recovery signal).
		if ms.status != "up" {
			// First check after startup (or after an unknown gap) only
			// establishes a baseline — don't fire a "recovered" alert for
			// every healthy service on restart.
			baseline := ms.status == "pending" || ms.status == "unknown"
			var downtime time.Duration
			if !ms.downSince.IsZero() {
				downtime = r.Time.Sub(ms.downSince)
			}
			ms.status = "up"
			ms.since = r.Time
			ms.downSince = time.Time{}
			if baseline {
				return nil
			}
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
		// If it was already down when we first looked (startup baseline),
		// record it silently instead of alerting on restart.
		baseline := ms.status == "pending"
		ms.status = "down"
		ms.since = r.Time
		if baseline {
			return nil
		}
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
			snap.LastLatencyMs = snap.History[n-1].LatencyMs
		}
		if s.db != nil {
			// All-time uptime from the persisted history (survives restarts).
			if st, err := s.db.AllTime(name); err == nil && st.Total > 0 {
				snap.UptimePct = float64(st.Up) / float64(st.Total) * 100
			}
		} else if n := len(ms.history); n > 0 {
			// No persistence: fall back to the in-memory window.
			snap.UptimePct = float64(up) / float64(n) * 100
		}
		out.Monitors = append(out.Monitors, snap)
	}
	return out
}
