package frostctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	pbconnect "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
	"google.golang.org/protobuf/encoding/protojson"
)

func execute(ctx context.Context, c *client, inv Invocation, in io.Reader, out io.Writer) (any, error) {
	c.verification = verificationCommand(inv)
	prefix := instancePrefix(inv)
	switch inv.Command.Name {
	case "version":
		return c.call(ctx, "GET", "/api/version", nil, false)
	case "instance list":
		return c.call(ctx, "GET", "/api/instances", nil, false)
	case "instance show":
		data, err := c.call(ctx, "GET", "/api/instances", nil, false)
		if err != nil {
			return nil, err
		}
		for _, v := range array(data, "instances") {
			if object(v)["id"] == inv.Args[0] {
				return v, nil
			}
		}
		return nil, &commandError{Code: "not_found", Message: "instance does not exist", Status: "failed"}
	case "status":
		return c.call(ctx, "POST", prefix+rpc("BotStatusService", "GetOverview"), map[string]any{}, false)
	case "storage status":
		return c.call(ctx, "GET", "/api/storage/status", nil, false)
	case "doctor", "adapter list", "adapter show":
		checks := inv.Values["check"]
		if strings.HasPrefix(inv.Command.Name, "adapter") {
			if inv.Command.Name == "adapter show" && inv.Args[0] != "onebot" {
				return nil, errors.New("only the onebot adapter is supported")
			}
			checks = []string{"onebot"}
		}
		query := url.Values{"check": checks}
		if inv.Value("instance") != "" {
			query.Set("instance", inv.Value("instance"))
		}
		data, err := c.call(ctx, "GET", "/api/diagnostics?"+query.Encode(), nil, false)
		if err != nil && inv.Command.Name == "doctor" {
			results := []map[string]any{}
			for _, name := range checks {
				state := "unavailable"
				if name == "management" {
					state = "failed"
				}
				results = append(results, map[string]any{"name": name, "status": state, "reason": "management connection unavailable"})
			}
			return map[string]any{"checks": results}, err
		}
		return data, err
	}
	if strings.HasPrefix(inv.Command.Name, "config ") {
		return executeConfig(ctx, c, inv, in)
	}
	if strings.HasPrefix(inv.Command.Name, "logs ") {
		return executeLogs(ctx, c, inv, out)
	}
	if advancedCommand(inv.Command.Name) {
		return executeAdvanced(ctx, c, inv, in)
	}
	return nil, errors.New("unsupported command")
}

func advancedCommand(name string) bool {
	for _, prefix := range []string{"trace ", "session ", "model ", "mcp ", "security ", "billing ", "instance ", "backup ", "support "} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func readJSONFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read JSON input file")
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, errors.New("invalid JSON input file")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("JSON file contains trailing data")
	}
	return value, nil
}

func cloneJSONMap(value map[string]any) map[string]any {
	data, _ := json.Marshal(value)
	var clone map[string]any
	_ = json.Unmarshal(data, &clone)
	return clone
}

func getModelState(ctx context.Context, c *client, inv Invocation) (map[string]any, error) {
	data, err := c.call(ctx, "POST", instancePrefix(inv)+rpc("ModelRouterService", "GetState"), map[string]any{"redactCredentials": true}, false)
	if err != nil {
		return nil, err
	}
	state := object(data)
	if field(data, "loadError") != "" {
		return nil, errors.New("active model configuration unavailable")
	}
	active, ok := state["active"].(map[string]any)
	if !ok {
		return nil, errors.New("model router returned no active configuration")
	}
	return cloneJSONMap(active), nil
}

func applyModelState(ctx context.Context, c *client, inv Invocation, cfg map[string]any) (any, error) {
	return c.call(ctx, "POST", instancePrefix(inv)+rpc("ModelRouterService", "ApplyConfiguration"), map[string]any{"configuration": cfg}, true)
}

