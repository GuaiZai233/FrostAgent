package backup

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/wsclient"
	"github.com/GuaiZai233/FrostAgent/internal/mcp"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

// PreviewRestore uses only reads. Import executes these validations again under
// its own mutation locks; a plan does not reserve names or catalog identities.
func PreviewRestore(ctx context.Context, db *storage.DB, decoded DecodedInstance) (modelrouter.ImportImpact, error) {
	if err := db.RequireNativeSchema(ctx, "restore a complete instance backup"); err != nil {
		return modelrouter.ImportImpact{}, err
	}
	manifest := decoded.Manifest
	if manifest.DatabaseSchemaVersion < 0 || manifest.DatabaseMinimumCompatibleVersion < 0 ||
		manifest.DatabaseMinimumCompatibleVersion > manifest.DatabaseSchemaVersion {
		return modelrouter.ImportImpact{}, fmt.Errorf("invalid backup database compatibility metadata")
	}
	// ADR-0001: a newer source schema alone never blocks a restore. Only the
	// declared minimum compatible version names structures this backend lacks.
	if manifest.DatabaseMinimumCompatibleVersion > storage.SupportedSchemaVersion {
		return modelrouter.ImportImpact{}, fmt.Errorf("backup requires database schema compatibility %d; this backend supports up to %d",
			manifest.DatabaseMinimumCompatibleVersion, storage.SupportedSchemaVersion)
	}
	for key, value := range decoded.Settings.Values {
		if strings.ContainsRune(value, 0) {
			return modelrouter.ImportImpact{}, fmt.Errorf("setting %s contains NUL", key)
		}
	}
	if _, err := wsclient.Load(func(key string) string {
		if strings.HasSuffix(key, "_TOKEN") {
			return ""
		}
		return decoded.Settings.Values[key]
	}, "ONEBOT"); err != nil {
		return modelrouter.ImportImpact{}, fmt.Errorf("invalid imported connection settings: %w", err)
	}
	mcpConfig := decoded.Settings.MCP
	mcpConfig.Servers = slices.Clone(mcpConfig.Servers)
	if err := prepareImportedMCP(&mcpConfig); err != nil {
		return modelrouter.ImportImpact{}, err
	}
	return modelrouter.PreviewImportedConfiguration(ctx, db, decoded.Settings.ModelRouter)
}

// prepareImportedMCP applies the same URL normalization and validation as the
// settings import transaction, so a preview cannot accept what restore rejects.
func prepareImportedMCP(cfg *mcp.Config) error {
	for i := range cfg.Servers {
		cfg.Servers[i].Transport.URL = publicURL(cfg.Servers[i].Transport.URL)
	}
	return mcp.PrepareImportedConfig(cfg)
}
