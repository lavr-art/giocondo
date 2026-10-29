package main

import (
	"net/http"
	"strings"
)

// stubPage is what a scanner or a random visitor sees at "/" when the admin UI is on a secret path.
const stubPage = `<!doctype html>
<html><head><meta charset="utf-8"><title>Welcome</title></head>
<body><h1>It works</h1><p>This server is up and running.</p></body></html>
`

// adminHandler mounts the admin UI (web and /api) under base. Without base the admin UI is at the root.
// With base, "/" serves the stub and other paths outside base return a bare 404.
func adminHandler(base string, admin http.Handler) http.Handler {
	base = normalizeBase(base)
	if base == "" {
		return admin
	}
	mux := http.NewServeMux()
	mux.Handle(base+"/", http.StripPrefix(base, admin))
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, base+"/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(stubPage))
	})
	return mux
}

// normalizeBase turns PANEL_ADMIN_PATH into "/path" without a trailing slash; "/" and "" mean the root.
func normalizeBase(base string) string {
	base = strings.Trim(strings.TrimSpace(base), "/")
	if base == "" {
		return ""
	}
	return "/" + base
}
