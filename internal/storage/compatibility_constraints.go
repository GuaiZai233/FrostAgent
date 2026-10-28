package storage

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

// These keys are part of the writer contract: UPSERT conflict targets and
// cascading deletes must keep their v14 meaning, not merely their column names.
var compatiblePrimaryKeys = map[string]string{
	"access_records":        "principal_key",
	"counters":              "key",
	"dialogues":             "instance_id,position",
	"group_members":         "instance_id,platform,group_id,user_id",
	"group_profiles":        "instance_id,platform,group_id",
	"group_summaries":       "instance_id,platform,group_id",
	"instances":             "id",
	"mcp_servers":           "instance_id,id",
	"mcp_tool_policies":     "instance_id,server_id,tool_name",
	"memory_catalogs":       "instance_id,scope_type,platform,group_id,owner",
	"memory_entries":        "instance_id,scope_type,platform,group_id,id",
	"memory_merge_archives": "instance_id,scope_type,platform,group_id,merged_id",
	"memory_tags":           "instance_id,scope_type,platform,group_id,memory_id,tag",
	"model_bindings":        "instance_id,platform,group_id,workload",
	"model_revisions":       "instance_id",
	"pending_deletions":     "instance_id",
	"provider_endpoints":    "id",
	"provider_models":       "id",
	"provider_revision":     "id",
	"provider_secrets":      "ref",
	"security_audit":        "id",
	"settings":              "scope,instance_id,key",
	"skills":                "instance_id,name",
	"sticker_entries":       "instance_id,id",
	"sticker_keywords":      "instance_id,sticker_id,position",
}

func compatibleForeignKeys(table string) []schemaForeignKey {
	var key schemaForeignKey
	switch table {
	case "dialogues", "group_profiles", "group_summaries", "mcp_servers", "memory_catalogs",
		"memory_entries", "memory_merge_archives", "model_bindings", "model_revisions",
		"pending_deletions", "skills", "sticker_entries":
		key = schemaForeignKey{"instance_id", "instances", "id", "CASCADE", "NO ACTION"}
	case "group_members":
		key = schemaForeignKey{"instance_id,platform,group_id", "group_profiles", "instance_id,platform,group_id", "CASCADE", "NO ACTION"}
	case "mcp_tool_policies":
		key = schemaForeignKey{"instance_id,server_id", "mcp_servers", "instance_id,id", "CASCADE", "NO ACTION"}
	case "memory_tags":
		key = schemaForeignKey{"instance_id,scope_type,platform,group_id,memory_id", "memory_entries", "instance_id,scope_type,platform,group_id,id", "CASCADE", "NO ACTION"}
	case "sticker_keywords":
		key = schemaForeignKey{"instance_id,sticker_id", "sticker_entries", "instance_id,id", "CASCADE", "NO ACTION"}
	case "provider_models":
		key = schemaForeignKey{"endpoint_id", "provider_endpoints", "id", "NO ACTION", "NO ACTION"}
	default:
		return nil
	}
	return []schemaForeignKey{key}
}

func (d *DB) validateCoreConstraints(ctx context.Context, q schemaReader, table string, columns []schemaColumn) error {
	primary := strings.Split(compatiblePrimaryKeys[table], ",")
	for _, column := range columns {
		position := slices.Index(primary, column.name) + 1
		notNull := true
		if d.Backend == SQLite && len(primary) == 1 && position == 1 {
			// SQLite's single-column PRIMARY KEY is reported separately from NOT NULL.
			notNull = false
		}
		if column.primary != position || column.notNull != notNull {
			return fmt.Errorf("table %s has unrecognized primary key or nullability", table)
		}
	}
	keys, err := d.tableForeignKeys(ctx, q, table)
	if err != nil {
		return err
	}
	if !slices.Equal(keys, compatibleForeignKeys(table)) {
		return fmt.Errorf("table %s has unrecognized foreign keys", table)
	}
	unique, err := d.tableUniqueKeys(ctx, q, table)
	if err != nil {
		return err
	}
	var expected []string
	if table == "instances" {
		expected = []string{"name"}
	}
	if !slices.Equal(unique, expected) {
		return fmt.Errorf("table %s has unrecognized unique constraints", table)
	}
	hasCheck, err := d.tableHasCheckConstraints(ctx, q, table)
	if err != nil {
		return err
	}
	// No v14 core table has CHECK constraints. Even an additive constraint can
	// reject values the old writer emits, so it cannot be assumed compatible.
	if hasCheck {
		return fmt.Errorf("table %s has unrecognized CHECK constraints", table)
	}
	return nil
}

