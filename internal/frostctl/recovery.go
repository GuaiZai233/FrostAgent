package frostctl

import (
	"encoding/json"
	"io"
	"strings"
)

// Return argv, not shell text: resource names may contain shell metacharacters.
func verificationCommand(inv Invocation) []string {
	command := []string{"instance", "list"}
	name := inv.Command.Name
	switch {
	case strings.HasPrefix(name, "config ") || name == "instance config copy":
		command = []string{"config", "list"}
	case strings.HasPrefix(name, "model endpoint "):
		command = []string{"model", "endpoint", "list", "--scope", "global"}
	case strings.HasPrefix(name, "model route "):
		command = []string{"model", "route", "list"}
	case strings.HasPrefix(name, "mcp "):
		command = []string{"mcp", "server", "list"}
	case strings.HasPrefix(name, "session ") && len(inv.Args) > 0:
		command = []string{"session", "inspect", inv.Args[0]}
	case name == "logs clear":
		command = []string{"logs", "stats"}
	case strings.HasPrefix(name, "instance ") && inv.Command.Scope == "id":
		command = []string{"instance", "show", inv.Args[0]}
	}
	if id := inv.Value("instance"); id != "" {
		command = append(command, "--instance", id)
	}
	if scope := inv.Value("scope"); scope != "" && !strings.HasPrefix(name, "model endpoint ") {
		command = append(command, "--scope", scope)
	}
	if remote := inv.Value("remote"); remote != "" {
		command = append(command, "--remote", remote)
	}
	return append([]string{"frostctl"}, append(command, "--output", "json")...)
}

func responseCorrelation(value map[string]any) map[string]string {
	result := map[string]string{}
	for _, key := range []string{"operationId", "operation_id", "requestId", "request_id", "traceId", "trace_id", "errorUuid", "error_uuid"} {
		if v, ok := value[key].(string); ok && v != "" && len(v) <= 256 {
			result[key] = v
		}
	}
	return result
}

func (c *client) failure(code, message, status, path string, value map[string]any) *commandError {
	resource := path
	if status == "unknown" && len(c.verification) > 0 {
		resource = "Run the verification command to inspect the current resource before retrying"
	}
	return &commandError{Code: code, Message: message, Status: status, Resource: resource, Verification: c.verification, Correlation: responseCorrelation(value)}
}

func readErrorMetadata(reader io.Reader) map[string]any {
	var value map[string]any
	_ = json.NewDecoder(io.LimitReader(reader, 64<<10)).Decode(&value)
	return value
}
