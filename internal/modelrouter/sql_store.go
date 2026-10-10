package modelrouter

import (
	"FrostAgent/internal/storage"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (m *Manager) loadSQL() error {
	ctx := context.Background()
	cfg := defaultConfiguration()
	err := m.db.SQL.QueryRowContext(ctx, m.db.Bind(`SELECT revision FROM model_revisions
		WHERE instance_id = ?`), m.instanceID).Scan(&cfg.Revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	rows, err := m.db.SQL.QueryContext(ctx, m.db.Bind(`SELECT id, display_name, base_url,
		api_key_source, api_key_ref, enabled FROM model_endpoints WHERE instance_id = ? ORDER BY position, id`), m.instanceID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var endpoint Endpoint
		var source string
		var enabled int
		if err := rows.Scan(&endpoint.ID, &endpoint.DisplayName, &endpoint.BaseURL,
			&source, &endpoint.APIKeyRef, &enabled); err != nil {
			rows.Close()
			return err
		}
		endpoint.APIKeySource = APIKeyStorage(source)
		endpoint.Enabled = enabled != 0
		endpoint.APIKeyConfigured = m.secrets.Configured(endpoint)
		cfg.Endpoints = append(cfg.Endpoints, endpoint)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = m.db.SQL.QueryContext(ctx, m.db.Bind(`SELECT id, display_name, endpoint_id,
		upstream_model, enabled, capabilities FROM models WHERE instance_id = ? ORDER BY position, id`), m.instanceID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var model Model
		var capabilities string
		var enabled int
		if err := rows.Scan(&model.ID, &model.DisplayName, &model.EndpointID,
			&model.UpstreamModel, &enabled, &capabilities); err != nil {
			rows.Close()
			return err
		}
		model.Enabled = enabled != 0
		if err := json.Unmarshal([]byte(capabilities), &model.Capabilities); err != nil {
			rows.Close()
			return err
		}
		cfg.Models = append(cfg.Models, model)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = m.db.SQL.QueryContext(ctx, m.db.Bind(`SELECT platform, group_id, workload,
		mode, model_id FROM model_bindings WHERE instance_id = ?`), m.instanceID)
	if err != nil {
		return err
	}
	overrides := make(map[string]*GroupOverride)
	for rows.Next() {
		var platform, groupID, workload, mode, modelID string
		if err := rows.Scan(&platform, &groupID, &workload, &mode, &modelID); err != nil {
			rows.Close()
			return err
		}
		binding := Binding{Mode: BindingMode(mode), ModelID: modelID}
		if platform == "" && groupID == "" {
			cfg.GlobalBindings[Workload(workload)] = binding
			continue
		}
		key := platform + "\x00" + groupID
		override := overrides[key]
		if override == nil {
			override = &GroupOverride{Platform: platform, GroupID: groupID, Bindings: make(map[Workload]Binding)}
			overrides[key] = override
		}
		override.Bindings[Workload(workload)] = binding
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, override := range overrides {
		cfg.GroupOverrides = append(cfg.GroupOverrides, *override)
	}
	normalizeConfiguration(&cfg)
	if err := validateConfiguration(cfg); err != nil {
		return err
	}
	m.active = cfg
	return nil
}

func (m *Manager) writeSQL(cfg Configuration) error {
	ctx := context.Background()
	tx, err := m.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeConfigurationTx(ctx, m.db, tx, m.instanceID, cfg); err != nil {
		return err
	}
	return tx.Commit()
}

// WriteImportedConfigurationTx restores a shareable model configuration with
// empty secrets inside the caller's transaction.
func WriteImportedConfigurationTx(ctx context.Context, db *storage.DB, tx *sql.Tx, instanceID string, cfg Configuration) error {
	for i := range cfg.Endpoints {
		cfg.Endpoints[i].APIKeySource = ""
		cfg.Endpoints[i].APIKeyRef = ""
		cfg.Endpoints[i].APIKeyConfigured = false
	}
	normalizeConfiguration(&cfg)
	if err := validateConfiguration(cfg); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, db.Bind(`DELETE FROM endpoint_ids WHERE instance_id = ?`), instanceID); err != nil {
		return err
	}
	for _, endpoint := range cfg.Endpoints {
		if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO endpoint_ids(id, instance_id) VALUES (?, ?)`), endpoint.ID, instanceID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, db.Bind(`DELETE FROM model_secrets WHERE instance_id = ?`), instanceID); err != nil {
		return err
	}
	return writeConfigurationTx(ctx, db, tx, instanceID, cfg)
}

func writeConfigurationTx(ctx context.Context, db *storage.DB, tx *sql.Tx, instanceID string, cfg Configuration) error {
	for _, table := range []string{"models", "model_bindings", "model_endpoints", "model_revisions"} {
		if _, err := tx.ExecContext(ctx, db.Bind(`DELETE FROM `+table+` WHERE instance_id = ?`), instanceID); err != nil {
			return err
		}
	}
	for position, endpoint := range cfg.Endpoints {
		enabled := 0
		if endpoint.Enabled {
			enabled = 1
		}
		if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO model_endpoints
			(instance_id, id, display_name, base_url, api_key_source, api_key_ref, enabled, position)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`), instanceID, endpoint.ID, endpoint.DisplayName,
			endpoint.BaseURL, endpoint.APIKeySource, endpoint.APIKeyRef, enabled, position); err != nil {
			return err
		}
	}
	for position, model := range cfg.Models {
		capabilities, err := json.Marshal(model.Capabilities)
		if err != nil {
			return err
		}
		enabled := 0
		if model.Enabled {
			enabled = 1
		}
		if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO models
			(instance_id, id, display_name, endpoint_id, upstream_model, enabled, capabilities, position)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`), instanceID, model.ID, model.DisplayName,
			model.EndpointID, model.UpstreamModel, enabled, string(capabilities), position); err != nil {
			return err
		}
	}
	for workload, binding := range cfg.GlobalBindings {
		if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO model_bindings
			(instance_id, platform, group_id, workload, mode, model_id)
			VALUES (?, '', '', ?, ?, ?)`), instanceID, workload, binding.Mode, binding.ModelID); err != nil {
			return err
		}
	}
	for _, override := range cfg.GroupOverrides {
		for workload, binding := range override.Bindings {
			if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO model_bindings
				(instance_id, platform, group_id, workload, mode, model_id)
				VALUES (?, ?, ?, ?, ?, ?)`), instanceID, override.Platform, override.GroupID,
				workload, binding.Mode, binding.ModelID); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO model_revisions(instance_id, revision)
		VALUES (?, ?)`), instanceID, cfg.Revision); err != nil {
		return err
	}
	return nil
}
