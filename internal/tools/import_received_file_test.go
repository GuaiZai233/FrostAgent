package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
)

func TestImportReceivedFileToolDelegatesToAdapter(t *testing.T) {
	tool := ImportReceivedFileTool()
	var got string
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{
		SessionID: "private:fixture",
		ImportReceivedFile: func(_ context.Context, ref string) llm.ReceivedFileImport {
			got = ref
			return llm.ReceivedFileImport{Ref: ref, Name: "a.docx", Size: 7, Path: "/sandbox/received/x/a.docx", Status: "ready"}
		},
	})
	out, err := tool.ExecuteContext(ctx, `{"ref":" file_0123456789ab "}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "file_0123456789ab" {
		t.Fatalf("ref not trimmed: %q", got)
	}
	var result llm.ReceivedFileImport
	if err := json.Unmarshal([]byte(out), &result); err != nil || result.Status != "ready" || result.Path == "" {
		t.Fatalf("result: %s %v", out, err)
	}
}

func TestImportReceivedFileToolRejectsInvalidInput(t *testing.T) {
	tool := ImportReceivedFileTool()
	called := false
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{
		ImportReceivedFile: func(context.Context, string) llm.ReceivedFileImport {
			called = true
			return llm.ReceivedFileImport{}
		},
	})
	for _, args := range []string{``, `{}`, `{"ref":"  "}`, `{"ref":"a","url":"https://example.invalid"}`, `{"ref":"` + strings.Repeat("a", 65) + `"}`} {
		if _, err := tool.ExecuteContext(ctx, args); err == nil {
			t.Fatalf("accepted invalid args: %s", args)
		}
	}
	if called {
		t.Fatal("adapter called for invalid args")
	}
	out, err := tool.ExecuteContext(context.Background(), `{"ref":"file_0123456789ab"}`)
	if err != nil || !strings.Contains(out, "不支持") {
		t.Fatalf("missing adapter hook: %q %v", out, err)
	}
}