func endpointFromFile(path string) (map[string]any, error) {
	value, err := readJSONFile(path)
	if err != nil {
		return nil, err
	}
	if wrapped, ok := value["endpoint"].(map[string]any); ok {
		value = wrapped
	}
	for _, key := range []string{"api_key", "apiKey", "secret", "token"} {
		if _, ok := value[key]; ok {
			return nil, errors.New("endpoint input must not contain credentials; use endpoint secret set")
		}
	}
	return value, nil
}

func endpointList(cfg map[string]any) []any {
	if value, ok := cfg["endpoints"].([]any); ok {
		return value
	}
	return []any{}
}
func anySlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func endpointID(value any) string { return field(value, "id") }

func executeAdvanced(ctx context.Context, c *client, inv Invocation, in io.Reader) (any, error) {
	name := inv.Command.Name
	prefix := instancePrefix(inv)
	switch {
	case strings.HasPrefix(name, "trace "):
		return executeTrace(ctx, c, inv)
	case strings.HasPrefix(name, "session "):
		return executeSession(ctx, c, inv)
	case strings.HasPrefix(name, "model "):
		return executeModel(ctx, c, inv, in)
	case strings.HasPrefix(name, "mcp "):
		return executeMCP(ctx, c, inv)
	case strings.HasPrefix(name, "security audit"):
		query := url.Values{"limit": {inv.Value("limit")}}
		if inv.Value("instance") != "" {
			query.Set("instance", inv.Value("instance"))
		}
		if name == "security audit show" {
			query.Set("id", inv.Args[0])
		}
		return c.call(ctx, "GET", "/api/security/audit?"+query.Encode(), nil, false)
	case name == "billing status":
		return c.call(ctx, "GET", "/api/instances/global/billing/status", nil, false)
	case strings.HasPrefix(name, "instance "):
		return executeInstance(ctx, c, inv)
	case strings.HasPrefix(name, "backup "):
		return executeBackup(ctx, c, inv)
	case name == "support collect":
		return executeSupport(ctx, c, inv)
	default:
		_ = prefix
		return nil, errors.New("unsupported management command")
	}
}

func executeTrace(ctx context.Context, c *client, inv Invocation) (any, error) {
	query := url.Values{}
	if inv.Command.Name == "trace show" {
		query.Set("trace", inv.Args[0])
	} else {
		query.Set("trace", inv.Value("trace"))
		query.Set("error", inv.Value("error"))
	}
	return c.call(ctx, "GET", "/api/instances/"+url.PathEscape(inv.Value("instance"))+"/trace?"+query.Encode(), nil, false)
}

func executeSession(ctx context.Context, c *client, inv Invocation) (any, error) {
	path := instancePrefix(inv) + rpc("BotStatusService", "")
	switch inv.Command.Name {
	case "session list":
		request := map[string]any{"pagination": map[string]any{"pageSize": 50}}
		if inv.Value("limit") != "" {
			request["pagination"].(map[string]any)["pageSize"] = json.Number(inv.Value("limit"))
		}
		if inv.Value("cursor") != "" {
			request["pagination"].(map[string]any)["pageToken"] = inv.Value("cursor")
		}
		if inv.Value("platform") != "" {
			request["platform"] = inv.Value("platform")
		}
		return c.call(ctx, "POST", path+"GetSessions", request, false)
	case "session inspect":
		data, err := c.call(ctx, "POST", path+"GetSessionContext", map[string]any{"sessionId": inv.Args[0], "recentLimit": 50, "includePrompt": inv.Bool("include-prompt")}, false)
		if err == nil && !inv.Bool("include-prompt") {
			m := object(data)
			delete(m, "promptText")
			delete(m, "systemPrompt")
			delete(m, "actualRequestJson")
		}
		return data, err
	case "session history":
		request := map[string]any{"sessionId": inv.Args[0], "pageSize": 50}
		for _, key := range []string{"after", "before"} {
			if value := inv.Value(key); value != "" {
				request[key] = value
			}
		}
		if value := inv.Value("cursor"); value != "" {
			request["pageToken"] = value
		}
		if inv.Value("limit") != "" {
			request["pageSize"] = json.Number(inv.Value("limit"))
		}
		return c.call(ctx, "POST", path+"GetSessionHistory", request, false)
	case "session tools":
		limit := inv.Value("limit")
		if limit == "" {
			limit = "50"
		}
		return c.call(ctx, "POST", path+"ListSessionTools", map[string]any{"sessionId": inv.Args[0], "limit": json.Number(limit)}, false)
	case "session tool show":
		args, err := c.call(ctx, "POST", path+"GetToolHistoryEntry", map[string]any{"refId": inv.Args[1], "sessionId": inv.Args[0]}, false)
		if err != nil {
			return nil, err
		}
		result, err := c.call(ctx, "POST", path+"GetToolHistoryEntry", map[string]any{"refId": inv.Args[1], "sessionId": inv.Args[0], "includeResult": true, "resultOffset": numberOrZero(inv.Value("offset"))}, false)
		return map[string]any{"arguments": args, "result": result}, err
	case "session summary clear":
		return c.call(ctx, "POST", path+"DeleteGroupSummary", map[string]any{"sessionId": inv.Args[0]}, true)
	default:
		return nil, errors.New("unsupported session command")
	}
}

