package core

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"
)

func TestComputerUseTransportDoesNotTrustForwardedTLS(t *testing.T) {
	r := httptest.NewRequest("GET", "http://synthetic/ws", nil)
	r.RemoteAddr = "192.0.2.10:3333"
	r.Header.Set("X-Forwarded-Proto", "https")
	if ComputerUseSecureTransport(r) {
		t.Fatal("untrusted forwarded header authorized plaintext")
	}
	r.TLS = &tls.ConnectionState{}
	if !ComputerUseSecureTransport(r) {
		t.Fatal("TLS rejected")
	}
	r.TLS = nil
	r.RemoteAddr = "127.0.0.1:3333"
	if !ComputerUseSecureTransport(r) {
		t.Fatal("loopback rejected")
	}
}
