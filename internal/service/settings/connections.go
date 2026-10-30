package settings

import (
	"fmt"
	"os"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/wsclient"
)

// Validate the final adapter configuration before any setting is committed.
// In particular, a draft may set the mode and its required URL together, but
// clearing the active client's URL alone must not stop a working runtime.
func (s *Service) validateConnectionChanges(changes map[string]*string) error {
	for _, adapter := range []string{"ONEBOT", "ASTRBOT"} {
		affected := false
		for _, suffix := range []string{"_WS_MODE", "_WS_URL", "_WS_TOKEN"} {
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