func numberOrZero(value string) json.Number {
	if value == "" {
		return json.Number("0")
	}
	return json.Number(value)
}

func executeModel(ctx context.Context, c *client, inv Invocation, in io.Reader) (any, error) {
	name := inv.Command.Name
	path := instancePrefix(inv) + rpc("ModelRouterService", "")
	switch name {
	case "model endpoint list", "model endpoint show":
		cfg, err := getModelState(ctx, c, inv)
		if err != nil {
			return nil, err
		}
		if name == "model endpoint list" {
			return map[string]any{"revision": cfg["revision"], "endpoints": endpointList(cfg)}, nil
		}
		for _, endpoint := range endpointList(cfg) {
			if endpointID(endpoint) == inv.Args[0] {
				return endpoint, nil
			}
		}
		return nil, errors.New("endpoint does not exist")
	case "model endpoint add", "model endpoint update":
		endpoint, err := endpointFromFile(inv.Value("file"))
		if err != nil {
			return nil, err
		}
		operation := "endpoint-add"
		id := ""
		if name == "model endpoint update" {
			operation = "endpoint-update"
			id = inv.Args[0]
		}
		return c.call(ctx, "POST", path+"ApplyConfiguration", map[string]any{"operation": operation, "endpointId": id, "endpoint": endpoint}, true)
	case "model endpoint remove":
		return c.call(ctx, "POST", path+"ApplyConfiguration", map[string]any{"operation": "endpoint-remove", "endpointId": inv.Args[0]}, true)
	case "model endpoint secret set":
		secret, err := readSecret(in)
		if err != nil {
			return nil, err
		}
		return c.call(ctx, "POST", path+"SetEndpointSecret", map[string]any{"endpointId": inv.Args[0], "apiKey": secret}, true)
	case "model endpoint secret unset":
		return c.call(ctx, "POST", path+"ClearEndpointSecret", map[string]any{"endpointId": inv.Args[0]}, true)
	case "model list":
		return c.call(ctx, "POST", path+"ListUpstreamModels", map[string]any{"endpointId": inv.Value("endpoint"), "activeOnly": true}, false)
	case "model test":
		return c.call(ctx, "POST", path+"TestModel", map[string]any{"modelId": inv.Args[0], "endpointId": inv.Value("endpoint"), "activeOnly": true}, false)
	case "model route list", "model route show":
		query := ""
		if name == "model route show" {
			query = "?workload=" + url.QueryEscape(inv.Args[0])
		}
		return c.call(ctx, "GET", "/api/instances/"+url.PathEscape(inv.Value("instance"))+"/routes"+query, nil, false)
	case "model route set", "model route unset":
		workload := "MODEL_WORKLOAD_" + strings.ToUpper(strings.ReplaceAll(inv.Args[0], "-", "_"))
		binding := map[string]any{"workload": workload}
		operation := "route-unset"
		if name == "model route set" {
			var err error
			binding, err = readJSONFile(inv.Value("file"))
			if err != nil {
				return nil, err
			}
			if v, ok := binding["workload"]; ok && v != workload {
				return nil, errors.New("binding workload conflicts with command")
			}
			binding["workload"] = workload
			operation = "route-set"
		}
		return c.call(ctx, "POST", path+"ApplyConfiguration", map[string]any{"operation": operation, "binding": binding}, true)
	}
	return nil, errors.New("unsupported model command")
}

