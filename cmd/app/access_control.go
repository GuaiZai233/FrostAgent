package main

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// protectListener keeps loopback-only listeners zero-configuration while
// requiring an explicit token before a control plane is exposed remotely.
func protectListener(addr, tokenEnv, realm string, next http.Handler) (http.Handler, error) {
	token := strings.TrimSpace(os.Getenv(tokenEnv))
	requireToken := token != "" || !isLoopbackListenAddr(addr)
	if !requireToken {
		return next, nil
	}
	if token == "" {
		return nil, fmt.Errorf("%s must be set when listening on non-loopback address %q", tokenEnv, addr)
	}
	return accessTokenMiddleware(token, realm, next), nil
}

func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func accessTokenMiddleware(token, realm string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tokenMatchesRequest(r, token) {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Basic realm=%q, charset="UTF-8"`, realm))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

func tokenMatchesRequest(r *http.Request, expected string) bool {
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if scheme, value, ok := strings.Cut(authorization, " "); ok &&
		strings.EqualFold(scheme, "Bearer") && secureStringEqual(strings.TrimSpace(value), expected) {
		return true
	}

	if _, password, ok := r.BasicAuth(); ok && secureStringEqual(password, expected) {
		return true
	}
	return false
}

func secureStringEqual(actual, expected string) bool {
	if len(actual) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

// corsMiddleware allows same-origin browser access and explicit exact origins.
// It never combines a wildcard origin with credentialed requests.
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

		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
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
	if request.TLS != nil {
		requestScheme = "https"
	}
	if strings.EqualFold(parsed.Scheme, requestScheme) && strings.EqualFold(parsed.Host, request.Host) {
		return true
	}
	_, ok := allowed[origin]
	return ok
}
