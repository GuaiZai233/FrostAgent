package llm

import (
	"FrostAgent/internal/core"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDialogueYAML_Success(t *testing.T) {
	yamlData := []byte(`
- id: 1
  scene:
  relation: 熟人
  user: |
    你不觉得草迪拉熊很神圣吗
  preferred: |
    咦惹！这啥奇怪的话，才不神圣呢！

- id: 2
  scene:
  relation: 熟人
  user: |
    老公
  preferred: |
    嗷呜...你是在叫霜降吗uwu...怎么突然这么叫霜降啦，好害羞呜……
`)

	examples, err := ParseDialogueYAML(yamlData)
	if err != nil {
		t.Fatalf("ParseDialogueYAML failed: %v", err)
	}

	if len(examples) != 2 {
		t.Fatalf("expected 2 examples, got %d", len(examples))
	}

	if strings.TrimSpace(examples[0].User) != "你不觉得草迪拉熊很神圣吗" {
		t.Errorf("unexpected example 0 user: %q", examples[0].User)
	}
	if strings.TrimSpace(examples[0].Preferred) != "咦惹！这啥奇怪的话，才不神圣呢！" {
		t.Errorf("unexpected example 0 preferred: %q", examples[0].Preferred)
	}
}

func TestFormatDialoguePrompt(t *testing.T) {
	t.Run("empty examples", func(t *testing.T) {
		prompt := FormatDialoguePrompt(nil)
		if prompt != "" {
			t.Errorf("expected empty prompt, got %q", prompt)
		}
	})

	t.Run("valid examples", func(t *testing.T) {
		examples := []DialogueExample{
			{
				ID:        1,
				User:      "你不觉得草迪拉熊很神圣吗\n",
				Preferred: "咦惹！这啥奇怪的话，才不神圣呢！\n",
			},
			{
				ID:        2,
				User:      "老公",
				Preferred: "嗷呜...你是在叫霜降吗uwu...",
			},
		}

		prompt := FormatDialoguePrompt(examples)
		if !strings.HasPrefix(prompt, DefaultDialogueInstruction) {
			t.Errorf("prompt should start with instruction, got %q", prompt)
		}

		expectedSnippet1 := "User: 你不觉得草迪拉熊很神圣吗\nAssistant: 咦惹！这啥奇怪的话，才不神圣呢！"
		expectedSnippet2 := "User: 老公\nAssistant: 嗷呜...你是在叫霜降吗uwu..."

		if !strings.Contains(prompt, expectedSnippet1) {
			t.Errorf("prompt missing snippet 1: %q", prompt)
		}
		if !strings.Contains(prompt, expectedSnippet2) {
			t.Errorf("prompt missing snippet 2: %q", prompt)
		}
	})

	t.Run("skips empty entries", func(t *testing.T) {
		examples := []DialogueExample{
			{ID: 1, User: "", Preferred: "回复"},
			{ID: 2, User: "问题", Preferred: ""},
			{ID: 3, User: "   ", Preferred: "   "},
			{ID: 4, User: "有效问题", Preferred: "有效回复"},
		}

		prompt := FormatDialoguePrompt(examples)
		if !strings.Contains(prompt, "User: 有效问题\nAssistant: 有效回复") {
			t.Errorf("prompt should contain valid example: %q", prompt)
		}
		if strings.Contains(prompt, "User: 问题") || strings.Contains(prompt, "Assistant: 回复") {
			t.Errorf("prompt should skip invalid examples: %q", prompt)
		}
	})
}

func TestLoadDialoguePrompt_ActualFile(t *testing.T) {
	// Locate eval/dialogue/dialogue.yml relative to test directory
	path := filepath.Join("..", "..", "eval", "dialogue", "dialogue.yml")
	prompt, err := LoadDialoguePrompt(path)
	if err != nil {
		t.Fatalf("LoadDialoguePrompt failed: %v", err)
	}

	if !strings.Contains(prompt, DefaultDialogueInstruction) {
		t.Errorf("prompt missing default instruction")
	}
	if !strings.Contains(prompt, "你不觉得草迪拉熊很神圣吗") {
		t.Errorf("prompt missing dialogue content: %q", prompt)
	}
	if !strings.Contains(prompt, "老公") {
		t.Errorf("prompt missing dialogue content: %q", prompt)
	}
	if !strings.Contains(prompt, "宝宝 你身上好香") {
		t.Errorf("prompt missing dialogue content: %q", prompt)
	}
	if !strings.Contains(prompt, "越来越没耐心怎么办") {
		t.Errorf("prompt missing dialogue content: %q", prompt)
	}
}

func TestLoadDialoguePrompt_NonExistentFile(t *testing.T) {
	_, err := LoadDialoguePrompt("non_existent_file.yml")
	if err == nil {
		t.Errorf("expected error for non-existent file, got nil")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected os.IsNotExist error, got %v", err)
	}
}

type mockDialogueCaptureProvider struct {
	lastMessages []ChatMessage
}

func (m *mockDialogueCaptureProvider) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	m.lastMessages = make([]ChatMessage, len(req.Messages))
	for i, msg := range req.Messages {
		m.lastMessages[i] = ChatMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		}
	}
	return &core.ChatResponse{
		Message: core.ChatMessage{
			Role:    core.RoleAssistant,
			Content: "mock reply",
		},
		Usage: &core.Usage{
			PromptTokens:     50,
			CompletionTokens: 10,
			TotalTokens:      60,
		},
	}, nil
}