func executeMCP(ctx context.Context, c *client, inv Invocation) (any, error) {
	path := instancePrefix(inv) + rpc("MCPService", "")
	name := inv.Command.Name
	switch name {
	case "mcp server list":
		return c.call(ctx, "POST", path+"ListMCPServers", map[string]any{"redactCredentials": true}, false)
	case "mcp server show":
		return c.call(ctx, "POST", path+"GetMCPServer", map[string]any{"id": inv.Args[0], "redactCredentials": true}, false)
	case "mcp server sync":
		return c.call(ctx, "POST", path+"SyncMCPServer", map[string]any{"id": inv.Args[0]}, true)
	case "mcp server enable", "mcp server disable":
		return c.call(ctx, "POST", path+"ToggleMCPServer", map[string]any{"id": inv.Args[0], "enabled": name == "mcp server enable"}, true)
	case "mcp server remove":
		return c.call(ctx, "POST", path+"DeleteMCPServer", map[string]any{"id": inv.Args[0]}, true)
	case "mcp tool list":
		data, err := c.call(ctx, "POST", path+"ListMCPServers", map[string]any{"redactCredentials": true}, false)
		if err != nil || inv.Value("server") == "" {
			return data, err
		}
		for _, server := range array(data, "servers") {
			if field(server, "id") == inv.Value("server") {
				return map[string]any{"server": server}, nil
			}
		}
		return nil, errors.New("MCP server does not exist")
	case "mcp tool enable", "mcp tool disable":
		return c.call(ctx, "POST", path+"ToggleMCPTool", map[string]any{"serverId": inv.Args[0], "toolName": inv.Args[1], "enabled": name == "mcp tool enable"}, true)
	case "mcp server add", "mcp server update":
		input, err := readJSONFile(inv.Value("file"))
		if err != nil {
			return nil, err
		}
		if name == "mcp server update" {
			if id, ok := input["id"]; ok && id != inv.Args[0] {
				return nil, errors.New("server ID conflicts with command")
			}
			input["id"] = inv.Args[0]
		}
		method := "AddMCPServer"
		if name == "mcp server update" {
			method = "UpdateMCPServer"
		}
		return c.call(ctx, "POST", path+method, input, true)
	default:
		return nil, errors.New("unsupported MCP command")
	}
}

func executeInstance(ctx context.Context, c *client, inv Invocation) (any, error) {
	name := inv.Command.Name
	if name == "instance create" {
		return c.call(ctx, "POST", "/api/instances", map[string]any{"name": inv.Value("name")}, true)
	}
	if name == "instance config copy" {
		return c.call(ctx, "POST", "/api/instances/"+url.PathEscape(inv.Value("instance"))+"/copy", map[string]any{"source": inv.Value("from")}, true)
	}
	base := "/api/instances/" + url.PathEscape(inv.Args[0])
	switch name {
	case "instance rename":
		return c.call(ctx, "POST", base+"/rename", map[string]any{"name": inv.Value("name")}, true)
	case "instance enable", "instance disable":
		return c.call(ctx, "POST", base+"/enable", map[string]any{"enabled": name == "instance enable"}, true)
	case "instance restart":
		return c.call(ctx, "POST", base+"/restart", map[string]any{}, true)
	case "instance delete prepare":
		return c.call(ctx, "POST", base+"/delete/prepare", map[string]any{}, true)
	case "instance delete cancel":
		return c.call(ctx, "POST", base+"/delete/cancel", map[string]any{}, true)
	case "instance delete confirm":
		archive, err := readBackup(inv.Value("backup"))
		if err != nil {
			return nil, err
		}
		verified, err := c.call(ctx, "POST", "/api/instances/verify", archive, false)
		if err != nil {
			return verified, err
		}
		result, err := c.call(ctx, "POST", base+"/delete/confirm", map[string]any{}, true)
		return map[string]any{"deletion": result, "backup_verification": verified, "ownership": "not-provable: archive has instance name but no instance ID"}, err
	}
	return nil, errors.New("unsupported instance command")
}

