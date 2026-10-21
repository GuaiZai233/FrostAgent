package instanceconfig

import (
	"fmt"
	"net"
)

// Default listener addresses can be overridden at link time for container builds.
// SQL settings take precedence, subject to container listener isolation.
var (
	DefaultListenAddr   = "127.0.0.1:8080"
	DefaultWSListenAddr = "127.0.0.1:1234"
	// Container builds require a loopback management listener in the connector's network namespace.
	ManagementLoopbackOnly = "false"
)

// ValidateListenerIsolation prevents SQL settings from exposing management routes
// outside the connector's shared network namespace, including through a shared port.
func ValidateListenerIsolation(listenAddr, wsListenAddr string) error {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return fmt.Errorf("invalid management listener: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("container management listener must use a loopback IP address")
	}
	managementPort, err := net.LookupPort("tcp", port)
	if err != nil {
		return fmt.Errorf("invalid management port: %w", err)
	}
	_, wsPort, err := net.SplitHostPort(wsListenAddr)
	if err != nil {
		return fmt.Errorf("invalid adapter listener: %w", err)
	}
	adapterPort, err := net.LookupPort("tcp", wsPort)
	if err != nil {
		return fmt.Errorf("invalid adapter port: %w", err)
	}
	if managementPort == adapterPort {
		return fmt.Errorf("container management and adapter listeners must use different ports")
	}
	return nil
}
