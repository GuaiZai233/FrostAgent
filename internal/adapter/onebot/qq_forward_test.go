package onebot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/qqextend"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

func TestQQReadForwardFormatsAndMediaPlaceholders(t *testing.T) {
	for _, tc := range []struct{ name, source, response, text string }{
		{"napcat", `[{"type":"forward","data":{"id":"resource-root"}}]`, `{"messages":[{"message_id":999,"sender":{"user_id":42,"nickname":"fixture"},"time":123,"message":[{"type":"text","data":{"text":"hello"}},{"type":"image","data":{"url":"https://example.invalid/private-media","base64":"secret-media"}}]}]}`, "hello[image]"},
		{"standard", `[{"type":"forward","data":{"id":"resource-root"}}]`, `{"message":[{"type":"node","data":{"uin":"42","name":"fixture","time":"123","content":[{"type":"text","data":{"text":"hello"}},{"type":"file","data":{"file":"/private-media"}}]}}]}`, "hello[file]"},
		{"cq", `"[CQ:forward,id=resource-root]"`, `{"messages":[{"sender":{"user_id":"42","nickname":"fixture"},"time":123,"content":"hello[CQ:record,file=secret-media]"}]}`, "hello[record]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			result, err := readQQForwardPage(json.RawMessage(tc.source), tools.QQArguments{MessageID: "101"}, func(action model.OneBotAction) (oneBotAPIResponse, error) {
				calls++
				params := action.Params.(map[string]any)
				if action.Action != "get_forward_msg" || params["id"] != "resource-root" || params["message_id"] != "resource-root" {
					t.Errorf("untrusted or incorrect resource: %+v", action)
				}
				return oneBotAPIResponse{Status: "ok", Data: json.RawMessage(tc.response)}, nil
			})
			if err != nil || calls != 1 || result.MessagesSent != 0 || result.ExternalMessagesSent != 0 {
				t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
			}
			var page struct{ Nodes []qqReadForwardNode }
			if err := json.Unmarshal([]byte(result.Content), &page); err != nil || len(page.Nodes) != 1 {
				t.Fatalf("invalid page: %s %v", result.Content, err)
			}
			node := page.Nodes[0]
			if node.Text != tc.text || node.Sender.UserID != "42" || node.Sender.Nickname != "fixture" || node.Time != 123 {
				t.Fatalf("node=%+v", node)
			}
			for _, secret := range []string{"secret-media", "private-media", "resource-root", "999"} {
				if strings.Contains(result.Content, secret) {
					t.Fatalf("raw metadata leaked: %s", result.Content)
				}
			}
		})
	}
}

func TestQQReadForwardNestedPathAndInlineContent(t *testing.T) {
	source := json.RawMessage(`[{"type":"text","data":{"text":"ignored"}},{"type":"forward","data":{"id":"unused"}},{"type":"forward","data":{"id":"root"}}]`)
	var calls []string
	call := func(action model.OneBotAction) (oneBotAPIResponse, error) {
		id := action.Params.(map[string]any)["id"].(string)
		calls = append(calls, id)
		if id != "root" {
			t.Fatalf("unexpected resource %q", id)
		}
		return oneBotAPIResponse{Status: "ok", Data: json.RawMessage(`{"messages":[{"content":"first"},{"content":[{"type":"forward","data":{"content":[{"type":"node","data":{"name":"nested fixture","content":"nested text"}}]}}]}]}`)}, nil
	}
	args := tools.QQArguments{MessageID: "101", ForwardIndex: 1}
	root, err := readQQForwardPage(source, args, call)
	if err != nil || !strings.Contains(root.Content, `"forward_count":1`) || len(calls) != 1 {
		t.Fatalf("nested card fetched eagerly: %s %v %v", root.Content, calls, err)
	}
	args.Path = []tools.QQForwardStep{{NodeIndex: 1}}
	result, err := readQQForwardPage(source, args, call)
	if err != nil || !strings.Contains(result.Content, "nested text") || !reflect.DeepEqual(calls, []string{"root", "root"}) {
		t.Fatalf("inline nested result=%+v calls=%v err=%v", result, calls, err)
	}
	args.Path[0].NodeIndex = 9
	if _, err := readQQForwardPage(source, args, call); err == nil {
		t.Fatal("out-of-range path accepted")
	}
	// CQ escaping is decoded only after tokenization; a quoted CQ example is not a card.
	if len(qqForwardReferences(json.RawMessage(`"&#91;CQ:forward,id=untrusted&#93;"`))) != 0 {
		t.Fatal("escaped user text became a forward reference")
	}
}

