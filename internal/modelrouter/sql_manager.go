package modelrouter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"

	"FrostAgent/internal/storage"
)

// SQL managers with an empty instance ID manage the shared catalog. Instance
// managers expose that catalog read-only and only publish their own bindings.
func (m *Manager) sqlSnapshot() (Configuration, error) {
	m.db.ModelRouterMu.RLock()
	defer m.db.ModelRouterMu.RUnlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadErr = m.loadSQL()
	return cloneConfiguration(m.active), m.loadErr
}

// Credentials and URLs are captured together; a published key change cannot
// pair a new credential with an old endpoint in a turn already in progress.
func (m *Manager) sqlRuntimeSnapshot() *Snapshot {
	m.db.ModelRouterMu.RLock()
	defer m.db.ModelRouterMu.RUnlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadErr = m.loadSQL()
	snapshot := &Snapshot{config: cloneConfiguration(m.active), err: m.loadErr, credentials: make(map[string]snapshotCredential)}
	if m.loadErr == nil {
		for _, endpoint := range m.active.Endpoints {
			value, err := m.secrets.Resolve(endpoint)
			snapshot.credentials[endpoint.ID] = snapshotCredential{value, err}
			if err != nil && endpoint.APIKeySource == APIKeyStorageManual {
				snapshot.err = err
				m.loadErr = err
			}
		}
	}
	return snapshot
}

func (m *Manager) sqlActive() Configuration { cfg, _ := m.sqlSnapshot(); return cfg }

func (m *Manager) sqlDraft() Configuration {
	m.db.ModelRouterMu.RLock()
	defer m.db.ModelRouterMu.RUnlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.instanceID != "" {
		m.loadErr = m.loadSQL()
		m.draft.Endpoints, m.draft.Models = m.active.Endpoints, m.active.Models
	}
	return cloneConfiguration(m.draft)
}

func (m *Manager) saveSQLDraft(cfg Configuration, owner string) error {
	m.db.ModelRouterMu.RLock()
	defer m.db.ModelRouterMu.RUnlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.loadSQL(); err != nil {
		return err
	}
	if err := m.checkDraftOwner(owner); err != nil {
		return err
	}
	if cfg.Revision != m.active.Revision {
		return fmt.Errorf("配置已更新，请刷新后重试")
	}
	if m.instanceID != "" {
		cfg.Endpoints, cfg.Models = m.active.Endpoints, m.active.Models
	} else {
		cfg.GlobalBindings, cfg.GroupOverrides = defaultConfiguration().GlobalBindings, nil
	}
	normalizeConfiguration(&cfg)
	if err := validateStoredConfiguration(cfg); err != nil {
		return err
	}
	if err := validateSQLSources(cfg); err != nil {
		return err
	}
	if err := PersistentRefs(cfg.Endpoints); err != nil {
		return err
	}
	m.secrets.ReconcileDraft(cfg)
	for i := range cfg.Endpoints {
		endpoint := &cfg.Endpoints[i]
		if mutation, ok := m.secrets.DraftMutation(*endpoint); ok {
			endpoint.APIKeyConfigured = !mutation.Clear
		}
	}
	m.draft = cloneConfiguration(cfg)
	if m.instanceID == "" {
		m.draftPending = !reflect.DeepEqual(cfg.Endpoints, m.active.Endpoints) ||
			!reflect.DeepEqual(cfg.Models, m.active.Models) || m.secrets.HasDraftChanges()
		if !m.draftPending {
			m.draftOwner = ""
		} else {
			m.draftOwner = owner
		}
	}
	return nil
}

func (m *Manager) discardSQLDraft(owner string) (Configuration, error) {
	m.db.ModelRouterMu.RLock()
	defer m.db.ModelRouterMu.RUnlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkDraftOwner(owner); err != nil {
		return Configuration{}, err
	}
	m.loadErr = m.loadSQL()
	m.draft = cloneConfiguration(m.active)
	m.secrets.DiscardDraft()
	m.draftOwner = ""
	m.draftPending = false
	return cloneConfiguration(m.draft), m.loadErr
}

