package mcp

import (
	"errors"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
)

func TestMCPFailuresUseOwningInstanceErrorReferences(t *testing.T) {
	const raw = "Post https://endpoint.invalid/private: synthetic-secret"
	store := logs.New("mcp-instance", "test", 50)
	scope := runtimescope.New(nil, nil, store)
	ctx := scope.Context()
	srv := NewServerRuntimeWithFactory(ServerConfig{ID: "synthetic-server", Enabled: true},
		createTestServerFactory("synthetic-server", map[string]func(string) (string, error){
			"fail": func(string) (string, error) { return "", errors.New(raw) },
		}))
	if err := srv.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop(); scope.Cancel() })
	for _, tc := range []struct{ args, summary, diagnostic string }{
		{"{", "MCP 工具调用失败：参数 JSON 解析失败", "unexpected end"},
		{"{}", "MCP 工具执行失败", raw},
	} {
		t.Run(tc.summary, func(t *testing.T) {
			reply, err := srv.CallTool(ctx, "fail", tc.args)
			prefix := "[FrostAgent 错误] " + tc.summary + " (Ref_ID: "
			if err != nil || !strings.HasPrefix(reply, prefix) || strings.Contains(reply, "endpoint.invalid") || strings.Contains(reply, "synthetic-secret") || strings.ContainsAny(reply, "\r\n") {
				t.Fatalf("unsafe MCP failure: %q, %v", reply, err)
			}
			id := strings.TrimSuffix(strings.TrimPrefix(reply, prefix), ")")
			entry, found := store.FindError(id)
			if !found || !strings.Contains(entry.Content, tc.diagnostic) {
				t.Fatalf("missing MCP diagnostics: %+v", entry)
			}
			if _, found := logs.General.FindError(id); found {
				t.Fatal("MCP diagnostics crossed stores")
			}
		})
	}
}