func TestQQReadForwardRemoteNestedCard(t *testing.T) {
	source := json.RawMessage(`[{"type":"forward","data":{"id":"root"}}]`)
	var calls []string
	result, err := readQQForwardPage(source, tools.QQArguments{MessageID: "101", Path: []tools.QQForwardStep{{NodeIndex: 0}}}, func(action model.OneBotAction) (oneBotAPIResponse, error) {
		id := action.Params.(map[string]any)["id"].(string)
		calls = append(calls, id)
		data := `{"message":[{"type":"node","data":{"content":"nested result"}}]}`
		if id == "root" {
			data = `{"messages":[{"content":"[CQ:forward,id=child&#44;resource]"}]}`
		}
		return oneBotAPIResponse{Status: "ok", Data: json.RawMessage(data)}, nil
	})
	if err != nil || !reflect.DeepEqual(calls, []string{"root", "child,resource"}) || !strings.Contains(result.Content, "nested result") {
		t.Fatalf("result=%+v calls=%v err=%v", result, calls, err)
	}
}

func TestQQReadForwardPaginationBoundsAndMalformedResponses(t *testing.T) {
	source := json.RawMessage(`[{"type":"forward","data":{"id":"root"}}]`)
	entries := make([]map[string]any, 60)
	for i := range entries {
		entries[i] = map[string]any{"content": strings.Repeat("咪\x00", 1500)}
	}
	data, _ := json.Marshal(map[string]any{"messages": entries})
	call := func(model.OneBotAction) (oneBotAPIResponse, error) {
		return oneBotAPIResponse{Status: "ok", Data: data}, nil
	}
	offset, pages := 0, 0
	for {
		result, err := readQQForwardPage(source, tools.QQArguments{MessageID: "101", Offset: offset, Limit: 50}, call)
		if err != nil || len(result.Content) > maxQQForwardPage || !utf8.ValidString(result.Content) {
			t.Fatalf("page bytes=%d err=%v", len(result.Content), err)
		}
		var page struct {
			Nodes      []qqReadForwardNode
			Total      int
			HasMore    bool `json:"has_more"`
			NextOffset int  `json:"next_offset"`
		}
		_ = json.Unmarshal([]byte(result.Content), &page)
		if page.Total != 60 || len(page.Nodes) == 0 || page.NextOffset != offset+len(page.Nodes) {
			t.Fatalf("pagination stalled: %s", result.Content)
		}
		for i, node := range page.Nodes {
			if node.Index != offset+i || !node.TextTruncated || utf8.RuneCountInString(node.Text) != maxQQForwardText {
				t.Fatalf("wrong record index or clipping: %+v", node)
			}
		}
		offset, pages = page.NextOffset, pages+1
		if !page.HasMore {
			break
		}
	}
	if offset != 60 || pages < 2 {
		t.Fatalf("offset=%d pages=%d", offset, pages)
	}
	if _, err := readQQForwardPage(source, tools.QQArguments{Offset: 61}, call); err == nil {
		t.Fatal("offset out of range accepted")
	}
	for _, invalid := range []string{`{}`, `{"messages":null}`, `{"messages":{}}`, `{"messages":[null]}`, `{"messages":[{"content":{}}]}`, `{"messages":[{"content":[1]}]}`, `{"messages":[{"content":[{}]}]}`, `{"messages":[{"type":"node","data":{"id":"arbitrary-message"}}]}`} {
		_, err := readQQForwardPage(source, tools.QQArguments{}, func(model.OneBotAction) (oneBotAPIResponse, error) {
			return oneBotAPIResponse{Data: json.RawMessage(invalid)}, nil
		})
		if err == nil {
			t.Errorf("invalid response accepted: %s", invalid)
		}
	}
	result, err := readQQForwardPage(source, tools.QQArguments{}, func(model.OneBotAction) (oneBotAPIResponse, error) {
		return oneBotAPIResponse{Data: json.RawMessage(`{"messages":[]}`)}, nil
	})
	if err != nil || !strings.Contains(result.Content, `"nodes":[]`) || !strings.Contains(result.Content, `"has_more":false`) {
		t.Fatalf("empty record: %+v %v", result, err)
	}
}

