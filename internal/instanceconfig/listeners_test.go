package instanceconfig

import "testing"

func TestValidateListenerIsolation(t *testing.T) {
	for _, tc := range []struct {
		name, management, adapter string
		wantError                 bool
	}{
		{"loopback", "127.0.0.1:8081", "0.0.0.0:1234", false},
		{"ipv6 loopback", "[::1]:8081", ":1234", false},
		{"custom loopback port", "127.0.0.2:8091", ":4321", false},
		{"wildcard", "0.0.0.0:8081", ":1234", true},
		{"ipv6 wildcard", "[::]:8081", ":1234", true},
		{"empty host", ":8081", ":1234", true},
		{"external address", "192.0.2.1:8081", ":1234", true},
		{"hostname", "localhost:8081", ":1234", true},
		{"malformed management", "127.0.0.1", ":1234", true},
		{"malformed adapter", "127.0.0.1:8081", "invalid", true},
		{"invalid management port", "127.0.0.1:invalid", ":1234", true},
		{"invalid adapter port", "127.0.0.1:8081", ":invalid", true},
		{"same address", "127.0.0.1:8081", "127.0.0.1:8081", true},
		{"same port wildcard", "127.0.0.1:8081", "0.0.0.0:8081", true},
		{"equivalent port", "127.0.0.1:8081", "0.0.0.0:08081", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateListenerIsolation(tc.management, tc.adapter)
			if (err != nil) != tc.wantError {
				t.Fatalf("ValidateListenerIsolation(%q, %q) = %v, wantError %v", tc.management, tc.adapter, err, tc.wantError)
			}
		})
	}
}
