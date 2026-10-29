// Package wsclient owns outbound WebSocket connection configuration and retries.
package wsclient

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

const (
	Reverse = "reverse"
	Forward = "forward"
)

// Config is a snapshot for one adapter activation. URLs and tokens are never
// included in errors or connection logs.
type Config struct {
	Mode  string
	URL   string
	Token string
}

// Load preserves the existing inbound listener when no mode was configured.
func Load(getenv func(string) string, adapter string) (Config, error) {
	if adapter != "ONEBOT" && adapter != "ASTRBOT" {
		return Config{}, fmt.Errorf("unknown WebSocket adapter")
	}
	config := Config{Mode: Reverse}
	if getenv != nil {
		rawMode, rawURL := getenv(adapter+"_WS_MODE"), getenv(adapter+"_WS_URL")
		if strings.IndexFunc(rawMode, unicode.IsControl) >= 0 || strings.IndexFunc(rawURL, unicode.IsControl) >= 0 {
			return Config{}, fmt.Errorf("%s: WS_MODE and WS_URL must not contain control characters", adapter)
		}
		config.Mode = strings.TrimSpace(rawMode)
		config.URL = strings.TrimSpace(rawURL)
		config.Token = getenv(adapter + "_WS_TOKEN")
	}
	if config.Mode == "" {
		config.Mode = Reverse
	}
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", adapter, err)
	}
	return config, nil
}

// Validate rejects malformed destinations without echoing sensitive values.
func (c Config) Validate() error {
	if c.Mode != "" && c.Mode != Reverse && c.Mode != Forward {
		return fmt.Errorf("WS_MODE must be reverse or forward")
	}
	if strings.IndexFunc(c.Token, unicode.IsControl) >= 0 {
		return fmt.Errorf("WS_TOKEN must not contain control characters")
	}
	if c.URL == "" {
		if c.Mode == Forward {
			return fmt.Errorf("WS_URL is required for forward WebSocket")
		}
		return nil
	}
	if strings.IndexFunc(c.URL, unicode.IsSpace) >= 0 || strings.IndexFunc(c.URL, unicode.IsControl) >= 0 {
		return fmt.Errorf("WS_URL must not contain whitespace or control characters")
	}
	endpoint, err := url.Parse(c.URL)
	if err != nil || (endpoint.Scheme != "ws" && endpoint.Scheme != "wss") ||
		endpoint.Hostname() == "" || endpoint.Opaque != "" || endpoint.User != nil ||
		endpoint.Fragment != "" || strings.Contains(c.URL, "#") {
		return fmt.Errorf("WS_URL must be an absolute ws:// or wss:// URL without credentials or fragment")
	}
	if port := endpoint.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return fmt.Errorf("WS_URL contains an invalid port")
		}
	} else if strings.HasSuffix(endpoint.Host, ":") {
		return fmt.Errorf("WS_URL contains an invalid port")
	}
	return nil
}