func TestQQReadForwardDefaultLimitAndLastPage(t *testing.T) {
	entries := make([]map[string]any, 60)
	for i := range entries {
		entries[i] = map[string]any{"message": fmt.Sprintf("record %d", i)}
	}
	data, _ := json.Marshal(map[string]any{"messages": entries})
	call := func(model.OneBotAction) (oneBotAPIResponse, error) {
		return oneBotAPIResponse{Status: "ok", Data: data}, nil
	}
	for _, tc := range []struct {
		offset, limit, count, next int
		more                       bool
	}{{0, 0, 20, 20, true}, {0, 50, 50, 50, true}, {50, 20, 10, 60, false}, {60, 20, 0, 60, false}} {
		result, err := readQQForwardPage(json.RawMessage(`[{"type":"forward","data":{"id":"root"}}]`), tools.QQArguments{MessageID: "101", Offset: tc.offset, Limit: tc.limit}, call)
		var page struct {
			Nodes      []qqReadForwardNode
			HasMore    bool `json:"has_more"`
			NextOffset int  `json:"next_offset"`
		}
		_ = json.Unmarshal([]byte(result.Content), &page)
		if err != nil || len(page.Nodes) != tc.count || page.HasMore != tc.more || page.NextOffset != tc.next {
			t.Fatalf("offset=%d limit=%d page=%s err=%v", tc.offset, tc.limit, result.Content, err)
		}
	}
}

func TestQQReadForwardRejectsMismatchedHistoricalMetadata(t *testing.T) {
	for _, kind := range []string{"group", "private"} {
		t.Run(kind, func(t *testing.T) {
			actions := make(chan string, 3)
			c := qqCrossSessionConnection(t, func(action model.OneBotAction) oneBotAPIResponse {
				actions <- action.Action
				data, _ := json.Marshal(map[string]any{"message_id": 101, "message_type": "group", "group_id": testGroupID + 1, "message": []tools.OneBotSegment{{Type: "forward", Data: map[string]any{"id": "foreign-resource"}}}})
				return oneBotAPIResponse{Status: "ok", Data: data}
			})
			c.Scope = newTestScopeWithEnv(t, nil)
			defer c.Scope.Cancel()
			event := model.OneBotEvent{SelfID: testBotID, UserID: testUserID, GroupID: testGroupID, MessageType: kind, MessageID: 102}
			c.rememberMessageSession(101, c.historyKey(event))
			if _, err := c.qqActionHook(event, nil, nil, nil)(context.Background(), `{"action":"read_forward","arguments":{"message_id":"101"}}`); err == nil {
				t.Fatal("foreign metadata accepted")
			}
			if len(actions) != 1 || <-actions != "get_msg" {
				t.Fatal("foreign resource reached get_forward_msg")
			}
		})
	}
}

func TestQQReadForwardScopeAndHistoricalMessage(t *testing.T) {
	for _, kind := range []string{"group", "private"} {
		for _, historical := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/historical=%v", kind, historical), func(t *testing.T) {
				actions := make(chan string, 4)
				event := model.OneBotEvent{SelfID: testBotID, UserID: testUserID, GroupID: testGroupID, MessageType: kind, MessageID: 101, Message: json.RawMessage(`[{"type":"forward","data":{"id":"root"}}]`)}
				c := qqCrossSessionConnection(t, func(action model.OneBotAction) oneBotAPIResponse {
					actions <- action.Action
					if action.Action == "get_msg" {
						data, _ := json.Marshal(map[string]any{"message_id": 101, "message_type": kind, "user_id": testUserID, "group_id": testGroupID, "message": event.Message})
						if kind == "private" {
							data, _ = json.Marshal(map[string]any{"message_id": 101, "message_type": kind, "user_id": testUserID, "message": event.Message})
						}
						return oneBotAPIResponse{Status: "ok", Data: data}
					}
					return oneBotAPIResponse{Status: "ok", Data: json.RawMessage(`{"messages":[{"message_id":999,"content":"fixture record"}]}`)}
				})
				c.Scope = newTestScopeWithEnv(t, nil)
				defer c.Scope.Cancel()
				c.rememberMessageSession(101, c.historyKey(event))
				if historical {
					event.MessageID = 102
				}
				result, err := c.qqActionHook(event, nil, func([]tools.Msg) { t.Error("read counted as delivery") }, nil)(context.Background(), `{"action":"read_forward","arguments":{"message_id":"101"}}`)
				want := []string{"get_forward_msg"}
				if historical {
					want = []string{"get_msg", "get_forward_msg"}
				}
				var calls []string
				for len(actions) > 0 {
					calls = append(calls, <-actions)
				}
				if err != nil || result.MessagesSent != 0 || !strings.Contains(result.Content, "fixture record") || !reflect.DeepEqual(calls, want) {
					t.Fatalf("result=%+v calls=%v err=%v", result, calls, err)
				}
				if c.messageSessionMatches(999, c.historyKey(event)) {
					t.Fatal("forwarded message ID granted live authority")
				}
				c.rememberMessageSession(101, "other-session")
				if _, err := c.qqActionHook(event, nil, nil, nil)(context.Background(), `{"action":"read_forward","arguments":{"message_id":"101"}}`); err == nil {
					t.Fatal("foreign message accepted")
				}
				if len(actions) != 0 {
					t.Fatal("untrusted message reached the platform")
				}
			})
		}
	}
}