func download(ctx context.Context, c *client, path string) ([]byte, error) {
	response, err := c.request(ctx, "GET", path, nil, false)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (512<<20)+1))
	if err != nil || len(data) > 512<<20 {
		return nil, errors.New("cannot read management download")
	}
	return data, nil
}

func readBackup(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open backup file")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (512<<20)+1))
	if err != nil || len(data) > 512<<20 {
		return nil, errors.New("backup is unreadable or exceeds size limit")
	}
	return data, nil
}
func executeBackup(ctx context.Context, c *client, inv Invocation) (any, error) {
	if inv.Command.Name == "backup create" {
		data, err := download(ctx, c, "/api/instances/"+url.PathEscape(inv.Value("instance"))+"/backup")
		if err != nil {
			return nil, err
		}
		if err = privateWrite(inv.Value("file"), data); err != nil {
			return nil, errors.New("cannot write backup file")
		}
		return map[string]any{"file": inv.Value("file"), "bytes": len(data), "complete": true}, nil
	}
	data, err := readBackup(inv.Value("file"))
	if err != nil {
		return nil, err
	}
	if inv.Command.Name == "backup verify" {
		return c.call(ctx, "POST", "/api/instances/verify", data, false)
	}
	path := "/api/instances/restore?name=" + url.QueryEscape(inv.Value("name"))
	if inv.Bool("dry-run") {
		path += "&dry_run=true"
	}
	return c.call(ctx, "POST", path, data, !inv.Bool("dry-run"))
}

func object(v any) map[string]any    { m, _ := v.(map[string]any); return m }
func array(v any, key string) []any  { a, _ := object(v)[key].([]any); return a }
func field(v any, key string) string { s, _ := object(v)[key].(string); return s }

type configFile struct {
	Values       map[string]string `json:"values"`
	Unset        []string          `json:"unset,omitempty"`
	SecretStates map[string]bool   `json:"secret_states,omitempty"`
}

