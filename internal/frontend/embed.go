package frontend

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded frontend.
//
// Rules:
//   - Paths under / are served from the embedded dist/ directory.
//   - Paths without a file extension fall back to index.html (SPA routing).
//   - ConnectRPC paths (containing "/frostagent.") are never captured — they are
//     not matched by this handler and should be registered on the mux before
//     the frontend fallback.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("frontend: embedded dist directory not found: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// SPA fallback: paths without a file extension → index.html
		if !hasExtension(path) && path != "/" {
			// Rewrite to "/" so FileServer serves index.html
			r2 := new(http.Request)
			*r2 = *r
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}

		fileServer.ServeHTTP(w, r)
	})
}

// hasExtension returns true if the last path segment contains a dot (like .js, .css).
func hasExtension(path string) bool {
	parts := strings.Split(path, "/")
	last := parts[len(parts)-1]
	return strings.Contains(last, ".")
}
