package main

import (
	"context"
	"log"
	"sync"
	"time"

	"downtime/notify"
)

// runMonitors starts one goroutine per monitor and blocks until ctx is
// cancelled. Each goroutine checks immediately, then on its interval.
func runMonitors(ctx context.Context, cfgs []MonitorConfig, store *Store, n notify.Notifier) {
	var wg sync.WaitGroup
	for _, m := range cfgs {
		wg.Add(1)
		go func(m MonitorConfig) {
			defer wg.Done()
			runOne(ctx, m, store, n)
		}(m)
	}
	wg.Wait()
}

func runOne(ctx context.Context, m MonitorConfig, store *Store, n notify.Notifier) {
	ticker := time.NewTicker(m.Interval.D())
	defer ticker.Stop()

	do := func() {
		r := check(ctx, m)
		if t := store.Record(m.Name, r); t != nil {
			emit(n, t)
		}
	}

	do() // check right away so the page isn't empty on boot
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			do()
		}
	}
}

func emit(n notify.Notifier, t *Transition) {
	if t.Up {
		log.Printf("[UP]   %s recovered after %s", t.Monitor, t.Downtime.Round(time.Second))
	} else {
		log.Printf("[DOWN] %s is down: %s", t.Monitor, t.Err)
	}
	if err := n.Notify(notify.Event{
		Monitor:  t.Monitor,
		Up:       t.Up,
		Err:      t.Err,
		Downtime: t.Downtime,
		Time:     t.Time,
	}); err != nil {
		log.Printf("notify failed for %s: %v", t.Monitor, err)
	}
}