func executeConfig(ctx context.Context, c *client, inv Invocation, in io.Reader) (any, error) {
	scope := "instance"
	if inv.Value("scope") == "global" {
		scope = "global"
	}
	path := instancePrefix(inv) + rpc("SettingsService", "")
	name := strings.TrimPrefix(inv.Command.Name, "config ")
	if name == "schema" {
		data, err := c.call(ctx, "POST", path+"GetConfigSchema", map[string]any{"scope": scope}, false)
		if err != nil {
			return data, err
		}
		if len(inv.Args) == 0 {
			return data, nil
		}
		for _, f := range array(data, "fields") {
			if field(f, "key") == inv.Args[0] {
				return f, nil
			}
		}
		return nil, errors.New("unknown configuration field")
	}
	if name == "list" || name == "get" || name == "export" {
		data, err := c.call(ctx, "POST", path+"ListEnvVars", map[string]any{"scope": scope, "redactSecrets": true}, false)
		if err != nil {
			return data, err
		}
		if name == "get" {
			for _, v := range array(data, "envVars") {
				if field(v, "key") == inv.Args[0] {
					return v, nil
				}
			}
			return nil, errors.New("configuration field is outside selected scope")
		}
		if name == "list" {
			return data, nil
		}
		file := configFile{Values: map[string]string{}, SecretStates: map[string]bool{}}
		for _, v := range array(data, "envVars") {
			key := field(v, "key")
			source := field(v, "source")
			if object(v)["isSecret"] == true {
				// Secret values are intentionally omitted. Keep only an explicit
				// configured marker for human tooling; applying an export never
				// clears or replaces an existing secret.
				if source == "instance-override" || (scope == "global" && source == "global") {
					file.SecretStates[key] = object(v)["secretConfigured"] == true
				}
			} else if (scope == "global" && source == "global") || (scope != "global" && source == "instance-override") {
				// Export persisted overrides only. Effective inherited/default values
				// must not turn into instance overrides when the file is reapplied.
				file.Values[key] = field(v, "value")
			}
		}
		bytes, err := json.MarshalIndent(file, "", "  ")
		if err != nil {
			return nil, err
		}
		if err = privateWrite(inv.Value("file"), append(bytes, '\n')); err != nil {
			return nil, errors.New("cannot write configuration export")
		}
		return map[string]any{"file": inv.Value("file"), "complete": true}, nil
	}
	request := map[string]any{"scope": scope}
	switch name {
	case "set", "secret set":
		value := inv.Value("value")
		if name == "secret set" {
			var err error
			value, err = readSecret(in)
			if err != nil {
				return nil, err
			}
			request["secretInput"] = true
		}
		request["updates"] = []map[string]any{{"key": inv.Args[0], "value": value, "isSecret": name == "secret set"}}
	case "unset", "secret unset":
		request["clearKeys"] = []string{inv.Args[0]}
		request["secretInput"] = name == "secret unset"
	case "validate", "diff", "apply":
		file, err := os.Open(inv.Value("file"))
		if err != nil {
			return nil, errors.New("cannot read configuration file")
		}
		defer file.Close()
		var input configFile
		decoder := json.NewDecoder(io.LimitReader(file, 4<<20))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&input); err != nil {
			return nil, errors.New("invalid configuration file; expected values object")
		}
		if decoder.Decode(new(any)) != io.EOF {
			return nil, errors.New("configuration file contains trailing data")
		}
		keys := []string{}
		for key := range input.Values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		updates := []map[string]any{}
		for _, key := range keys {
			updates = append(updates, map[string]any{"key": key, "value": input.Values[key]})
		}
		request["updates"] = updates
		request["clearKeys"] = input.Unset
		request["dryRun"] = name != "apply" || inv.Bool("dry-run")
	}
	dry, _ := request["dryRun"].(bool)
	return c.call(ctx, "POST", path+"UpdateEnvVars", request, !dry)
}
func executeLogs(ctx context.Context, c *client, inv Invocation, out io.Writer) (any, error) {
	scope := "instance"
	if inv.Value("scope") == "control-plane" {
		scope = "control-plane"
	}
	name := strings.TrimPrefix(inv.Command.Name, "logs ")
	path := instancePrefix(inv) + rpc("LogService", "")
	req := map[string]any{"scope": scope, "pagination": map[string]any{"pageSize": 50}}
	for _, pair := range [][2]string{{"since", "since"}, {"until", "until"}, {"trace", "traceId"}, {"search", "search"}, {"source", "sourceFilter"}} {
		if value := inv.Value(pair[0]); value != "" {
			req[pair[1]] = value
		}
	}
	if level := inv.Value("level"); level != "" {
		levels := map[string]int{"debug": 1, "info": 2, "warn": 3, "error": 4}
		n, ok := levels[level]
		if !ok {
			return nil, errors.New("invalid log level")
		}
		req["minLevel"] = n
	}
	page := req["pagination"].(map[string]any)
	if inv.Value("limit") != "" {
		page["pageSize"] = json.Number(inv.Value("limit"))
	}
	if inv.Value("cursor") != "" {
		page["pageToken"] = inv.Value("cursor")
	}
	switch name {
	case "show":
		req["errorUuid"] = inv.Args[0]
		req["includePayload"] = inv.Bool("include-payload")
	case "stats":
		return c.call(ctx, "POST", path+"GetLogStats", req, false)
	case "clear":
		return c.call(ctx, "POST", path+"ClearLogs", map[string]any{"scope": scope}, true)
	}
	if name == "export" {
		return exportLogs(ctx, c, inv, path, req)
	}
	data, err := c.call(ctx, "POST", path+"ListLogs", req, false)
	if err != nil || name != "tail" || !inv.Bool("follow") {
		return data, err
	}
	return followLogs(ctx, c, inv, scope, req, data, out)
}
func exportLogs(ctx context.Context, c *client, inv Invocation, path string, req map[string]any) (any, error) {
	if _, ok := req["until"]; !ok {
		req["until"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	destination := inv.Value("file")
	partial := destination + ".partial"
	f, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, errors.New("cannot create export; remove an existing .partial file or choose another destination")
	}
	if err = privatePath(partial); err != nil {
		f.Close()
		return nil, err
	}
	result := map[string]any{"file": partial, "complete": false, "scope": req["scope"], "since": req["since"], "until": req["until"]}
	count := 0
	seen := map[string]bool{}
	for {
		data, readErr := c.call(ctx, "POST", path+"ListLogs", req, false)
		if readErr != nil {
			f.Close()
			result["entries"] = count
			return result, readErr
		}
		result["coverage"] = object(data)["coverage"]
		for _, entry := range array(data, "entries") {
			if err = json.NewEncoder(f).Encode(entry); err != nil {
				f.Close()
				result["entries"] = count
				return result, errors.New("log export write failed")
			}
			count++
		}
		cursor := field(object(data)["pagination"], "pageToken")
		if cursor == "" {
			break
		}
		if seen[cursor] {
			f.Close()
			return result, errors.New("server repeated an export cursor")
		}
		seen[cursor] = true
		req["pagination"].(map[string]any)["pageToken"] = cursor
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return result, errors.New("cannot finalize log export")
	}
	if err = replaceFile(partial, destination); err != nil {
		return result, errors.New("cannot finalize log export")
	}
	result["file"] = destination
	result["complete"] = true
	result["entries"] = count
	return result, nil
}
func followLogs(ctx context.Context, c *client, inv Invocation, scope string, req map[string]any, initial any, out io.Writer) (any, error) {
	entries := array(initial, "entries")
	seen := map[string]bool{}
	writeEntry := func(entry any) error {
		id := field(entry, "id")
		if seen[id] {
			return nil
		}
		seen[id] = true
		if inv.Value("output") == "json" {
			return nil
		}
		return json.NewEncoder(out).Encode(entry)
	}
	for _, entry := range entries {
		if err := writeEntry(entry); err != nil {
			return nil, err
		}
	}
	service := pbconnect.NewLogServiceClient(c.http, c.base+instancePrefix(inv))
	message := &v1.StreamLogsRequest{Scope: scope, Since: inv.Value("since"), Until: inv.Value("until"), TraceId: inv.Value("trace"), Search: inv.Value("search"), SourceFilter: inv.Value("source")}
	if n, ok := req["minLevel"].(int); ok {
		message.MinLevel = v1.LogLevel(n)
	}
	request := connect.NewRequest(message)
	if c.token != "" {
		request.Header().Set("Authorization", "Bearer "+c.token)
	}
	stream, err := service.StreamLogs(ctx, request)
	if err != nil {
		return nil, errors.New("log stream could not start")
	}
	defer stream.Close()
	for stream.Receive() {
		raw, _ := protojson.Marshal(stream.Msg())
		var entry any
		_ = json.Unmarshal(raw, &entry)
		if inv.Value("output") == "json" {
			entries = append(entries, entry)
		}
		if err := writeEntry(entry); err != nil {
			return nil, err
		}
	}
	result := map[string]any{"stream": "ended", "delivery": "best-effort", "coverage": object(initial)["coverage"]}
	if inv.Value("output") == "json" {
		result["entries"] = entries
	}
	if err := stream.Err(); err != nil {
		return result, fmt.Errorf("log stream ended before completion")
	}
	return result, nil
}
