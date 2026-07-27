package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Duration wraps time.Duration so it can be parsed from a JSON string like "30s".
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) D() time.Duration { return time.Duration(d) }

type Config struct {
	Listen      string          `json:"listen"`
	HistorySize int             `json:"history_size"`
	Monitors    []MonitorConfig `json:"monitors"`
	XMPP        XMPPConfig      `json:"xmpp"`
}

type MonitorConfig struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"` // "http" | "tcp"
	URL          string   `json:"url"`
	Target       string   `json:"target"`
	Interval     Duration `json:"interval"`
	Timeout      Duration `json:"timeout"`
	ExpectStatus []int    `json:"expect_status"`
	Keyword      string   `json:"keyword"`
	// Disabled marks a temporarily-decommissioned service: it's shown greyed
	// out on the status page but never probed and never alerts.
	Disabled bool `json:"disabled"`
}

// Endpoint returns the human-facing target string for display / API.
func (m MonitorConfig) Endpoint() string {
	if m.Type == "http" {
		return m.URL
	}
	return m.Target
}

type XMPPConfig struct {
	Enabled          bool     `json:"enabled"`
	JID              string   `json:"jid"`
	Password         string   `json:"password"`
	Server           string   `json:"server"` // host:port override; defaults to JID domain :5222
	Recipients       []string `json:"recipients"`
	FailureThreshold int      `json:"failure_threshold"`
}

// resolveSecret expands an "env:VAR" reference into the environment variable's
// value; anything else is returned verbatim.
func resolveSecret(v string) string {
	if strings.HasPrefix(v, "env:") {
		return os.Getenv(strings.TrimPrefix(v, "env:"))
	}
	return v
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	// Defaults.
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.HistorySize <= 0 {
		c.HistorySize = 100
	}
	if c.XMPP.FailureThreshold <= 0 {
		c.XMPP.FailureThreshold = 3
	}
	c.XMPP.Password = resolveSecret(c.XMPP.Password)

	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate() error {
	if len(c.Monitors) == 0 {
		return fmt.Errorf("no monitors configured")
	}
	seen := map[string]bool{}
	for i := range c.Monitors {
		m := &c.Monitors[i]
		if m.Name == "" {
			return fmt.Errorf("monitor #%d: name is required", i)
		}
		if seen[m.Name] {
			return fmt.Errorf("duplicate monitor name %q", m.Name)
		}
		seen[m.Name] = true

		// A disabled monitor is never probed, so its probe fields needn't be
		// valid (a decommissioned service may have had its url/target removed).
		if m.Disabled {
			continue
		}

		if m.Interval.D() <= 0 {
			m.Interval = Duration(30 * time.Second)
		}
		if m.Timeout.D() <= 0 {
			m.Timeout = Duration(5 * time.Second)
		}
		switch m.Type {
		case "http":
			if m.URL == "" {
				return fmt.Errorf("monitor %q: http requires \"url\"", m.Name)
			}
		case "tcp":
			if m.Target == "" {
				return fmt.Errorf("monitor %q: tcp requires \"target\" (host:port)", m.Name)
			}
		default:
			return fmt.Errorf("monitor %q: unknown type %q (want \"http\" or \"tcp\")", m.Name, m.Type)
		}
	}

	if c.XMPP.Enabled {
		if c.XMPP.JID == "" || c.XMPP.Password == "" {
			return fmt.Errorf("xmpp enabled but jid/password missing")
		}
		if len(c.XMPP.Recipients) == 0 {
			return fmt.Errorf("xmpp enabled but no recipients")
		}
	}
	return nil
}
