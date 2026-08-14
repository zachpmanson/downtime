package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StateFile persists a single small datum per monitor: the timestamp of its
// last check. Results/history stay in memory; this only exists so that after a
// crash or restart downtime can reconstruct the coverage gap and mark the
// crossed window as "unknown" rather than guessing up/down.
//
// It is intentionally tiny and append-free: a single JSON file, rewritten
// atomically (temp + rename) on each check. At this scale (a handful of
// monitors ticking at ~30s) that is a negligible amount of I/O.
type StateFile struct {
	path string
	mu   sync.Mutex
	m    map[string]time.Time
}

// NewStateFile loads the persisted last-check timestamps (or an empty set if
// the file is absent/corrupt) for the given path.
func NewStateFile(path string) *StateFile {
	s := &StateFile{path: path, m: map[string]time.Time{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return s // no file yet — first boot, or persistence not desired
	}
	var data struct {
		Monitors map[string]time.Time `json:"monitors"`
	}
	if err := json.Unmarshal(b, &data); err != nil {
		return s // corrupt file: start clean rather than crash the monitor
	}
	for k, v := range data.Monitors {
		s.m[k] = v
	}
	return s
}

// LastChecks returns a copy of the loaded timestamps keyed by monitor name.
func (s *StateFile) LastChecks() map[string]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]time.Time, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out
}

// Set updates the persisted last-check time for a monitor and writes it to
// disk atomically. A failure to persist is logged-never: it must not disrupt
// monitoring, and the in-memory copy still advances regardless.
func (s *StateFile) Set(name string, t time.Time) error {
	if s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.m[name] = t

	data := struct {
		Monitors map[string]time.Time `json:"monitors"`
	}{Monitors: s.m}

	b, err := json.Marshal(data)
	if err != nil {
		return err
	}

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, s.path)
}