func TestQQReadForwardToolLoopAndSecurityGate(t *testing.T) {
	for _, block := range []bool{false, true} {
		t.Run(fmt.Sprintf("block=%v", block), func(t *testing.T) {
			actions := make(chan string, 4)
			c := qqCrossSessionConnection(t, func(action model.OneBotAction) oneBotAPIResponse {
				actions <- action.Action
				return oneBotAPIResponse{Status: "ok", Data: json.RawMessage(`{"messages":[{"content":"forwarded-content-marker"}]}`)}
			})
			c.Scope = newTestScopeWithEnv(t, nil)
			defer c.Scope.Cancel()
			event := model.OneBotEvent{MessageType: "private", SelfID: testBotID, UserID: testUserID, MessageID: 101, Message: json.RawMessage(`[{"type":"forward","data":{"id":"root"}}]`)}
			c.rememberMessageSession(101, c.historyKey(event))
			providerCalls, inspected := 0, false
			provider := &mockLLMProvider{customChat: func(_ context.Context, request core.ChatRequest) (*core.ChatResponse, error) {
				providerCalls++
				if providerCalls == 1 {
					return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "read-fixture", Type: "function", Function: core.ToolCallFunction{Name: qqextend.ToolName, Arguments: `{"action":"read_forward","arguments":{"message_id":"101"}}`}}}}}, nil
				}
				toolContent, _ := request.Messages[len(request.Messages)-1].Content.(string)
				if strings.Contains(toolContent, "forwarded-content-marker") == block {
					t.Errorf("security result was not applied: %s", toolContent)
				}
				return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "fixture summary"}}, nil
			}}
			engine := newTestEngine(provider)
			engine.Scope = c.Scope
			engine.ToolRegistry[qqextend.ToolName] = tools.QQTool()
			controller := security.NewController(t.TempDir())
			controller.SetMode(security.ControlModeAggressive)
			controller.Watchdog.SetClassifier(&mockClassifier{fn: func(_ context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
				if input.Stage == security.StageToolResult && strings.Contains(input.Content, "forwarded-content-marker") {
					inspected = true
					if block {
						return security.ClassificationResult{Category: security.RiskCategoryDataExfiltration, RiskLevel: security.RiskLevelHigh}, nil
					}
				}
				return security.ClassificationResult{Category: security.RiskCategoryNone, RiskLevel: security.RiskLevelNone}, nil
			}})
			engine.Security = controller
			result := engine.RunMessagesWithContext([]llm.ChatMessage{{Role: "user", Content: "fixture request"}}, llm.RunContext{SessionID: c.historyKey(event), ActorPlatform: "qq", ActorUserID: fmt.Sprint(testUserID), PlatformActionHook: c.qqActionHook(event, nil, func([]tools.Msg) { t.Error("read marked as a send") }, engine)})
			if result.Error != nil || result.Content != "fixture summary" || providerCalls != 2 || !inspected {
				t.Fatalf("result=%+v providerCalls=%d inspected=%v", result, providerCalls, inspected)
			}
			if len(actions) != 1 || <-actions != "get_forward_msg" {
				t.Fatal("read was sent twice or created a message delivery")
			}
		})
	}
}

func TestQQReadForwardStopsOnLookupFailureAndCancellation(t *testing.T) {
	for _, failure := range []error{context.DeadlineExceeded, errors.New("unsupported"), context.Canceled} {
		calls := 0
		_, err := readQQForwardPage(json.RawMessage(`[{"type":"forward","data":{"id":"root"}}]`), tools.QQArguments{}, func(model.OneBotAction) (oneBotAPIResponse, error) { calls++; return oneBotAPIResponse{}, failure })
		if !errors.Is(err, failure) || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	}
	c := newWSConnection(nil)
	defer c.Close()
	event := model.OneBotEvent{MessageType: "private", UserID: testUserID, MessageID: 101, Message: json.RawMessage(`[{"type":"forward","data":{"content":[]}}]`)}
	c.rememberMessageSession(101, c.historyKey(event))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.readQQForward(ctx, event, nil, tools.QQArguments{MessageID: "101"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read succeeded: %v", err)
	}
}
