package modelrouter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func (m *Manager) loadSQL() error {
	ctx := context.Background()
	cfg := defaultConfiguration()
	var err error
	if m.instanceID == "" {
		err = m.db.SQL.QueryRowContext(ctx, `SELECT revision FROM provider_revision WHERE id = 1`).Scan(&cfg.Revision)
	} else {
		err = m.db.SQL.QueryRowContext(ctx, m.db.Bind(`SELECT revision FROM model_revisions WHERE instance_id = ?`), m.instanceID).Scan(&cfg.Revision)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	rows, err := m.db.SQL.QueryContext(ctx, m.db.Bind(`SELECT id, display_name, base_url,
		api_key_source, api_key_ref, enabled FROM provider_endpoints ORDER BY position, id`))
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
		cfg.Endpoints = append(cfg.Endpoints, endpoint)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for i := range cfg.Endpoints {
		e := &cfg.Endpoints[i]
		if e.APIKeySource == APIKeyStorageManual {
			value, err := m.secrets.Resolve(*e)
			if err != nil {
				return err
			}
			e.APIKeyConfigured = value != ""
		} else if e.APIKeySource == APIKeyStorageWindowsCredentialManager {
			// An unused Windows provider must not prevent other instances from
			// loading on hosts without Windows Credential Manager.
			value, err := m.secrets.Resolve(*e)
			e.APIKeyConfigured = err == nil && value != ""
		} else {
			e.APIKeyConfigured = e.APIKeyRef != ""
		}
	}
	rows, err = m.db.SQL.QueryContext(ctx, m.db.Bind(`SELECT id, display_name, endpoint_id,
		upstream_model, enabled, capabilities, max_input_tokens FROM provider_models ORDER BY position, id`))
	if err != nil {
		return err
	}
	for rows.Next() {
		var model Model
		var capabilities string
		var enabled int
		if err := rows.Scan(&model.ID, &model.DisplayName, &model.EndpointID,
			&model.UpstreamModel, &enabled, &capabilities, &model.MaxInputTokens); err != nil {
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
	if err := validateStoredConfiguration(cfg); err != nil {
		return err
	}
	if err := validateSQLSources(cfg); err != nil {
		return err
	}
	m.active = cfg
	// Imports and restores update the shared catalog outside this manager. A
	// clean in-memory draft follows those changes, while pending edits retain
	// their original revision so the editor can report a conflict.
	if m.instanceID == "" && !m.draftPending && m.draftOwner == "" && !m.secrets.HasDraftChanges() {
		m.draft = cloneConfiguration(cfg)
	}
	return nil
}

func writeConfigurationTx(ctx context.Context, db *storage.DB, tx *sql.Tx, instanceID string, cfg Configuration) error {
	for _, table := range []string{"model_bindings", "model_revisions"} {
		if _, err := tx.ExecContext(ctx, db.Bind(`DELETE FROM `+table+` WHERE instance_id = ?`), instanceID); err != nil {
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