func (m *Manager) resetSQLCatalogDraft() (Configuration, error) {
	m.db.CatalogMutationMu.Lock()
	defer m.db.CatalogMutationMu.Unlock()
	m.db.ModelRouterMu.Lock()
	defer m.db.ModelRouterMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadErr = m.loadSQL()
	if m.loadErr != nil {
		return Configuration{}, m.loadErr
	}
	if m.draftOwner != "" || m.secrets.HasDraftChanges() ||
		!reflect.DeepEqual(m.draft.Endpoints, m.active.Endpoints) ||
		!reflect.DeepEqual(m.draft.Models, m.active.Models) {
		// A reset invalidates the old editor's revision, so it cannot later
		// report a successful publication of a secret that was discarded.
		if _, err := m.db.SQL.Exec(`UPDATE provider_revision SET revision = revision + 1 WHERE id = 1`); err != nil {
			return Configuration{}, err
		}
		m.active.Revision++
	}
	m.draft = cloneConfiguration(m.active)
	m.secrets.DiscardDraft()
	m.draftOwner = ""
	m.draftPending = false
	return cloneConfiguration(m.draft), nil
}

func (m *Manager) publishSQL(owner string, expected *Configuration) (Configuration, error) {
	m.db.CatalogMutationMu.Lock()
	defer m.db.CatalogMutationMu.Unlock()
	m.db.ModelRouterMu.Lock()
	defer m.db.ModelRouterMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.loadSQL(); err != nil {
		return Configuration{}, err
	}
	if err := m.checkDraftOwner(owner); err != nil {
		return Configuration{}, err
	}
	if expected != nil {
		if err := m.checkExpectedRouteDraft(*expected); err != nil {
			return Configuration{}, err
		}
	}
	cfg := cloneConfiguration(m.draft)
	if cfg.Revision != m.active.Revision {
		return Configuration{}, fmt.Errorf("配置已更新，请刷新后重新发布")
	}
	if m.instanceID != "" {
		cfg.Endpoints, cfg.Models = m.active.Endpoints, m.active.Models
	}
	if err := validateStoredConfiguration(cfg); err != nil {
		return Configuration{}, err
	}
	cfg.Revision++
	if m.instanceID == "" {
		if err := checkCatalogReferences(m.db, cfg); err != nil {
			return Configuration{}, err
		}
	}
	// Keep other readers outside the secret/config publication boundary. External
	// credential changes are rolled back if the SQL transaction cannot commit.
	rollback := func() error { return nil }
	var err error
	if m.instanceID == "" {
		if err := m.secrets.reloadSQL(); err != nil {
			return Configuration{}, err
		}
		rollback, err = m.secrets.apply(m.active, cfg, false)
		if err != nil {
			return Configuration{}, err
		}
	}
	err = m.writeSQL(cfg)
	if err != nil {
		if undoErr := rollback(); undoErr != nil {
			return Configuration{}, fmt.Errorf("publish: %v; restore credentials: %w", err, undoErr)
		}
		return Configuration{}, err
	}
	m.active, m.draft = cloneConfiguration(cfg), cloneConfiguration(cfg)
	m.secrets.DiscardDraft()
	m.draftOwner = ""
	m.draftPending = false
	m.loadErr = nil
	return cloneConfiguration(cfg), nil
}

