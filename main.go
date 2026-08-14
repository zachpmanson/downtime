package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"downtime/notify"
)

func main() {
	cfgPath := flag.String("config", "config.json", "path to JSON config file")
	flag.Parse()

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	st := NewStateFile(cfg.StateFile)

	store := NewStore(cfg.Monitors, cfg.HistorySize, cfg.XMPP.FailureThreshold,
		st.LastChecks(), time.Now())

	var n notify.Notifier = notify.LogNotifier{}
	if cfg.XMPP.Enabled {
		n = &notify.XMPPNotifier{
			JID:        cfg.XMPP.JID,
			Password:   cfg.XMPP.Password,
			Server:     cfg.XMPP.Server,
			Recipients: cfg.XMPP.Recipients,
		}
		log.Printf("xmpp notifications enabled (%d recipient(s), threshold %d)",
			len(cfg.XMPP.Recipients), cfg.XMPP.FailureThreshold)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go runMonitors(ctx, cfg.Monitors, store, n, st)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           newServer(store),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("serving status page on %s (%d monitors)", cfg.Listen, len(cfg.Monitors))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	os.Exit(0)
}
