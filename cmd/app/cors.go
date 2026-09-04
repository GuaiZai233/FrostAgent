package main

import (
	"net/http"
	"net/url"
	"os"
	"strings"
)

// corsMiddleware allows same-origin browser access and explicit exact origins.
// It prevents unauthorized cross-origin requests from external malicious websites.
func corsMiddleware(next http.Handler) http.Handler {
	allowedOrigins := configuredHTTPOrigins()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			if !httpOriginAllowed(origin, r, allowedOrigins) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
		}

		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, Connect-Protocol-Version")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func configuredHTTPOrigins() map[string]struct{} {
	result := make(map[string]struct{})
	for _, value := range strings.Split(os.Getenv("HTTP_ALLOWED_ORIGINS"), ",") {
		if origin := strings.TrimSpace(value); origin != "" {
			result[origin] = struct{}{}
		}
	}
	return result
}

func httpOriginAllowed(origin string, request *http.Request, allowed map[string]struct{}) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	requestScheme := "http"
	if request.TLS != nil || strings.EqualFold(request.Header.Get("X-Forwarded-Proto"), "https") {
		requestScheme = "https"
	}
	if strings.EqualFold(parsed.Scheme, requestScheme) && strings.EqualFold(parsed.Host, request.Host) {
		return true
	}
	_, ok := allowed[origin]
	return ok
}
