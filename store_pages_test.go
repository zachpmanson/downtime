package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPagesGroupAndFilter covers page assignment: monitors land on their
// configured page (default "index"), "index" always comes first in the page
// list, and Snapshot(page) only returns that page's monitors.
func TestPagesGroupAndFilter(t *testing.T) {
	cfg := []MonitorConfig{
		{Name: "own-a", Type: "http", URL: "http://a", Interval: Duration(time.Minute)},
		{Name: "third-1", Type: "http", URL: "http://b", Interval: Duration(time.Minute), Page: "external"},
		{Name: "own-b", Type: "http", URL: "http://c", Interval: Duration(time.Minute)},
		{Name: "third-2", Type: "http", URL: "http://d", Interval: Duration(time.Minute), Page: "external"},
	}
	st := NewStore(cfg, 100, 3, map[string]time.Time{}, nil, time.Now())

	if got := strings.Join(st.Pages(), ","); got != "index,external" {
		t.Fatalf("pages = %v, want [index external]", got)
	}
	if !st.HasPage("index") || !st.HasPage("external") || st.HasPage("nope") {
		t.Fatalf("HasPage got the wrong answers")
	}

	idx := st.Snapshot(time.Now(), "index")
	if len(idx.Monitors) != 2 {
		t.Fatalf("index page has %d monitors, want 2", len(idx.Monitors))
	}
	if idx.Monitors[0].Name != "own-a" || idx.Monitors[1].Name != "own-b" {
		t.Fatalf("index page order wrong: %+v", idx.Monitors)
	}

	ext := st.Snapshot(time.Now(), "external")
	if len(ext.Monitors) != 2 || ext.Monitors[0].Name != "third-1" || ext.Monitors[1].Name != "third-2" {
		t.Fatalf("external page wrong: %+v", ext)
	}
}

// TestIndexPageStaysFirst: even when the first configured monitor is on a
// named page, "index" is still the first entry in the page list.
func TestIndexPageStaysFirst(t *testing.T) {
	cfg := []MonitorConfig{
		{Name: "third", Type: "http", URL: "http://x", Interval: Duration(time.Minute), Page: "external"},
		{Name: "own", Type: "http", URL: "http://y", Interval: Duration(time.Minute)},
	}
	st := NewStore(cfg, 100, 3, map[string]time.Time{}, nil, time.Now())
	if got := strings.Join(st.Pages(), ","); got != "index,external" {
		t.Fatalf("pages = %v, want [index external]", got)
	}
}

// TestConfigParsesAndValidatesPages: the page key round-trips through JSON and
// invalid page names (path/query metacharacters, dots that would shadow static
// assets) are rejected at load time.
func TestConfigParsesAndValidatesPages(t *testing.T) {
	save := func(page string) string {
		p := "test.json"
		body := `{
			"listen": ":0",
			"monitors": [
				{"name":"m","type":"http","url":"http://x","interval":"30s","page":"` + page + `"},
				{"name":"n","type":"http","url":"http://y","interval":"30s"}
			],
			"xmpp":{"enabled":false}
		}`
		path := filepath.Join(t.TempDir(), p)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	c, err := LoadConfig(save("external"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Monitors[0].PageName(); got != "external" {
		t.Fatalf("page = %q, want external", got)
	}
	if got := c.Monitors[1].PageName(); got != "index" {
		t.Fatalf("unset page must default to index, got %q", got)
	}

	for _, bad := range []string{"a/b", "a b", "style.css", "a?b", "a%20b", "this-page-name-is-deliberately-longer-than-sixty-four-characters-0123456789"} {
		if _, perr := LoadConfig(save(bad)); perr == nil {
			t.Fatalf("page %q must be rejected, LoadConfig succeeded", bad)
		}
	}
}