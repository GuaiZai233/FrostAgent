package tools

import (
	"FrostAgent/internal/llm"
	"FrostAgent/internal/memory"
	"context"
	"path/filepath"
	"testing"
)

func TestMemoryResultReportsOnlyCompletedDurableWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		mock bool
		path string
		want bool
	}{
		{name: "durable", path: "brain.json", want: true},
		{name: "mock", path: "brain.json", mock: true},
		{name: "write failure", path: "missing/brain.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := memory.NewStore(filepath.Join(t.TempDir(), tc.path))
			tool := NewMemoryTool(&llm.Engine{MemoryWriter: memory.NewWriter(store)})
			ctx := llm.WithRunContext(context.Background(), llm.RunContext{Owner: "test_owner", OwnerType: memory.OwnerUser, Mock: tc.mock})
			result, err := tool.ExecuteResult(ctx, `{"action":"write","content":"test memory"}`)
			if err != nil || result.MemoryWritten != tc.want {
				t.Fatalf("durable effect = %t, want %t; err = %v", result.MemoryWritten, tc.want, err)
			}
			entries, err := store.ListAll()
			if err != nil {
				t.Fatal(err)
			}
			wantCount := 0
			if tc.want {
				wantCount = 1
			}
			if len(entries) != wantCount {
				t.Fatalf("persisted entries = %d, want %d", len(entries), wantCount)
			}
		})
	}
}

func TestSendMessageStructuredAndLegacyAPIsHaveSameContent(t *testing.T) {
	tool := SendMsgTool()
	args := `{"messages":[{"type":"plain","text":"test message"}]}`
	result, err := tool.ExecuteResult(context.Background(), args)
	if err != nil || result.MessagePayload == "" || result.MessagePayload != result.Content {
		t.Fatalf("missing explicit message payload: %+v, %v", result, err)
	}
	legacy, err := tool.Execute(args)
	if err != nil || legacy != result.Content {
		t.Fatal("legacy execution changed content")
	}
	invalid, err := tool.ExecuteResult(context.Background(), `{"messages":[]}`)
	if err == nil || invalid.MessagePayload != "" {
		t.Fatal("invalid message requested delivery")
	}
}

func TestOrdinaryToolJSONDoesNotBecomeMessagePayload(t *testing.T) {
	tool := Tool{name: "test_tool", execute: func(string) (string, error) { return `{"messages":[{"text":"data"}]}`, nil }}
	result, err := tool.ExecuteResult(context.Background(), "{}")
	if err != nil || result.MessagePayload != "" || result.MemoryWritten {
		t.Fatalf("ordinary result was interpreted as a side effect: %+v, %v", result, err)
	}
}
