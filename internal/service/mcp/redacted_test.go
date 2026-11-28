package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
)

func TestRedactedMCPTransportDoesNotExposeCredentials(t *testing.T) {
	info := redactServerInfo(&v1.MCPServerInfo{Url: "https://synthetic:secret@mcp.invalid/sse?key=synthetic-secret#secret", Args: []string{"--api-key", "synthetic-secret"}, Command: "synthetic-secret", Env: map[string]string{"INNOCENT": "synthetic-secret"}, Headers: map[string]string{"X-Custom": "synthetic-secret"}, LastError: "failed https://mcp.invalid/?key=synthetic-secret"}, true)
	data, _ := json.Marshal(info)
	if strings.Contains(string(data), "synthetic-secret") || info.Url != "https://mcp.invalid/sse" || !info.LastErrorPresent || !info.TransportDetailsConfigured {
		t.Fatalf("redaction failed: %s", data)
	}
}
