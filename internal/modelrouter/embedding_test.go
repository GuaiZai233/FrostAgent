package modelrouter

import (
	"errors"
	"testing"
	"unicode/utf8"

	"FrostAgent/internal/core"
)

func TestMemoryInputWindow(t *testing.T) {
	req := core.ChatRequest{Messages: []core.ChatMessage{{Role: core.RoleUser, Content: "中文记忆" + string(make([]byte, 1000))}}, MaxTokens: 16}
	limited, err := limitMemoryChat(req, 155)
	if err != nil {
		t.Fatal(err)
	}
	text := limited.Messages[0].Content.(string)
	if len(text) > 11 || !utf8.ValidString(text) {
		t.Fatalf("unsafe truncation: %q", text)
	}
	if req.Messages[0].Content == text {
		t.Fatal("request source changed")
	}
	if _, err := limitMemoryChat(req, 128); !errors.Is(err, ErrInputTooLong) {
		t.Fatalf("impossible window accepted: %v", err)
	}
}
func TestEmbeddingGroupCannotDisableOrFollowDialogue(t *testing.T) {
	for _, mode := range []BindingMode{BindingDisabled, BindingFollowDialogue} {
		cfg := defaultConfiguration()
		cfg.GroupOverrides = []GroupOverride{{Platform: "qq", GroupID: "synthetic-group", Bindings: map[Workload]Binding{WorkloadEmbedding: {Mode: mode}}}}
		if err := validateConfiguration(cfg); err == nil {
			t.Fatalf("embedding group allowed %s", mode)
		}
	}
}
