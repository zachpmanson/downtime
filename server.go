package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
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
// fields), adds the page list for the UI nav, and build metadata for the
// footer. The watches fields are for the JS polling loop.
type statusResponse struct {
	Snapshot
	Pages   []string    `json:"pages"`
	Version versionInfo `json:"version"`
}

func newServer(store *Store) http.Handler {
	mux := http.NewServeMux()

	// Page-aware status API: GET /api/status serves the "index" page,
	// GET /api/status/<page> a named page. 404 for unknown pages.
	// (ServeMux pattern wildcards: {name} matches one path segment and shows
	// up in r.PathValue("name").)
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		serveStatus(w, store, "index")
	})
	mux.HandleFunc("GET /api/status/{page}", func(w http.ResponseWriter, r *http.Request) {
		serveStatus(w, store, r.PathValue("page"))
	})

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	// Page routes: the embedded single-file UI is served for the root page and
	// for every configured page subpath — the JS picks its page from the URL.
	// Everything else (style.css, app.js, favicon.svg, 404s) goes to the
	// file server.
	var files http.Handler = http.FileServer(http.FS(sub))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		// Tolerate a trailing slash on page URLs (/external/ == /external).
		if len(p) > 1 && strings.HasSuffix(p, "/") {
			p = strings.TrimSuffix(p, "/")
		}
		page := ""
		if p == "/" {
			page = "index"
		} else if len(p) > 1 && !strings.Contains(p[1:], "/") {
			page = strings.TrimPrefix(p, "/")
		}
		if page != "" && (page == "index" || store.HasPage(page)) {
			b, rerr := fs.ReadFile(sub, "index.html")
			if rerr == nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				data, merr := json.Marshal(statusForPage(store, page))
				if merr != nil {
					http.Error(w, "could not render status", http.StatusInternalServerError)
					return
				}
				initial := append([]byte(`<script id="initial-status" type="application/json">`), data...)
				initial = append(initial, []byte(`</script>`)...)
				b = bytes.Replace(b, []byte("<!-- INITIAL_STATUS -->"), initial, 1)
				_, _ = w.Write(b)
				return
			}
		}
		files.ServeHTTP(w, r)
	})

	return mux
}

// serveStatus writes the JSON status snapshot for one page. Unknown pages
// get a 404 JSON body (Content-Type is set by the caller alongside the
// status code helpers).
func serveStatus(w http.ResponseWriter, store *Store, page string) {
	if !store.HasPage(page) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("{\"error\":\"no such page\"}\n"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(statusForPage(store, page))
}

func statusForPage(store *Store, page string) statusResponse {
	return statusResponse{
		Snapshot: store.Snapshot(time.Now(), page),
		Pages:    store.Pages(),
		Version: versionInfo{
			Commit:    commit,
			Repo:      repoURL,
			BuiltUnix: buildUnixInt(),
		},
	}
}
