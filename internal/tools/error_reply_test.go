package tools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/actionscat"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
)

func assertToolErrorReference(t *testing.T, store *logs.Store, reply, summary, diagnostic string) {
	t.Helper()
	prefix := "[FrostAgent 错误] " + summary + " (Ref_ID: "
	if !strings.HasPrefix(reply, prefix) || !strings.HasSuffix(reply, ")") || strings.ContainsAny(reply, "\r\n") || strings.Contains(reply, "endpoint.invalid") || strings.Contains(reply, "synthetic-secret") {
		t.Fatalf("unsafe tool error: %q", reply)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(reply, prefix), ")")
	entry, found := store.FindError(id)
	if !found || !strings.Contains(entry.Content, diagnostic) || entry.TraceID != "synthetic-session" {
		t.Fatalf("missing tool diagnostics: %+v", entry)
	}
}

func TestActionsCatFailuresRetainEndpointsOnlyInOwningStore(t *testing.T) {
	const raw = "https://endpoint.invalid/private synthetic-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, raw, http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	client := actionscat.New(func(key string) string {
		switch key {
		case "ACTIONSCAT_ENDPOINT":
			return server.URL
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return "synthetic-token"
		}
		return ""
	})
	store := logs.New("tool-instance", "test", 30)
	scope := runtimescope.New(nil, nil, store)
	ctx := llm.WithRunContext(scope.Context(), llm.RunContext{SessionID: "synthetic-session"})
	for _, tc := range []struct {
		tool                      Tool
		args, summary, diagnostic string
	}{
		{ActionsCatListActionsTool(client), "{}", "获取 ActionsCat actions 列表失败", raw},
		{ActionsCatRunActionTool(client), `{"action_id":"synthetic-action","wait":false}`, "触发 ActionsCat Action 失败", raw},
		{ActionsCatGetRunTool(client), `{"action_id":"synthetic-action","run_id":"synthetic-run"}`, "查询 Run 状态失败", raw},
		{ActionsCatGetRunTool(client), "{", "参数解析错误", "unexpected end"},
	} {
		t.Run(tc.summary, func(t *testing.T) {
			reply, err := tc.tool.ExecuteContext(ctx, tc.args)
			if err != nil {
				t.Fatal(err)
			}
			assertToolErrorReference(t, store, reply, tc.summary, tc.diagnostic)
		})
	}
}

func TestImageRuntimeFailuresRetainEndpointsOnlyInOwningStore(t *testing.T) {
	const raw = "Post https://endpoint.invalid/private: synthetic-secret"
	for _, loading := range []bool{true, false} {
		summary := "图片识别失败：视觉模型调用失败"
		if loading {
			summary = "图片读取失败"
		}
		t.Run(summary, func(t *testing.T) {
			store := logs.New("image-instance", "test", 30)
			engine := &llm.Engine{Scope: runtimescope.New(nil, nil, store), VisionProvider: &inspectImageProviderStub{err: errors.New(raw)}}
			run := llm.RunContext{SessionID: "synthetic-session", LoadObservedImage: func(context.Context, string, int) ([]byte, error) {
				if loading {
					return nil, errors.New(raw)
				}
				return testPNGImage, nil
			}}
			reply, err := runInspectMessageImage(t, engine, run, `{"message_id":"synthetic-message"}`)
			if err != nil {
				t.Fatal(err)
			}
			assertToolErrorReference(t, store, reply, summary, raw)
		})
	}
}
