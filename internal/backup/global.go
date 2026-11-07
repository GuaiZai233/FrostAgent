package backup

import (
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	settingsservice "github.com/GuaiZai233/FrostAgent/internal/service/settings"
)

// GlobalSettings is separate from the per-instance setting.json contract.
type GlobalSettings struct {
	FormatVersion int                `json:"format_version"`
	Values        map[string]string  `json:"settings"`
	ExtraEnv      *map[string]string `json:"extra_env,omitempty"`
	SecretNotice  string             `json:"secret_notice"`
}

func globalExportableKeys() map[string]bool {
	classified := settingsservice.ExportableValueKeys()
	keys := make(map[string]bool)
	for key := range classified {
		if instanceconfig.GlobalKeys[key] || instanceconfig.SharedKeys[key] {
			keys[key] = true
		}
	}
	return keys
}

func ExportGlobalSettings(global *instanceconfig.Store) GlobalSettings {
	result := GlobalSettings{FormatVersion: FormatVersion,
		SecretNotice: "常规设置中的密钥与凭据来源仍被排除；额外 env 注入项会以明文导出，请妥善保管备份文件。",
		Values:       make(map[string]string)}
	for key, value := range global.Snapshot() {
		if globalExportableKeys()[key] {
			result.Values[key] = value
		} else if instanceconfig.GlobalKeys[key] {
			result.Values[key] = ""
		}
	}
	return result
}

func ImportGlobalSettings(global *instanceconfig.Store, data GlobalSettings) error {
	if err := ValidateFormat(data.FormatVersion); err != nil {
		return err
	}
	keys := globalExportableKeys()
	for key, value := range data.Values {
		if !keys[key] {
			if instanceconfig.GlobalKeys[key] && value == "" {
				delete(data.Values, key)
				continue
			}
			return fmt.Errorf("global setting %s is not importable", key)
		}
	}
	return global.ReplaceDatabaseSubset(keys, data.Values)
}
