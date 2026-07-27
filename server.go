package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"time"
)

//go:embed all:web
var webFS embed.FS

func newServer(store *Store) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(store.Snapshot(time.Now()))
	})

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(sub)))

	return mux
}
