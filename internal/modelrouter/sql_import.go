package modelrouter

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"FrostAgent/internal/storage"
)

// ReferencedConfiguration includes only catalog records needed by this instance.
// Callers redact credential material before exporting it.
func ReferencedConfiguration(cfg Configuration) Configuration {
	result := cloneConfiguration(cfg)
	models, endpoints := map[string]bool{}, map[string]bool{}
	for _, binding := range cfg.GlobalBindings {
		if binding.ModelID != "" {
			models[binding.ModelID] = true
		}
	}
	for _, group := range cfg.GroupOverrides {
		for _, binding := range group.Bindings {
			if binding.ModelID != "" {
				models[binding.ModelID] = true
			}
		}
	}
	result.Models, result.Endpoints = nil, nil
	for _, model := range cfg.Models {
		if models[model.ID] {
			result.Models = append(result.Models, model)
			endpoints[model.EndpointID] = true
		}
	}
	for _, endpoint := range cfg.Endpoints {
		if endpoints[endpoint.ID] {
			result.Endpoints = append(result.Endpoints, endpoint)
		}
	}
	return result
}

// Import only adds missing catalog definitions; it never overwrites global
// providers or credentials. Conflicting IDs are remapped with their bindings.
// The caller holds DB.ModelRouterMu across the enclosing transaction.
func WriteImportedConfigurationTx(ctx context.Context, db *storage.DB, tx *sql.Tx, instanceID string, cfg Configuration) error {
	cfg = cloneConfiguration(cfg)
	for i := range cfg.Endpoints {
		cfg.Endpoints[i].APIKeySource = ""
		cfg.Endpoints[i].APIKeyRef = ""
		cfg.Endpoints[i].APIKeyConfigured = false
	}
	normalizeConfiguration(&cfg)
	if err := validateStoredConfiguration(cfg); err != nil {
		return err
	}
	catalog, err := readCatalogTx(ctx, tx)
	if err != nil {
		return err
	}
	endpointIDs, modelIDs, endpointNames, modelNames := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range catalog.Endpoints {
		endpointIDs[e.ID] = true
		endpointNames[strings.ToLower(e.DisplayName)] = true
	}
	for _, m := range catalog.Models {
		modelIDs[m.ID] = true
		modelNames[strings.ToLower(m.DisplayName)] = true
	}
	// An unreferenced credential can remain after a historical migration. A new
	// imported provider must not adopt that credential merely by matching its ID.
	rows, err := tx.QueryContext(ctx, `SELECT ref FROM provider_secrets`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return err
		}
		if strings.HasPrefix(ref, "endpoint/") {
			endpointIDs[strings.TrimPrefix(ref, "endpoint/")] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	endpointMap, modelMap := map[string]string{}, map[string]string{}
	changed := false
	for _, incoming := range cfg.Endpoints {
		oldID := incoming.ID
		for _, existing := range catalog.Endpoints {
			if existing.ID == incoming.ID && existing.DisplayName == incoming.DisplayName &&
				existing.BaseURL == incoming.BaseURL && existing.Enabled == incoming.Enabled {
				endpointMap[oldID] = existing.ID
				break
			}
		}
		if endpointMap[oldID] != "" {
			continue
		}
		if endpointIDs[incoming.ID] {
			incoming.ID, err = importedID(endpointIDs)
			if err != nil {
				return err
			}
		}
		incoming.DisplayName = importedName(incoming.DisplayName, endpointNames)
		incoming.APIKeySource, incoming.APIKeyRef = APIKeyStorageManual, manualAPIKeyRef(incoming.ID)
		incoming.APIKeyConfigured = false
		endpointIDs[incoming.ID] = true
		endpointMap[oldID] = incoming.ID
		catalog.Endpoints = append(catalog.Endpoints, incoming)
		changed = true
	}
	for _, incoming := range cfg.Models {
		oldID := incoming.ID
		incoming.EndpointID = endpointMap[incoming.EndpointID]
		for _, existing := range catalog.Models {
			if reflect.DeepEqual(existing, incoming) {
				modelMap[oldID] = existing.ID
				break
			}
		}
		if modelMap[oldID] != "" {
			continue
		}
		if modelIDs[incoming.ID] {
			incoming.ID, err = importedID(modelIDs)
			if err != nil {
				return err
			}
		}
		incoming.DisplayName = importedName(incoming.DisplayName, modelNames)
		modelIDs[incoming.ID] = true
		modelMap[oldID] = incoming.ID
		catalog.Models = append(catalog.Models, incoming)
		changed = true
	}
	remap := func(bindings map[Workload]Binding) {
		for workload, binding := range bindings {
			if binding.ModelID != "" {
				binding.ModelID = modelMap[binding.ModelID]
				bindings[workload] = binding
			}
		}
	}
	remap(cfg.GlobalBindings)
	for _, group := range cfg.GroupOverrides {
		remap(group.Bindings)
	}
	cfg.Endpoints, cfg.Models = catalog.Endpoints, catalog.Models
	if err := validateStoredConfiguration(cfg); err != nil {
		return err
	}
	if changed {
		catalog.Revision++
		if err := writeCatalogTx(ctx, db, tx, catalog); err != nil {
			return err
		}
	}
	var revision int64
	err = tx.QueryRowContext(ctx, db.Bind(`SELECT revision FROM model_revisions WHERE instance_id = ?`), instanceID).Scan(&revision)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	cfg.Revision = revision + 1
	return writeConfigurationTx(ctx, db, tx, instanceID, cfg)
}

func readCatalogTx(ctx context.Context, tx *sql.Tx) (Configuration, error) {
	cfg := defaultConfiguration()
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM provider_revision WHERE id = 1`).Scan(&cfg.Revision); err != nil {
		return cfg, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, display_name, base_url, api_key_source, api_key_ref, enabled FROM provider_endpoints ORDER BY position, id`)
	if err != nil {
		return cfg, err
	}
	for rows.Next() {
		var endpoint Endpoint
		var enabled int
		if err := rows.Scan(&endpoint.ID, &endpoint.DisplayName, &endpoint.BaseURL, &endpoint.APIKeySource, &endpoint.APIKeyRef, &enabled); err != nil {
			rows.Close()
			return cfg, err
		}
		endpoint.Enabled = enabled != 0
		cfg.Endpoints = append(cfg.Endpoints, endpoint)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return cfg, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id, display_name, endpoint_id, upstream_model, enabled, capabilities, max_input_tokens FROM provider_models ORDER BY position, id`)
	if err != nil {
		return cfg, err
	}
	defer rows.Close()
	for rows.Next() {
		var model Model
		var enabled int
		var capabilities string
		if err := rows.Scan(&model.ID, &model.DisplayName, &model.EndpointID, &model.UpstreamModel, &enabled, &capabilities, &model.MaxInputTokens); err != nil {
			return cfg, err
		}
		if err := json.Unmarshal([]byte(capabilities), &model.Capabilities); err != nil {
			return cfg, err
		}
		model.Enabled = enabled != 0
		cfg.Models = append(cfg.Models, model)
	}
	return cfg, rows.Err()
}

// SnapshotCatalogWithinMutation and RestoreCatalogWithinMutation are paired
// around instance restoration. The caller holds DB.CatalogMutationMu so no
// unrelated publication can be lost during compensation.
func SnapshotCatalogWithinMutation(db *storage.DB) (Configuration, error) {
	db.ModelRouterMu.RLock()
	defer db.ModelRouterMu.RUnlock()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Configuration{}, err
	}
	defer tx.Rollback()
	return readCatalogTx(ctx, tx)
}

func RestoreCatalogWithinMutation(db *storage.DB, snapshot Configuration) error {
	db.ModelRouterMu.Lock()
	defer db.ModelRouterMu.Unlock()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeCatalogTx(ctx, db, tx, snapshot); err != nil {
		return err
	}
	return tx.Commit()
}

func importedID(used map[string]bool) (string, error) {
	for {
		value := make([]byte, 12)
		if _, err := rand.Read(value); err != nil {
			return "", err
		}
		id := "imported_" + hex.EncodeToString(value)
		if !used[id] {
			return id, nil
		}
	}
}
func importedName(base string, used map[string]bool) string {
	value := base
	for suffix := 2; used[strings.ToLower(value)]; suffix++ {
		value = fmt.Sprintf("%s (%d)", base, suffix)
	}
	used[strings.ToLower(value)] = true
	return value
}