func (m *Manager) writeSQL(cfg Configuration) error {
	ctx := context.Background()
	tx, err := m.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if m.instanceID == "" {
		err = writeCatalogTx(ctx, m.db, tx, cfg)
	} else {
		err = writeConfigurationTx(ctx, m.db, tx, m.instanceID, cfg)
	}
	if err != nil {
		return err
	}
	if m.instanceID == "" {
		if err := m.secrets.writeSQLChangesTx(ctx, tx, m.active, cfg); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Disabling a shared provider is allowed without rewriting instance policy.
// Resolution checks the actual enabled flags before making an upstream call.
func validateStoredConfiguration(cfg Configuration) error {
	cfg = cloneConfiguration(cfg)
	for i := range cfg.Endpoints {
		cfg.Endpoints[i].Enabled = true
	}
	for i := range cfg.Models {
		cfg.Models[i].Enabled = true
	}
	return validateConfiguration(cfg)
}

func checkCatalogReferences(db *storage.DB, cfg Configuration) error {
	ids := map[string]bool{}
	for _, model := range cfg.Models {
		ids[model.ID] = true
	}
	rows, err := db.SQL.Query(`SELECT instance_id, model_id FROM model_bindings WHERE model_id <> ''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var instanceID, modelID string
		if err := rows.Scan(&instanceID, &modelID); err != nil {
			return err
		}
		if !ids[modelID] {
			return fmt.Errorf("模型 %s 仍被实例 %s 引用，请先修改该实例的路由", modelID, instanceID)
		}
	}
	return rows.Err()
}

func writeCatalogTx(ctx context.Context, db *storage.DB, tx *sql.Tx, cfg Configuration) error {
	for _, table := range []string{"provider_models", "provider_endpoints"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
			return err
		}
	}
	for pos, endpoint := range cfg.Endpoints {
		enabled := 0
		if endpoint.Enabled {
			enabled = 1
		}
		if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO provider_endpoints
   (id, display_name, base_url, api_key_source, api_key_ref, enabled, position) VALUES (?, ?, ?, ?, ?, ?, ?)`),
			endpoint.ID, endpoint.DisplayName, endpoint.BaseURL, endpoint.APIKeySource, endpoint.APIKeyRef, enabled, pos); err != nil {
			return err
		}
	}
	for pos, model := range cfg.Models {
		capabilities, err := json.Marshal(model.Capabilities)
		if err != nil {
			return err
		}
		enabled := 0
		if model.Enabled {
			enabled = 1
		}
		if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO provider_models
   (id, display_name, endpoint_id, upstream_model, enabled, capabilities, position, max_input_tokens) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`),
			model.ID, model.DisplayName, model.EndpointID, model.UpstreamModel, enabled, string(capabilities), pos, model.MaxInputTokens); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, db.Bind(`UPDATE provider_revision SET revision = ? WHERE id = 1`), cfg.Revision)
	return err
}

func (b *SecretBackend) reloadSQL() error {
	next, err := newSQLSecretBackend(b.db, "")
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.manual = next.manual
	return nil
}

func (b *SecretBackend) writeSQLChangesTx(ctx context.Context, tx *sql.Tx, previous, next Configuration) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, endpoint := range previous.Endpoints {
		if endpoint.APIKeySource == APIKeyStorageManual && !secretReferenceUsed(next, endpoint.APIKeySource, endpoint.APIKeyRef) {
			if _, err := tx.ExecContext(ctx, b.db.Bind(`DELETE FROM provider_secrets WHERE ref = ?`), endpoint.APIKeyRef); err != nil {
				return err
			}
		}
	}
	for id, mutation := range b.draft {
		endpoint, err := endpointByID(next, id)
		if err != nil || mutation.Source != APIKeyStorageManual || endpoint.APIKeySource != mutation.Source || endpoint.APIKeyRef != mutation.Ref {
			continue
		}
		if mutation.Clear {
			_, err = tx.ExecContext(ctx, b.db.Bind(`DELETE FROM provider_secrets WHERE ref = ?`), mutation.Ref)
		} else {
			_, err = tx.ExecContext(ctx, b.db.Bind(`INSERT INTO provider_secrets(ref, secret) VALUES (?, ?)
    ON CONFLICT (ref) DO UPDATE SET secret = excluded.secret`), mutation.Ref, mutation.Value)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
