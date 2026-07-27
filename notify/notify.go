// Package notify delivers status-change alerts. Notifier is an interface so
// new channels (Slack, webhooks, email) can be added without touching the
// monitor loop.
package notify

import (
	"fmt"
	"log"
	"time"
)

type Event struct {
	Monitor  string
	Up       bool // true = recovery, false = went down
	Err      string
	Downtime time.Duration
	Time     time.Time
}

// Message renders a human-readable one-liner for the event.
func (e Event) Message() string {
	if e.Up {
		return fmt.Sprintf("✅ %s is back UP (down for %s)", e.Monitor, e.Downtime.Round(time.Second))
	}
	return fmt.Sprintf("🔴 %s is DOWN: %s", e.Monitor, e.Err)
}

type Notifier interface {
	Notify(Event) error
}

// LogNotifier just logs; used when XMPP is disabled so transitions are still
// visible in the process output.
type LogNotifier struct{}

func (LogNotifier) Notify(e Event) error {
	log.Printf("notify: %s", e.Message())
	return nil
}
