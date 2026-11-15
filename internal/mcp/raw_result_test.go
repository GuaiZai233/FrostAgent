package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	officialmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolHistoryCapturesMCPEnvelopeAndExactLargeArguments(t *testing.T) {
	server := officialmcp.NewServer(&officialmcp.Implementation{Name: "fixture-server", Version: "1"}, nil)
	server.AddTool(&officialmcp.Tool{Name: "fixture_tool", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, req *officialmcp.CallToolRequest) (*officialmcp.CallToolResult, error) {
		if !strings.Contains(string(req.Params.Arguments), "7700000000000000001") {
			t.Errorf("large argument rounded: %s", req.Params.Arguments)
		}
		return &officialmcp.CallToolResult{Content: []officialmcp.Content{&officialmcp.TextContent{Text: `{"orderIdStr":"7700000000000000001"}`}, &officialmcp.ImageContent{MIMEType: "image/png", Data: []byte{1, 2, 3}}}, StructuredContent: map[string]any{"fixture": "structured"}}, nil
	})
	factory := func(TransportConfig) (officialmcp.Transport, error) {
		s, c := officialmcp.NewInMemoryTransports()
		_, err := server.Connect(context.Background(), s, nil)
		return c, err
	}
	runtime := NewServerRuntimeWithFactory(ServerConfig{ID: "fixture-server", Name: "Fixture", Enabled: true, Transport: TransportConfig{Type: TransportStdio, Command: "fixture-server"}}, factory)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer runtime.Stop()
	var raw string
	ctx := core.WithRawToolResultReceiver(context.Background(), func(value string) { raw = value })
	body, err := runtime.CallTool(ctx, "fixture_tool", `{"orderId":7700000000000000001}`)
	if err != nil || !strings.Contains(body, "7700000000000000001") || !strings.Contains(raw, `"type":"image"`) || !strings.Contains(raw, "AQID") || !strings.Contains(raw, "structuredContent") {
		t.Fatalf("logical MCP original lost: %s %s %v", body, raw, err)
	}
}
