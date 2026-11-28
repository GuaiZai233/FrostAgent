package settings

import (
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/sandbox"
	"os"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/wsclient"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
)

// Validate the final adapter configuration before any setting is committed.
// In particular, a draft may set the mode and its required URL together, but
// clearing the active client's URL alone must not stop a working runtime.
func (s *Service) validateConnectionChanges(changes map[string]*string) error {
	for _, adapter := range []string{"ONEBOT"} {
		affected := false
		for _, suffix := range []string{"_WS_ENABLED", "_WS_MODE", "_WS_URL", "_WS_TOKEN", "_WS_INBOUND_TOKEN"} {
			if _, ok := changes[adapter+suffix]; ok {
				affected = true
			}
		}
		if !affected {
			continue
		}
		get := func(key string) string {
			if value, changed := changes[key]; changed {
				if value == nil {
					return ""
				}
				return *value
			}
			if s.config != nil {
				return s.config.Get(key)
			}
			return os.Getenv(key)
		}
		if _, err := wsclient.Load(get, adapter); err != nil {
			return fmt.Errorf("%s 连接配置无效: %w", adapter, err)
		}
	}
	return nil
}

func (s *Service) validateScopedCandidate(changes map[string]*string) error {
	affectedSandbox := false
	for key := range changes {
		affectedSandbox = affectedSandbox || strings.HasPrefix(key, "SANDBOX_")
	}
	if affectedSandbox {
		get := func(key string) string {
			if value, ok := changes[key]; ok {
				if value == nil {
					return ""
				}
				return *value
			}
			if store := s.store(key); store != nil {
				return store.Get(key)
			}
			return os.Getenv(key)
		}
		cfg := sandbox.LoadConfig(get)
		if cfg.Enabled {
			if err := cfg.Validate(); err != nil {
				return err
			}
		}
	}

	return nil
}

// validateListenerChanges checks the listener pair that applying changes would
// produce, so a dry run rejects addresses the listener apply step rejects.
func (s *Service) validateListenerChanges(changes map[string]*string) error {
	_, listenChanged := changes["LISTEN_ADDR"]
	_, wsChanged := changes["WS_LISTEN_ADDR"]
	if !listenChanged && !wsChanged {
		return nil
	}
	final := func(key, fallback string) string {
		value := ""
		if changed, ok := changes[key]; ok {
			if changed != nil {
				value = *changed
			}
		} else if store := s.store(key); store != nil {
			value = store.Get(key)
		}
		if value == "" {
			return fallback
		}
		return value
	}
	return instanceconfig.ValidateListeners(final("LISTEN_ADDR", instanceconfig.DefaultListenAddr),
		final("WS_LISTEN_ADDR", instanceconfig.DefaultWSListenAddr))
}
