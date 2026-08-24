package llm

import (
	"FrostAgent/internal/core"
	"context"
	"testing"
)

type silentMarkerProvider struct {
	content string
}

func (p *silentMarkerProvider) Chat(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
	return &core.ChatResponse{
		Message: core.ChatMessage{
			Role:    core.RoleAssistant,
			Content: p.content,
		},
	}, nil
}

func TestRunLoopTreatsStandaloneAssistantSilentMarkerAsSilence(t *testing.T) {
	for _, content := range []string{
		AssistantSilentMarker,
		"\n  " + AssistantSilentMarker + " \t",
	} {
		engine := &Engine{
			MaxIterations: 1,
			ToolRegistry:  map[string]ToolExecutor{},
			Provider:      &silentMarkerProvider{content: content},
		}

		result := engine.RunMessagesWithContext([]ChatMessage{{
			Role:    "user",
			Content: "无需回复",
		}}, RunContext{})

		if !result.Silent {
			t.Fatalf("expected %q to be treated as silence", content)
		}
		if result.Content != "" {
			t.Fatalf("expected silent result content to be empty, got %q", result.Content)
		}
	}
}

func TestRunLoopPreservesEmbeddedAssistantSilentMarker(t *testing.T) {
	content := "不要把 " + AssistantSilentMarker + " 当作普通回复发送"
	engine := &Engine{
		MaxIterations: 1,
		ToolRegistry:  map[string]ToolExecutor{},
		Provider:      &silentMarkerProvider{content: content},
	}

	result := engine.RunMessagesWithContext([]ChatMessage{{
		Role:    "user",
		Content: "这个内部标记是什么？",
	}}, RunContext{})

	if result.Silent {
		t.Fatal("expected embedded marker to remain visible content")
	}
	if result.Content != content {
		t.Fatalf("expected content %q, got %q", content, result.Content)
	}
}
