package frontend

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// Supported locales, ordered by priority for Accept-Language matching.
var supportedLocales = []string{"zh", "en"}

// defaultLocale is used when no Accept-Language preference matches.
const defaultLocale = "zh"

//go:embed all:dist
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded frontend.
//
// Rules:
//   - Root "/" redirects based on the Accept-Language header (Angular i18n pattern).
//   - Paths under a locale prefix fall back to that locale's index.html (SPA routing).
//   - Static assets (paths with a file extension) are served directly.
//   - ConnectRPC paths are not captured — register them on the mux before this handler.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("frontend: embedded dist directory not found: " + err.Error())
	}
	fileFS := http.FS(sub)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Root: redirect based on Accept-Language header.
		if path == "/" {
			locale := detectLocale(r)
			http.Redirect(w, r, "/"+locale+"/", http.StatusFound)
			return
		}

		// SPA fallback: paths without a file extension → locale's index.html.
		if !hasExtension(path) {
			index := resolveLocaleIndex(path)
			serveFile(fileFS, w, r, index)
			return
		}

		http.FileServer(fileFS).ServeHTTP(w, r)
	})
}

// detectLocale picks the best locale based on the Accept-Language header.
// Falls back to defaultLocale if no match or no header present.
func detectLocale(r *http.Request) string {
	header := r.Header.Get("Accept-Language")
	if header == "" {
		return defaultLocale
	}
	for _, tag := range parseAcceptLanguage(header) {
		for _, loc := range supportedLocales {
			if strings.HasPrefix(tag, loc) || strings.HasPrefix(tag, strings.ReplaceAll(loc, "-", "_")) {
				return loc
			}
		}
	}
	return defaultLocale
}

// parseAcceptLanguage extracts language tags from the Accept-Language header.
// Per RFC 7231 §5.3.1, the header lists languages in descending preference order.
func parseAcceptLanguage(header string) []string {
	var tags []string
	for _, seg := range strings.Split(header, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		// Strip quality parameter (e.g. "en;q=0.9" → "en").
		if idx := strings.IndexByte(seg, ';'); idx >= 0 {
			seg = strings.TrimSpace(seg[:idx])
		}
		tags = append(tags, strings.ToLower(seg))
	}
	return tags
}

// supportedLocalePrefix returns the locale subpath and true if the path starts
// with a known locale prefix.
func supportedLocalePrefix(path string) (prefix string, ok bool) {
	for _, loc := range supportedLocales {
		if path == "/"+loc || strings.HasPrefix(path, "/"+loc+"/") {
			return loc, true
		}
	}
	return "", false
}

// resolveLocaleIndex maps an SPA route to its locale's index.html.
//   - "/zh/settings" → "/zh/index.html"
//   - "/en/foo/bar"  → "/en/index.html"
//   - unknown prefix  → default locale's index.html
func resolveLocaleIndex(path string) string {
	if prefix, ok := supportedLocalePrefix(path); ok {
		return "/" + prefix + "/index.html"
	}
	return "/" + defaultLocale + "/index.html"
}

// serveFile serves a single file from the filesystem, with no directory listing.
func serveFile(fsys http.FileSystem, w http.ResponseWriter, r *http.Request, name string) {
	f, err := fsys.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, stat.Name(), stat.ModTime(), f)
}

// hasExtension returns true if the last path segment contains a dot (like .js, .css).
func hasExtension(path string) bool {
	parts := strings.Split(path, "/")
	last := parts[len(parts)-1]
	return strings.Contains(last, ".")
}
