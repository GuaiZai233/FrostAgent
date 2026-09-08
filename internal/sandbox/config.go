package sandbox

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	DefaultBaseURL          = "http://127.0.0.1:3874"
	DefaultSessionNamespace = "frostagent"
	DefaultExecutionTimeout = 30 * time.Second
	MaxExecutionTimeout     = 120 * time.Second
	MaxCommandLength        = 65536 // Maximum command character length matching upstream ShellExecRequest
	MaxCwdLength            = 1024  // Maximum cwd character length matching upstream ShellExecRequest
)

// Config defines the configuration for the sandbox subsystem.
type Config struct {
	Enabled          bool
	BaseURL          string
	AuthToken        string
	SessionNamespace string
	ClientTimeout    time.Duration
}

// LoadConfigFromEnv reads sandbox configuration from environment variables.
func LoadConfigFromEnv() Config {
	enabled := ParseBool(os.Getenv("SANDBOX_ENABLED"), false)
	baseURL := strings.TrimSpace(os.Getenv("SANDBOX_BASE_URL"))
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	authToken := strings.TrimSpace(os.Getenv("SANDBOX_AUTH_TOKEN"))
	sessionNamespace := strings.TrimSpace(os.Getenv("SANDBOX_SESSION_NAMESPACE"))
	if sessionNamespace == "" {
		sessionNamespace = DefaultSessionNamespace
	}

	return Config{
		Enabled:          enabled,
		BaseURL:          baseURL,
		AuthToken:        authToken,
		SessionNamespace: sessionNamespace,
		ClientTimeout:    135 * time.Second, // Max execution timeout (120s) + 15s envelope
	}
}

// LoadConfigFromMap reads sandbox configuration from a key-value map (e.g. parsed from .env).
func LoadConfigFromMap(m map[string]string) Config {
	enabled := ParseBool(m["SANDBOX_ENABLED"], false)
	baseURL := strings.TrimSpace(m["SANDBOX_BASE_URL"])
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	authToken := strings.TrimSpace(m["SANDBOX_AUTH_TOKEN"])
	sessionNamespace := strings.TrimSpace(m["SANDBOX_SESSION_NAMESPACE"])
	if sessionNamespace == "" {
		sessionNamespace = DefaultSessionNamespace
	}

	return Config{
		Enabled:          enabled,
		BaseURL:          baseURL,
		AuthToken:        authToken,
		SessionNamespace: sessionNamespace,
		ClientTimeout:    135 * time.Second,
	}
}

// Validate checks whether the configuration is valid when sandbox execution is enabled.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}

	if c.BaseURL == "" {
		return errors.New("SANDBOX_BASE_URL cannot be empty when SANDBOX_ENABLED is true")
	}

	parsed, err := url.Parse(c.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("SANDBOX_BASE_URL %q is not a valid HTTP or HTTPS URL", c.BaseURL)
	}

	if c.AuthToken == "" {
		return errors.New("SANDBOX_AUTH_TOKEN cannot be empty when SANDBOX_ENABLED is true")
	}

	if c.SessionNamespace == "" {
		return errors.New("SANDBOX_SESSION_NAMESPACE cannot be empty when SANDBOX_ENABLED is true")
	}

	return nil
}

// ParseBool parses common boolean string representations.
func ParseBool(val string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "1", "t", "true", "yes", "on":
		return true
	case "0", "f", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
