package backup

import (
	"FrostAgent/internal/groupsummary"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/mcp"
	"FrostAgent/internal/memory"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/service/dialogue"
	"FrostAgent/internal/sticker"
	"FrostAgent/internal/storage"
	"fmt"
	"net/url"
)

// These fields have an explicit non-secret value contract. Uncatalogued
// settings retain their names but export empty values until classified.
var exportableSettingValues = map[string]bool{
	"BOT_NAME": true, "BOT_ALIASES": true, "ADMIN_QQ_IDS": true,
	"SYSTEM_PROMPT": true, "MAX_CONTEXT_MESSAGES": true, "MAX_CONTEXT_CHARS": true,
	"MEMORY_REFLECTION_TIMEOUT": true, "ENABLE_AT_IN_GROUP_MSG": true,
	"GROUP_REPLY_ON_MENTION": true, "ENABLE_REPLY_IN_GROUP_MSG": true,
	"GROUP_COMPACT_BUFFER_SIZE": true, "GROUP_COMPACT_MAX_BUFFER_SIZE": true,
	"GROUP_COMPACT_MIN_INTERVAL": true, "GROUP_RAW_CONTEXT_MAX_CHARS": true,
	"MEMORY_EXTRACT_BATCH_MIN": true, "MEMORY_EXTRACT_BATCH_MAX": true,
	"BILLING_ENABLED": true, "BILLING_MAX_OUTPUT_TOKENS": true,
	"BILLING_SAFETY_MULTIPLIER":            true,
	"BILLING_PROMPT_PRICE_PER_MILLION":     true,
	"BILLING_COMPLETION_PRICE_PER_MILLION": true,
	"AGENT_MAX_ITERATIONS":                 true,
	"ENABLE_ONEBOT_ADAPTER":                true, "ENABLE_ASTRBOT_ADAPTER": true,
	"SECURITY_GATEWAY_TIMEOUT": true, "SECURITY_CLASSIFIER_TIMEOUT": true,
}

func ExportSettings(db *storage.DB, instanceID string) (Settings, error) {
	result := Settings{FormatVersion: FormatVersion, SecretNotice: SecretNotice}
	config, err := instanceconfig.OpenDatabase(db, instanceID, false)
	if err != nil {
		return result, err
	}
	result.Values = config.Snapshot()
	for key := range result.Values {
		if !exportableSettingValues[key] {
			result.Values[key] = ""
		}
	}

	router := modelrouter.NewSQL(db, instanceID)
	if err := router.LoadError(); err != nil {
		return result, err
	}
	result.ModelRouter = router.Active()
	for i := range result.ModelRouter.Endpoints {
		endpoint := &result.ModelRouter.Endpoints[i]
		endpoint.APIKeySource = ""
		endpoint.APIKeyRef = ""
		endpoint.APIKeyConfigured = false
		endpoint.BaseURL = publicURL(endpoint.BaseURL)
	}

	mcpConfig, err := mcp.NewSQLConfigStore(db, instanceID).Load()
	if err != nil {
		return result, err
	}
	result.MCP = *mcpConfig
	for i := range result.MCP.Servers {
		transport := &result.MCP.Servers[i].Transport
		transport.Env = nil
		transport.Headers = nil
		transport.Args = nil
		transport.URL = publicURL(transport.URL)
	}
	result.Dialogues, err = dialogue.LoadExamplesSQL(db, instanceID)
	return result, err
}

func ExportMemories(db *storage.DB, instanceID string) (Memories, error) {
	result := Memories{FormatVersion: FormatVersion,
		DuplicateWarning: "按 ID 合并可跳过已存在的记忆，但不同 ID 可能形成重复内容；请在前端人工查重。"}
	private := memory.NewSQLStore(db, instanceID)
	var err error
	if result.PrivateEntries, err = private.ListAll(); err != nil {
		return result, err
	}
	if result.PrivateArchives, err = private.ListMergeArchives(); err != nil {
		return result, err
	}
	manager := memory.NewSQLGroupManager(db, instanceID, nil)
	groups, err := manager.ListGroups()
	if err != nil {
		return result, err
	}
	result.Groups = make([]GroupMemory, 0, len(groups))
	for _, group := range groups {
		store, err := manager.GetGroupStoreForPlatform(group.Platform, group.GroupID)
		if err != nil {
			return result, err
		}
		item := GroupMemory{Platform: group.Platform, GroupID: group.GroupID}
		if item.Profile, err = store.GetProfile(); err != nil {
			return result, err
		}
		if item.Entries, err = store.ListAll(); err != nil {
			return result, err
		}
		if item.Archives, err = store.ListMergeArchives(); err != nil {
			return result, err
		}
		result.Groups = append(result.Groups, item)
	}
	return result, nil
}

func ExportSummaries(db *storage.DB, instanceID string) (Summaries, error) {
	result := Summaries{FormatVersion: FormatVersion}
	store, err := groupsummary.NewSQLStore(db, instanceID)
	if err != nil {
		return result, err
	}
	result.Records, err = store.List()
	return result, err
}

func ExportStickers(db *storage.DB, instanceID, imageDir string) (Stickers, error) {
	result := Stickers{FormatVersion: FormatVersion}
	store, err := sticker.NewSQLStore(db, instanceID, imageDir)
	if err != nil {
		return result, err
	}
	result.Entries = store.List()
	return result, nil
}

func publicURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func ValidateFormat(version int) error {
	if version != FormatVersion {
		return fmt.Errorf("unsupported backup format version %d", version)
	}
	return nil
}
