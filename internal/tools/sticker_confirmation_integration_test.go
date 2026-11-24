package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
)

type stickerConfirmationProvider struct {
	calls int
	chat  func(int, core.ChatRequest) core.ChatMessage
}

func (p *stickerConfirmationProvider) Chat(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	p.calls++
	return &core.ChatResponse{Message: p.chat(p.calls, req)}, nil
}

type observedStickerConfirmationResponse struct {
	Candidates []struct {
		ID          string   `json:"id"`
		Keywords    []string `json:"keywords"`
		Description string   `json:"description"`
	} `json:"candidates"`
	Hint      string `json:"hint"`
	Truncated bool   `json:"truncated"`
}

func newStickerConfirmationStore(t *testing.T, count int, description string, keywords []string) *sticker.Store {
	t.Helper()
	store, err := sticker.NewStore(filepath.Join(t.TempDir(), "stickers"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range count {
		data := []byte(fmt.Sprintf("synthetic-confirmation-sticker-%d", i))
		id := sticker.HashBytes(data)
		if err := store.Add(id, id+".png", data); err != nil {
			t.Fatal(err)
		}
		if err := store.Update(id, description, keywords); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func stickerConfirmationToolCall(id, name, args string) core.ToolCall {
	return core.ToolCall{ID: id, Type: "function", Function: core.ToolCallFunction{Name: name, Arguments: args}}
}

func stickerConfirmationToolContent(t *testing.T, req core.ChatRequest, id string) string {
	t.Helper()
	for _, message := range req.Messages {
		if message.Role == core.RoleTool && message.ToolCallID == id {
			content, ok := message.Content.(string)
			if !ok {
				t.Errorf("tool %s content has type %T, want string", id, message.Content)
			}
			return content
		}
	}
	t.Errorf("provider did not receive result for tool %s", id)
	return ""
}

func readStickerConfirmationResponse(t *testing.T, content string) observedStickerConfirmationResponse {
	t.Helper()
	if len(content) > llm.MaxToolOutputBytes {
		t.Errorf("provider received %d candidate bytes, exceeding engine budget %d", len(content), llm.MaxToolOutputBytes)
	}
	if !utf8.ValidString(content) {
		t.Error("provider received invalid UTF-8 candidate content")
	}
	var response observedStickerConfirmationResponse
	if err := json.Unmarshal([]byte(content), &response); err != nil {
		t.Errorf("provider received incomplete candidate JSON (%d bytes): %v", len(content), err)
		return response
	}
	if len(response.Candidates) == 0 {
		t.Error("provider received no candidates for matching stickers")
	}
	if !strings.Contains(response.Hint, "send_message") || !strings.Contains(response.Hint, "send_sticker(id)") {
		t.Errorf("candidate hint omits text plus confirmed-sticker instructions: %q", response.Hint)
	}
	if !strings.Contains(response.Hint, "同批") && !strings.Contains(response.Hint, "同一批") {
		t.Errorf("candidate hint does not require a shared confirmation batch: %q", response.Hint)
	}
	return response
}

func TestStickerConfirmationEngineCandidateBudget(t *testing.T) {
	for _, tc := range []struct {
		name        string
		count       int
		description string
		keywords    []string
	}{
		{
			name: "many Chinese candidates", count: 120,
			description: strings.Repeat("笑", 180), keywords: []string{"开心"},
		},
		{
			name: "escape-heavy metadata", count: 24,
			description: strings.Repeat("<&>\"\\\n\x01", 1600),
			keywords:    []string{"开心", strings.Repeat("<&>\"\\\n\x01", 800)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(sticker.ActivityEnvKey, "1")
			t.Setenv(sticker.ActivityModeEnvKey, sticker.ActivityBotTurns)
			t.Setenv(sticker.ConfirmEnvKey, "true")
			store := newStickerConfirmationStore(t, tc.count, tc.description, tc.keywords)
			tool := SendStickerTool(store)
			sends := 0
			provider := &stickerConfirmationProvider{chat: func(round int, req core.ChatRequest) core.ChatMessage {
				switch round {
				case 1:
					return core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
						stickerConfirmationToolCall("candidate-query", "send_sticker", `{"query":"开心"}`),
					}}
				case 2:
					response := readStickerConfirmationResponse(t, stickerConfirmationToolContent(t, req, "candidate-query"))
					if len(response.Candidates) >= tc.count || !response.Truncated {
						t.Errorf("large library was not marked and bounded: candidates=%d truncated=%v", len(response.Candidates), response.Truncated)
					}
					for _, candidate := range response.Candidates {
						if _, ok := store.Get(candidate.ID); !ok {
							t.Errorf("candidate ID %q does not refer to a stored sticker", candidate.ID)
						}
					}
					if sends != 0 {
						t.Errorf("query sent %d messages before any confirmation", sends)
					}
				default:
					t.Errorf("unexpected provider round %d", round)
				}
				return core.ChatMessage{Role: core.RoleAssistant, Content: "inspection complete"}
			}}
			engine := &llm.Engine{Provider: provider, MaxIterations: 2, ToolRegistry: map[string]llm.ToolExecutor{tool.Name(): tool}}
			result := engine.RunMessagesWithContext([]llm.ChatMessage{{Role: "user", Content: "find a matching sticker"}}, llm.RunContext{
				SendHook: func(string) error { sends++; return nil },
			})
			if result.Error != nil || result.Content != "inspection complete" || provider.calls != 2 || sends != 0 {
				t.Fatalf("result=%+v provider calls=%d sends=%d", result, provider.calls, sends)
			}
		})
	}
}

func TestStickerConfirmationEngineExpiredQuota(t *testing.T) {
	for _, count := range []int{2, 120} {
		t.Run(fmt.Sprintf("candidates=%d", count), func(t *testing.T) {
			testStickerConfirmationEngineExpiredQuota(t, count)
		})
	}
}

func testStickerConfirmationEngineExpiredQuota(t *testing.T, count int) {
	t.Helper()
	t.Setenv(sticker.ActivityEnvKey, "10")
	t.Setenv(sticker.ActivityModeEnvKey, sticker.ActivityBotTurns)
	t.Setenv(sticker.ConfirmEnvKey, "true")
	store := newStickerConfirmationStore(t, count, strings.Repeat("开心的合成表情", 30), []string{"开心"})
	stickerTool, messageTool := SendStickerTool(store), SendMsgTool()
	var delivered []string
	var selectedID string
	provider := &stickerConfirmationProvider{chat: func(round int, req core.ChatRequest) core.ChatMessage {
		switch round {
		case 1:
			if len(req.Tools) != 2 {
				t.Errorf("provider received %d tools, want send_message and send_sticker", len(req.Tools))
			}
			for _, tool := range req.Tools {
				for _, instruction := range []string{"send_sticker(query)", "send_sticker(id)", "send_message", "alone", "same", "batch"} {
					if !strings.Contains(tool.Description, instruction) {
						t.Errorf("%s description omits two-stage instruction %q", tool.Name, instruction)
					}
				}
			}
			foundExpiredQuota := false
			for _, message := range req.Messages {
				if content, ok := message.Content.(string); ok && strings.Contains(content, "还剩 0 个 Bot 对话轮次") {
					foundExpiredQuota = true
				}
			}
			if !foundExpiredQuota {
				t.Error("test did not enter the expired sticker-quota path")
			}
			return core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
				stickerConfirmationToolCall("candidate-query", "send_sticker", `{"query":"开心"}`),
			}}
		case 2:
			response := readStickerConfirmationResponse(t, stickerConfirmationToolContent(t, req, "candidate-query"))
			if len(delivered) != 0 {
				t.Errorf("query delivered messages before confirmation: %v", delivered)
			}
			if len(response.Candidates) == 0 {
				return core.ChatMessage{Role: core.RoleAssistant, Content: "missing candidates"}
			}
			selectedID = response.Candidates[0].ID
			return core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
				stickerConfirmationToolCall("confirmed-text", "send_message", `{"messages":[{"type":"plain","text":"一起开心一下"}]}`),
				stickerConfirmationToolCall("confirmed-sticker", "send_sticker", `{"id":"`+selectedID+`"}`),
			}}
		case 3:
			for _, id := range []string{"confirmed-text", "confirmed-sticker"} {
				if got := stickerConfirmationToolContent(t, req, id); got != "消息已发送" {
					t.Errorf("%s confirmation result = %q", id, got)
				}
			}
		default:
			t.Errorf("unexpected provider round %d after confirmed delivery", round)
		}
		return core.ChatMessage{Role: core.RoleAssistant, Content: "confirmation complete"}
	}}
	engine := &llm.Engine{Provider: provider, MaxIterations: 3, ToolRegistry: map[string]llm.ToolExecutor{
		stickerTool.Name(): stickerTool, messageTool.Name(): messageTool,
	}}
	for range 5 {
		engine.RecordBotTurn()
	}
	result := engine.RunMessagesWithContext([]llm.ChatMessage{{Role: "user", Content: "reply with text and a sticker"}}, llm.RunContext{
		SendHook: func(payload string) error { delivered = append(delivered, payload); return nil },
	})
	if result.Error != nil || result.Content != "confirmation complete" || provider.calls != 3 {
		t.Fatalf("quota not satisfied after confirmation: result=%+v provider calls=%d", result, provider.calls)
	}
	if len(delivered) != 1 {
		t.Fatalf("confirmation delivered %d chains, want one ordered pair: %v", len(delivered), delivered)
	}
	var payload struct {
		Messages []Msg `json:"messages"`
	}
	if err := json.Unmarshal([]byte(delivered[0]), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Messages) != 2 || payload.Messages[0].Type != "plain" || payload.Messages[0].Text != "一起开心一下" {
		t.Fatalf("text was lost or reordered in confirmation chain: %+v", payload.Messages)
	}
	image := payload.Messages[1]
	if image.Type != "image" || !image.IsSticker || !strings.Contains(image.Path, selectedID) || image.URL != "/api/sticker/"+selectedID+"/image" {
		t.Fatalf("confirmation did not deliver the selected sticker after text: %+v", image)
	}
}