func (d *DB) tableHasCheckConstraints(ctx context.Context, q schemaReader, table string) (bool, error) {
	if d.Backend == Postgres {
		var hasCheck bool
		err := q.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_catalog.pg_constraint
			WHERE conrelid = pg_catalog.to_regclass($1) AND contype = 'c'
		)`, table).Scan(&hasCheck)
		return hasCheck, err
	}
	// SQLite exposes CHECK clauses in the stored CREATE TABLE statement, not
	// table_info/index_list. Inspect tokens, not text inside quotes/comments.
	var statement string
	if err := q.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
		WHERE type = 'table' AND name = ? COLLATE NOCASE`, table).Scan(&statement); err != nil {
		return false, err
	}
	return containsSQLiteCheckConstraint(statement), nil
}

func containsSQLiteCheckConstraint(statement string) bool {
	for i := 0; i < len(statement); {
		switch {
		case strings.HasPrefix(statement[i:], "--"):
			if end := strings.IndexByte(statement[i+2:], '\n'); end >= 0 {
				i += end + 3
			} else {
				return false
			}
		case strings.HasPrefix(statement[i:], "/*"):
			if end := strings.Index(statement[i+2:], "*/"); end >= 0 {
				i += end + 4
			} else {
				return false
			}
		case statement[i] == '\'' || statement[i] == '"' || statement[i] == '`':
			quote := statement[i]
			i++
			for i < len(statement) {
				if statement[i] != quote {
					i++
					continue
				}
				i++
				if i < len(statement) && statement[i] == quote {
					i++ // Escaped quote inside a string or identifier.
					continue
				}
				break
			}
		case statement[i] == '[':
			if end := strings.IndexByte(statement[i+1:], ']'); end >= 0 {
				i += end + 2
			} else {
				return false
			}
		case sqliteIdentifierByte(statement[i]):
			start := i
			for i < len(statement) && sqliteIdentifierByte(statement[i]) {
				i++
			}
			if strings.EqualFold(statement[start:i], "CHECK") {
				return true
			}
		default:
			i++
		}
	}
	return false
}

func sqliteIdentifierByte(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
		char >= '0' && char <= '9' || char == '_' || char == '$' || char >= 0x80
}

func (d *DB) tableUniqueKeys(ctx context.Context, q schemaReader, table string) ([]string, error) {
	query := `SELECT i.name, c.name, i.partial FROM pragma_index_list(?) i
		JOIN pragma_index_info(i.name) c WHERE i."unique" = 1 AND i.origin <> 'pk' ORDER BY i.name, c.seqno`
	if d.Backend == Postgres {
		query = `SELECT i.indexrelid::text, a.attname, i.indpred IS NOT NULL
			FROM pg_catalog.pg_index i CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY k(attnum, position)
			LEFT JOIN pg_catalog.pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
			WHERE i.indrelid = pg_catalog.to_regclass($1) AND i.indisunique AND NOT i.indisprimary
			AND k.position <= i.indnkeyatts ORDER BY i.indexrelid, k.position`
	}
	rows, err := q.QueryContext(ctx, query, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	var previous string
	for rows.Next() {
		var id string
		var column sql.NullString
		var partial bool
		if err := rows.Scan(&id, &column, &partial); err != nil {
			return nil, err
		}
		if partial || !column.Valid {
			return nil, fmt.Errorf("table %s has an unrecognized unique expression or predicate", table)
		}
		if id == previous {
			keys[len(keys)-1] += "," + column.String
		} else {
			keys = append(keys, column.String)
		}
		previous = id
	}
	slices.Sort(keys)
	return keys, rows.Err()
}
