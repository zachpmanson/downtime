package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDBCrudAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hist.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if db == nil {
		t.Fatal("OpenDB returned nil for a set path")
	}

	base := time.Now().Add(-time.Hour)
	for i := 0; i < 10; i++ {
		up := i%4 != 3 // down at i=3,7 -> 8 up of 10 (80%)
		db.Append("web", Result{
			Time:    base.Add(time.Duration(i) * time.Minute),
			Up:      up,
			Latency: 200 * time.Millisecond,
		})
	}

	st, err := db.AllTime("web")
	if err != nil {
		t.Fatalf("AllTime: %v", err)
	}
	if st.Total != 10 || st.Up != 8 {
		t.Fatalf("AllTime = %+v, want total=10 up=8", st)
	}

	recent, err := db.Recent("web", 3)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(recent) != 3 {
		t.Fatalf("Recent len = %d, want 3", len(recent))
	}
	// Most recent three are failures at idx 7,8,9 -> idx9 up, idx8? i%4!=3
	// ascending order (oldest first within the 3-window).
	if !recent[0].Time.Before(recent[2].Time) {
		t.Fatalf("Recent not ascending: %v", recent)
	}
	_ = db.Close()

	// Simulate a restart: reopen the DB and confirm the data is still there
	// (this is the whole point of persisting history).
	db2, err := OpenDB(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
	st2, _ := db2.AllTime("web")
	if st2.Total != 10 || st2.Up != 8 {
		t.Fatalf("after restart AllTime = %+v, want total=10 up=8", st2)
	}

	// Non-existent monitor returns zeros.
	zero, _ := db2.AllTime("nope")
	if zero.Total != 0 || zero.Up != 0 {
		t.Fatalf("missing monitor AllTime = %+v, want zeros", zero)
	}
}

func TestOpenDBNilOnEmptyPath(t *testing.T) {
	db, err := OpenDB("")
	if err != nil {
		t.Fatalf("OpenDB('') err: %v", err)
	}
	if db != nil {
		_ = db.Close()
		t.Fatal("OpenDB('') should return nil (persistence disabled)")
	}
}

func TestDBCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "hist.db")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	db, err := OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("db file not created: %v", err)
	}
}
