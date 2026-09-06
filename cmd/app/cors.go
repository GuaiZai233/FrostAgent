package main

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// corsMiddleware enforces strict origin and host boundaries.
// It protects against DNS rebinding and malicious cross-origin requests.
func corsMiddleware(next http.Handler) http.Handler {
	allowedOrigins, allowedHosts := configuredHTTPOrigins()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Validate Host header to protect against DNS rebinding
		if !isHostAllowed(r.Host, allowedHosts) {
			http.Error(w, "host not allowed", http.StatusForbidden)
			return
		}

		// 2. Validate Origin header if present
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

func configuredHTTPOrigins() (map[string]struct{}, map[string]struct{}) {
	origins := make(map[string]struct{})
	hosts := make(map[string]struct{})
	for _, value := range strings.Split(os.Getenv("HTTP_ALLOWED_ORIGINS"), ",") {
		origin := strings.TrimSpace(value)
		if origin == "" {
			continue
		}
		origins[origin] = struct{}{}
		if parsed, err := url.Parse(origin); err == nil && parsed.Host != "" {
			hosts[parsed.Host] = struct{}{}
		}
	}
	return origins, hosts
}

func isLoopbackHost(rawHost string) bool {
	host := rawHost
	if h, _, err := net.SplitHostPort(rawHost); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isHostAllowed(host string, allowedHosts map[string]struct{}) bool {
	if isLoopbackHost(host) {
		return true
	}
	_, ok := allowedHosts[host]
	return ok
}

func httpOriginAllowed(origin string, request *http.Request, allowed map[string]struct{}) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}

	// Explicitly configured origins always allowed
	if _, ok := allowed[origin]; ok {
		return true
	}

	// Same-origin is only trusted automatically if the host is a local loopback.
	// This prevents DNS rebinding (e.g. attacker.example resolving to 127.0.0.1 where Origin == Host).
	if !isLoopbackHost(request.Host) {
		return false
	}

	requestScheme := "http"
	if request.TLS != nil || strings.EqualFold(request.Header.Get("X-Forwarded-Proto"), "https") {
		requestScheme = "https"
	}
	return strings.EqualFold(parsed.Scheme, requestScheme) && strings.EqualFold(parsed.Host, request.Host)
}
