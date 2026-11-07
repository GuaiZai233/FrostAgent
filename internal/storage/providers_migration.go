package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

var providerSchema = []string{
	`CREATE TABLE provider_endpoints (id TEXT PRIMARY KEY, display_name TEXT NOT NULL, base_url TEXT NOT NULL,
  api_key_source TEXT NOT NULL, api_key_ref TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL, position INTEGER NOT NULL)`,
	`CREATE TABLE provider_models (id TEXT PRIMARY KEY, display_name TEXT NOT NULL,
  endpoint_id TEXT NOT NULL REFERENCES provider_endpoints(id), upstream_model TEXT NOT NULL,
  enabled INTEGER NOT NULL, capabilities TEXT NOT NULL, position INTEGER NOT NULL)`,
	`CREATE TABLE provider_secrets (ref TEXT PRIMARY KEY, secret TEXT NOT NULL)`,
	`CREATE TABLE provider_revision (id INTEGER PRIMARY KEY, revision BIGINT NOT NULL)`,
	`INSERT INTO provider_revision(id, revision) VALUES (1, 1)`,
}

// migrateProviders copies into the shared catalog in the schema transaction.
// Old tables remain as inactive migration archives; no source rows are deleted.
func (d *DB) migrateProviders(ctx context.Context, tx *sql.Tx) error {
	endpoints, err := migrationRows(ctx, tx, `SELECT instance_id, id, display_name, base_url, api_key_source, api_key_ref, enabled, position FROM model_endpoints ORDER BY instance_id, id`, 8)
	if err != nil {
		return err
	}
	models, err := migrationRows(ctx, tx, `SELECT instance_id, id, display_name, endpoint_id, upstream_model, enabled, capabilities, position FROM models ORDER BY instance_id, id`, 8)
	if err != nil {
		return err
	}
	secrets, err := migrationRows(ctx, tx, `SELECT instance_id, ref, secret FROM model_secrets ORDER BY instance_id, ref`, 3)
	if err != nil {
		return err
	}
	bindings, err := migrationRows(ctx, tx, `SELECT instance_id, platform, group_id, workload, model_id FROM model_bindings WHERE model_id <> ''`, 5)
	if err != nil {
		return err
	}
	endpointMap, modelMap := map[[2]string]string{}, map[[2]string]string{}
	secretCopies, secretValues := map[string]string{}, map[[2]string]string{}
	consumed := map[[2]string]bool{}
	reservedRefs := map[string]bool{}
	for _, row := range secrets {
		secretValues[[2]string{migrationText(row[0]), migrationText(row[1])}] = migrationText(row[2])
	}
	endpointIDs, modelIDs := migrationIDs(endpoints), migrationIDs(models)
	seenEndpoints, seenModels := map[string]bool{}, map[string]bool{}
	endpointNames, modelNames := map[string]bool{}, map[string]bool{}
	for _, row := range endpoints {
		owner, oldID := migrationText(row[0]), migrationText(row[1])
		id := oldID
		if seenEndpoints[id] {
			id = migrationUnique("migrated-"+owner+"-"+id, endpointIDs, false)
		}
		seenEndpoints[oldID] = true
		endpointMap[[2]string{owner, oldID}] = id
		source, ref := migrationText(row[4]), migrationText(row[5])
		if source == "windows_credential_manager" && id != oldID {
			return fmt.Errorf("provider ID %s has conflicting Windows credentials; schema was not changed", oldID)
		}
		if source == "manual" || source == "" {
			key := [2]string{owner, ref}
			ref = "endpoint/" + id
			reservedRefs[ref] = true
			if value, ok := secretValues[key]; ok {
				secretCopies[ref] = value
				consumed[key] = true
			}
		}
		name := migrationUnique(migrationText(row[2]), endpointNames, true)
		if _, err := tx.ExecContext(ctx, d.Bind(`INSERT INTO provider_endpoints
   (id, display_name, base_url, api_key_source, api_key_ref, enabled, position) VALUES (?, ?, ?, ?, ?, ?, ?)`),
			id, name, row[3], source, ref, row[6], row[7]); err != nil {
			return err
		}
	}
	for _, row := range models {
		owner, oldID := migrationText(row[0]), migrationText(row[1])
		id := oldID
		if seenModels[id] {
			id = migrationUnique("migrated-"+owner+"-"+id, modelIDs, false)
		}
		seenModels[oldID] = true
		modelMap[[2]string{owner, oldID}] = id
		endpointID, ok := endpointMap[[2]string{owner, migrationText(row[3])}]
		if !ok {
			return fmt.Errorf("model %s references a missing provider; schema was not changed", oldID)
		}
		name := migrationUnique(migrationText(row[2]), modelNames, true)
		if _, err := tx.ExecContext(ctx, d.Bind(`INSERT INTO provider_models
   (id, display_name, endpoint_id, upstream_model, enabled, capabilities, position) VALUES (?, ?, ?, ?, ?, ?, ?)`),
			id, name, endpointID, row[4], row[5], row[6], row[7]); err != nil {
			return err
		}
	}
	refs := reservedRefs
	for ref := range secretCopies {
		refs[ref] = true
	}
	for _, row := range secrets {
		owner, ref := migrationText(row[0]), migrationText(row[1])
		if consumed[[2]string{owner, ref}] {
			continue
		}
		secretCopies[migrationUnique(ref, refs, false)] = migrationText(row[2])
	}
	for ref, value := range secretCopies {
		if _, err := tx.ExecContext(ctx, d.Bind(`INSERT INTO provider_secrets(ref, secret) VALUES (?, ?)`), ref, value); err != nil {
			return err
		}
		var copied string
		if err := tx.QueryRowContext(ctx, d.Bind(`SELECT secret FROM provider_secrets WHERE ref = ?`), ref).Scan(&copied); err != nil {
			return err
		}
		if copied != value {
			return fmt.Errorf("provider credential copy verification failed")
		}
	}
	for _, row := range bindings {
		modelID, ok := modelMap[[2]string{migrationText(row[0]), migrationText(row[4])}]
		if !ok {
			return fmt.Errorf("instance %s references missing model %s; schema was not changed", row[0], row[4])
		}
		result, err := tx.ExecContext(ctx, d.Bind(`UPDATE model_bindings SET model_id = ? WHERE instance_id = ? AND platform = ? AND group_id = ? AND workload = ?`), modelID, row[0], row[1], row[2], row[3])
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return fmt.Errorf("model binding copy verification failed")
		}
	}
	for _, check := range []struct {
		table string
		want  int
	}{{"provider_endpoints", len(endpoints)}, {"provider_models", len(models)}, {"provider_secrets", len(secretCopies)}} {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+check.table).Scan(&count); err != nil {
			return err
		}
		if count != check.want {
			return fmt.Errorf("%s migration row count mismatch", check.table)
		}
	}
	return nil
}

func migrationRows(ctx context.Context, tx *sql.Tx, query string, columns int) ([][]any, error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result [][]any
	for rows.Next() {
		row, dest := make([]any, columns), make([]any, columns)
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		for i, value := range row {
			if data, ok := value.([]byte); ok {
				row[i] = string(data)
			}
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
func migrationText(value any) string { return fmt.Sprint(value) }
func migrationIDs(rows [][]any) map[string]bool {
	ids := map[string]bool{}
	for _, row := range rows {
		ids[migrationText(row[1])] = true
	}
	return ids
}
func migrationUnique(base string, used map[string]bool, fold bool) string {
	key := func(s string) string {
		if fold {
			return strings.ToLower(strings.TrimSpace(s))
		}
		return s
	}
	value := base
	for suffix := 2; used[key(value)]; suffix++ {
		value = fmt.Sprintf("%s-%d", base, suffix)
	}
	used[key(value)] = true
	return value
}
