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

type versionInfo struct {
	Commit    string `json:"commit"`
	Repo      string `json:"repo"`
	BuiltUnix int64  `json:"built_unix"`
}

// statusResponse embeds the store Snapshot (flattening its generated/monitors
// fields) and adds build metadata for the page footer.
type statusResponse struct {
	Snapshot
	Version versionInfo `json:"version"`
}

func newServer(store *Store) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(statusResponse{
			Snapshot: store.Snapshot(time.Now()),
			Version: versionInfo{
				Commit:    commit,
				Repo:      repoURL,
				BuiltUnix: buildUnixInt(),
			},
		})
	})

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(sub)))

	return mux
}
