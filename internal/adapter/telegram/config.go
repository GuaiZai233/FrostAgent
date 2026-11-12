// Package telegram implements a native Telegram Bot API transport.
package telegram

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	Enabled  bool
	Token    string
	ProxyURL string
}

var tokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{Token: strings.TrimSpace(getenv("TELEGRAM_BOT_TOKEN")), ProxyURL: strings.TrimSpace(getenv("TELEGRAM_PROXY_URL"))}
	raw := strings.TrimSpace(getenv("TELEGRAM_ENABLED"))
	if raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("TELEGRAM_ENABLED must be true or false")
		}
		cfg.Enabled = enabled
	}
	if !cfg.Enabled {
		return cfg, nil
	}
	if !tokenPattern.MatchString(cfg.Token) {
		return cfg, fmt.Errorf("TELEGRAM_BOT_TOKEN must contain a valid BotFather token")
	}
	if cfg.ProxyURL != "" {
		proxy, err := url.Parse(cfg.ProxyURL)
		if err != nil || proxy.Hostname() == "" || proxy.Fragment != "" || proxy.RawQuery != "" || (proxy.Path != "" && proxy.Path != "/") {
			return cfg, fmt.Errorf("TELEGRAM_PROXY_URL must be an absolute HTTP(S) or SOCKS5 proxy URL")
		}
		switch proxy.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return cfg, fmt.Errorf("unsupported TELEGRAM_PROXY_URL scheme")
		}
	}
	return cfg, nil
}
