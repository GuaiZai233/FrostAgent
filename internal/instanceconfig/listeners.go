package instanceconfig

import (
	"fmt"
	"net"
)

// Default listener addresses bind management and adapters to loopback.
const (
	DefaultListenAddr   = "127.0.0.1:8080"
	DefaultWSListenAddr = "127.0.0.1:1023"
)

// Container builds require a loopback management listener and a separate adapter port.
var ManagementLoopbackOnly = "false"

// ValidateListenerIsolation prevents container settings from exposing management
// routes outside loopback or reusing the adapter port.
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
