package llm

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/openai"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
)

func TestDialogueErrorsExposeOnlyTypeAndLookupUUID(t *testing.T) {
	for _, tc := range []struct {
		err  error
		kind string
	}{
		{&url.Error{Op: "Post", URL: "https://endpoint.invalid/secret", Err: errors.New("synthetic-secret")}, "网络请求失败"},
		{&openai.HTTPError{StatusCode: 401, Body: "https://endpoint.invalid/secret synthetic-secret"}, "上游 HTTP 401"},
		{context.DeadlineExceeded, "请求超时"},
		{context.Canceled, "请求已取消"},
		{errors.New("synthetic-secret"), "模型调用错误"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			store := logs.New("test", "test", 50)
			engine := &Engine{Scope: runtimescope.New(nil, nil, store), MaxIterations: 1, Provider: &failedDialogueProvider{err: tc.err}}
			result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{SessionID: "synthetic-session"})
			if !result.LLMFailed || !errors.Is(result.Error, tc.err) || !strings.Contains(result.Content, tc.kind) || strings.Contains(result.Content, "synthetic-secret") || strings.Contains(result.Content, "endpoint.invalid") {
				t.Fatalf("unsafe error reply: %+v", result)
			}
			_, id, ok := strings.Cut(result.Content, "错误 UUID：")
			entry, found := store.FindError(id)
			if !ok || !found || !strings.Contains(entry.Content, tc.err.Error()) || entry.TraceID != "synthetic-session" {
				t.Fatalf("diagnostics missing: %+v", entry)
			}
		})
	}
}
