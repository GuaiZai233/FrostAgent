package core

import (
	"net"
	"net/http"
)

// ComputerUseSecureTransport does not trust X-Forwarded-* from arbitrary
// clients. TLS termination may use a trusted same-host loopback proxy.
func ComputerUseSecureTransport(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback()
}
