package frostctl

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// Support archives deliberately contain metadata, never arbitrary log text,
// endpoint URLs, prompts, configuration values or upstream error responses.
func executeSupport(ctx context.Context, c *client, inv Invocation) (any, error) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	complete := true
	warnings := []string{}
	add := func(name string, value any) error {
		file, err := archive.Create(name)
		if err != nil {
			return err
		}
		return json.NewEncoder(file).Encode(value)
	}
	fetch := func(name, method, path string, body any) any {
		value, err := c.call(ctx, method, path, body, false)
		if err != nil {
			complete = false
			warnings = append(warnings, name+": unavailable")
			return nil
		}
		return value
	}
	if err := add("environment.json", map[string]any{
		"version": pick(fetch("version", "GET", "/api/version", nil), "version", "commit"),
		"storage": pick(fetch("storage", "GET", "/api/storage/status", nil), "driver", "connected", "schema_version", "minimum_compatible_version", "supported_schema_version", "compatible"),
	}); err != nil {
		return nil, err
	}
	instances := fetch("instances", "GET", "/api/instances", nil)
	scopes := []string{""}
	metadata := []any{}
	for _, inst := range array(instances, "instances") {
		metadata = append(metadata, pick(inst, "id", "enabled", "deleting"))
		if id := field(inst, "id"); id != "" {
			scopes = append(scopes, id)
		}
	}
	if err := add("instances.json", metadata); err != nil {
		return nil, err
	}
	for n, id := range scopes {
		prefix, scope, configScope := "", "control-plane", "global"
		query := "?check=management&check=storage"
		if id != "" {
			prefix = "/instances/" + url.PathEscape(id)
			scope = "instance"
			configScope = "instance"
			query = "?instance=" + url.QueryEscape(id)
		}
		label := fmt.Sprintf("scope-%d", n)
		config := fetch(label+" configuration", "POST", prefix+rpc("SettingsService", "ListEnvVars"), map[string]any{"scope": configScope, "redactSecrets": true})
		states := []any{}
		for _, entry := range array(config, "envVars") {
			states = append(states, pick(entry, "key", "source", "isSecret", "secretConfigured", "isInherited"))
		}
		diagnostics := fetch(label+" diagnostics", "GET", "/api/diagnostics"+query, nil)
		checks := []any{}
		for _, entry := range array(diagnostics, "checks") {
			checks = append(checks, pick(entry, "name", "status"))
		}
		if err := add(label+"/state.json", map[string]any{"instance": id, "scope": scope, "configuration": states, "checks": checks}); err != nil {
			return nil, err
		}
		entries := []any{}
		cursor := ""
		var coverage any
		seen := map[string]bool{}
		for {
			page := fetch(label+" logs", "POST", prefix+rpc("LogService", "ListLogs"), map[string]any{"scope": scope, "since": inv.Value("since"), "pagination": map[string]any{"pageSize": 500, "pageToken": cursor}})
			if page == nil {
				break
			}
			coverage = pick(object(page)["coverage"], "earliest", "latest", "persistent", "budgetBytes", "retention")
			for _, entry := range array(page, "entries") {
				entries = append(entries, pick(entry, "id", "timestamp", "level", "source", "traceId", "errorUuid"))
			}
			cursor = field(object(page)["pagination"], "pageToken")
			if cursor == "" {
				break
			}
			if seen[cursor] || len(entries) >= 100000 {
				complete = false
				warnings = append(warnings, label+": log export truncated")
				break
			}
			seen[cursor] = true
		}
		if err := add(label+"/logs.json", map[string]any{"since": inv.Value("since"), "coverage": coverage, "entries": entries}); err != nil {
			return nil, err
		}
	}
	manifest := map[string]any{"format_version": 1, "created_at": time.Now().UTC(), "since": inv.Value("since"), "complete": complete, "warnings": warnings, "content": "log metadata, configuration states, diagnostic states, and environment", "excluded": []string{"configuration values", "credentials", "endpoint URLs", "log text and payloads", "prompts", "chat and tool contents"}}
	if err := add("manifest.json", manifest); err != nil {
		return nil, err
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	if err := privateWrite(inv.Value("file"), buffer.Bytes()); err != nil {
		return nil, errors.New("cannot write support package")
	}
	result := map[string]any{"file": inv.Value("file"), "complete": complete, "warnings": warnings}
	if !complete {
		return result, errors.New("support collection is incomplete; see package manifest")
	}
	return result, nil
}

func pick(value any, keys ...string) map[string]any {
	result := map[string]any{}
	for _, key := range keys {
		if v, ok := object(value)[key]; ok {
			result[key] = v
		}
	}
	return result
}