func TestEngine_DialoguePromptInjection(t *testing.T) {
	t.Setenv("SYSTEM_PROMPT", "你是一个乐于助人的助手。")
	mockProv := &mockDialogueCaptureProvider{}
	engine := &Engine{
		MaxIterations:  5,
		Provider:       mockProv,
		DialoguePrompt: "以下是示例对话，请仿照句子格式、语气等回应接下来的用户输入。\n\nUser: 你好\nAssistant: 嗷呜~",
	}

	result := engine.RunMessagesWithContext([]ChatMessage{
		{Role: "user", Content: "你好呀"},
	}, RunContext{})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	if len(mockProv.lastMessages) == 0 {
		t.Fatalf("expected messages sent to provider")
	}

	sysMsg := mockProv.lastMessages[0]
	if sysMsg.Role != "system" {
		t.Errorf("expected first message to be system, got %s", sysMsg.Role)
	}

	sysContent, ok := sysMsg.Content.(string)
	if !ok {
		t.Fatalf("expected string content in system message, got %T", sysMsg.Content)
	}

	if !strings.Contains(sysContent, "你是一个乐于助人的助手。") {
		t.Errorf("system message missing base prompt: %q", sysContent)
	}
	if !strings.Contains(sysContent, "以下是示例对话，请仿照句子格式、语气等回应接下来的用户输入。") {
		t.Errorf("system message missing dialogue instruction: %q", sysContent)
	}
	if !strings.Contains(sysContent, "User: 你好\nAssistant: 嗷呜~") {
		t.Errorf("system message missing dialogue examples: %q", sysContent)
	}

	// Test Run
	mockProv.lastMessages = nil
	_ = engine.Run("测试prompt")
	if len(mockProv.lastMessages) == 0 {
		t.Fatalf("expected messages in engine.Run")
	}
	sysContentRun := mockProv.lastMessages[0].Content.(string)
	if !strings.Contains(sysContentRun, "以下是示例对话，请仿照句子格式、语气等回应接下来的用户输入。") {
		t.Errorf("engine.Run missing dialogue instruction: %q", sysContentRun)
	}

	// Test RunMessages
	mockProv.lastMessages = nil
	_ = engine.RunMessages([]ChatMessage{{Role: "user", Content: "测试消息"}})
	if len(mockProv.lastMessages) == 0 {
		t.Fatalf("expected messages in engine.RunMessages")
	}
	sysContentRunMsgs := mockProv.lastMessages[0].Content.(string)
	if !strings.Contains(sysContentRunMsgs, "以下是示例对话，请仿照句子格式、语气等回应接下来的用户输入。") {
		t.Errorf("engine.RunMessages missing dialogue instruction: %q", sysContentRunMsgs)
	}
}


