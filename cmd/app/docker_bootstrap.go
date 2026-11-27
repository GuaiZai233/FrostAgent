package main

import (
	"fmt"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
)

const dockerBootstrapTokenKey = "FROSTAGENT_BOOTSTRAP_CONTROL_TOKEN"

// initializeDockerControlToken seeds a bridge deployment's selected SQL credential.
// It is a one-time bootstrap input, never an override of an existing credential.
func initializeDockerControlToken(global *instanceconfig.Store, getenv func(string) string) error {
	if strings.TrimSpace(global.Get("MCP_CONTROL_TOKEN")) != "" || strings.TrimSpace(global.Get("ADMIN_TOKEN")) != "" {
		return nil
	}
	token := strings.TrimSpace(getenv(dockerBootstrapTokenKey))
	if token == "" {
		return nil
	} // Local IPC remains available before HTTP credentials are initialized.
	valid := len(token) >= 32 && len(token) <= 4096
	for _, char := range token {
		valid = valid && char >= '!' && char <= '~'
	}
	if !valid {
		return fmt.Errorf("Invalid management bootstrap token: set %s to 32-4096 random printable ASCII characters without spaces for the first startup; existing SQL tokens are preserved", dockerBootstrapTokenKey)
	}
	return global.Update("MCP_CONTROL_TOKEN", token, false)
}
