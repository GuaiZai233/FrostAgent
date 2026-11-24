package onebot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/qqextend"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

func TestQQForwardImageReferenceBounds(t *testing.T) {
	a := tools.QQArguments{MessageID: "101", ForwardIndex: 1, Path: []tools.QQForwardStep{{NodeIndex: 20, ForwardIndex: 2}, {NodeIndex: 3}, {NodeIndex: 4}}}
	ref := qqForwardImageRef(a, 27)
	got, node, err := parseQQForwardImageRef(ref)
	if err != nil || !reflect.DeepEqual(got, a) || node != 27 {
		t.Fatalf("round trip: %+v node=%d err=%v", got, node, err)
	}
	for _, ref := range []string{
		"", "qq-forward-image:101", "qq-forward-image:101:0", "qq-forward-image:101:0:1:0",
		"qq-forward-image:resource:0:0", "qq-forward-image:101:-1:0", "qq-forward-image:101:0:-1",
		"qq-forward-image:101:0:9999999999999999999999999999", "qq-forward-image:101:0:0:0:0:0:0:0:0:0:0",
		qqForwardImagePrefix + strings.Repeat("0", 256),
	} {
		if _, _, err := parseQQForwardImageRef(ref); err == nil {
			t.Errorf("invalid reference accepted: %q", ref)
		}
	}
}

func TestQQForwardImageFormatsAndOrdinals(t *testing.T) {
	image := []byte("GIF89a forwarded fixture")
	encoded := base64.StdEncoding.EncodeToString(image)
	for _, tc := range []struct {
		name, body   string
		count, index int
	}{
		{"segments", fmt.Sprintf(`[{"type":"image","data":{}},{"type":"image","data":{"base64":%q}}]`, encoded), 2, 1},
		{"cq", fmt.Sprintf(`"[CQ:image][CQ:image,file=base64://%s]"`, encoded), 2, 1},
		{"mface", fmt.Sprintf(`[{"type":"mface","data":{"base64":%q}}]`, encoded), 1, 0},
		{"escaped cq", `"&#91;CQ:image,file=base64://ignored&#93;"`, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newWSConnection(nil)
			defer c.Close()
			event := model.OneBotEvent{MessageType: "private", UserID: testUserID, MessageID: 101,
				Message: json.RawMessage(`[{"type":"forward","data":{"content":[{"content":` + tc.body + `}]}}]`)}
			c.rememberMessageSession(101, c.historyKey(event))
			result, err := c.readQQForward(context.Background(), event, nil, tools.QQArguments{MessageID: "101"})
			if err != nil {
				t.Fatal(err)
			}
			var page struct{ Nodes []qqReadForwardNode }
			if err := json.Unmarshal([]byte(result.Content), &page); err != nil || len(page.Nodes) != 1 {
				t.Fatalf("page=%s err=%v", result.Content, err)
			}
			node := page.Nodes[0]
			if node.ImageCount != tc.count || strings.Contains(result.Content, encoded) {
				t.Fatalf("image metadata=%s", result.Content)
			}
			if tc.count == 0 {
				if node.ImageRef != "" {
					t.Fatal("escaped example became an image reference")
				}
				return
			}
			got, err := c.loadObservedImage(context.Background(), event, resolvedReplyContext{}, nil, node.ImageRef, tc.index)
			if err != nil || string(got) != string(image) {
				t.Fatalf("image=%q err=%v", got, err)
			}
			if _, err := c.loadObservedImage(context.Background(), event, resolvedReplyContext{}, nil, node.ImageRef, tc.count); err == nil {
				t.Fatal("out-of-range image accepted")
			}
			if tc.index == 1 {
				if _, err := c.loadObservedImage(context.Background(), event, resolvedReplyContext{}, nil, node.ImageRef, 0); err == nil {
					t.Fatal("unreadable slot shifted the image index")
				}
			}
			if _, ok := numericMessageID(node.ImageRef); ok {
				t.Fatal("image reference became a live message ID")
			}
		})
	}
}

func TestQQForwardImageNestedHistoricalPageAndSessionTrust(t *testing.T) {
	image := []byte("GIF89a nested fixture")
	encoded := base64.StdEncoding.EncodeToString(image)
	for _, kind := range []string{"private", "group"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			var foreign atomic.Bool
			source := json.RawMessage(`[{"type":"forward","data":{"id":"root"}}]`)
			c := qqCrossSessionConnection(t, func(action model.OneBotAction) oneBotAPIResponse {
				calls.Add(1)
				if action.Action == "get_msg" {
					userID, groupID := testUserID, testGroupID
					if foreign.Load() {
						userID++
						groupID++
					}
					if kind == "private" {
						groupID = 0
					}
					data, _ := json.Marshal(map[string]any{"message_id": 101, "message_type": kind, "user_id": userID, "group_id": groupID, "message": source})
					return oneBotAPIResponse{Status: "ok", Data: data}
				}
				if action.Action != "get_forward_msg" {
					t.Errorf("unexpected action %s", action.Action)
				}
				id := action.Params.(map[string]any)["id"]
				if id == "root" {
					return oneBotAPIResponse{Status: "ok", Data: json.RawMessage(`{"messages":[{"content":"skip"},{"content":[{"type":"forward","data":{"id":"child"}}]}]}`)}
				}
				if id != "child" {
					t.Errorf("unexpected resource %v", id)
				}
				return oneBotAPIResponse{Status: "ok", Data: json.RawMessage(fmt.Sprintf(`{"message":[{"type":"node","data":{"content":"skip"}},{"type":"node","data":{"content":[{"type":"image","data":{"base64":%q}}]}}]}`, encoded))}
			})
			c.Scope = newTestScopeWithEnv(t, nil)
			defer c.Scope.Cancel()
			event := model.OneBotEvent{SelfID: testBotID, MessageType: kind, UserID: testUserID, GroupID: testGroupID, MessageID: 102}
			c.rememberMessageSession(101, c.historyKey(event))
			a := tools.QQArguments{MessageID: "101", Path: []tools.QQForwardStep{{NodeIndex: 1}}, Offset: 1, Limit: 1}
			page, err := c.readQQForward(context.Background(), event, nil, a)
			if err != nil {
				t.Fatal(err)
			}
			var result struct{ Nodes []qqReadForwardNode }
			_ = json.Unmarshal([]byte(page.Content), &result)
			if len(result.Nodes) != 1 || result.Nodes[0].Index != 1 || result.Nodes[0].ImageCount != 1 {
				t.Fatalf("page=%s", page.Content)
			}
			ref := result.Nodes[0].ImageRef
			got, err := c.loadObservedImage(context.Background(), event, resolvedReplyContext{}, nil, ref, 0)
			if err != nil || string(got) != string(image) || calls.Load() != 6 {
				t.Fatalf("image=%q calls=%d err=%v", got, calls.Load(), err)
			}
			foreign.Store(true)
			if _, err := c.loadObservedImage(context.Background(), event, resolvedReplyContext{}, nil, ref, 0); err == nil || calls.Load() != 7 {
				t.Fatalf("foreign historical metadata accepted: calls=%d err=%v", calls.Load(), err)
			}
			c.rememberMessageSession(101, "other-session")
			if _, err := c.loadObservedImage(context.Background(), event, resolvedReplyContext{}, nil, ref, 0); err == nil || calls.Load() != 7 {
				t.Fatalf("foreign root queried: calls=%d err=%v", calls.Load(), err)
			}
		})
	}
}

func TestQQForwardImageCancellationAndRestrictedTurns(t *testing.T) {
	c := newWSConnection(nil)
	defer c.Close()
	event := model.OneBotEvent{MessageType: "private", UserID: testUserID, MessageID: 101, Message: json.RawMessage(`[{"type":"forward","data":{"content":[]}}]`)}
	c.rememberMessageSession(101, c.historyKey(event))
	ref := qqForwardImageRef(tools.QQArguments{MessageID: "101"}, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.loadObservedImage(ctx, event, resolvedReplyContext{}, nil, ref, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	for _, run := range []llm.RunContext{{Mock: true}, {Proactive: true}} {
		if _, err := c.loadObservedImage(llm.WithRunContext(context.Background(), run), event, resolvedReplyContext{}, nil, ref, 0); err == nil || !strings.Contains(err.Error(), "模拟或主动") {
			t.Fatalf("restricted turn accepted: %v", err)
		}
	}
}

func TestQQForwardImageRejectsUntrustedMediaSources(t *testing.T) {
	for _, source := range []string{"file:///private-fixture.png", "https://example.invalid/private-media", "http://127.0.0.1/private-media"} {
		c := newWSConnection(nil)
		event := model.OneBotEvent{MessageType: "private", UserID: testUserID, MessageID: 101,
			Message: json.RawMessage(fmt.Sprintf(`[{"type":"forward","data":{"content":[{"content":[{"type":"image","data":{"url":%q}}]}]}}]`, source))}
		c.rememberMessageSession(101, c.historyKey(event))
		ref := qqForwardImageRef(tools.QQArguments{MessageID: "101"}, 0)
		if data, err := c.loadObservedImage(context.Background(), event, resolvedReplyContext{}, nil, ref, 0); err == nil || len(data) != 0 {
			t.Errorf("untrusted image source accepted: %s err=%v", source, err)
		}
		if _, err := c.loadObservedImage(context.Background(), event, resolvedReplyContext{}, nil, qqForwardImageRef(tools.QQArguments{MessageID: "101"}, 1), 0); err == nil {
			t.Error("out-of-range node accepted")
		}
		c.Close()
	}
}

func TestQQForwardImageToolLoop(t *testing.T) {
	image := []byte("GIF89a tool loop fixture")
	encoded := base64.StdEncoding.EncodeToString(image)
	for _, dedicatedVision := range []bool{false, true} {
		t.Run(fmt.Sprintf("dedicated=%v", dedicatedVision), func(t *testing.T) {
			c := newWSConnection(nil)
			defer c.Close()
			c.Scope = newTestScopeWithEnv(t, nil)
			defer c.Scope.Cancel()
			event := model.OneBotEvent{MessageType: "private", UserID: testUserID, MessageID: 101,
				Message: json.RawMessage(fmt.Sprintf(`[{"type":"forward","data":{"content":[{"content":[{"type":"image","data":{"base64":%q}}]}]}}]`, encoded))}
			c.rememberMessageSession(101, c.historyKey(event))
			providerCalls, imageCalls := 0, 0
			var imageRef string
			vision := &mockLLMProvider{customChat: func(_ context.Context, request core.ChatRequest) (*core.ChatResponse, error) {
				imageCalls++
				body, _ := json.Marshal(request.Messages)
				if !strings.Contains(string(body), "data:image/gif;base64,"+encoded) {
					t.Errorf("vision request missing image: %s", body)
				}
				return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "forwarded image description marker"}}, nil
			}}
			provider := &mockLLMProvider{customChat: func(ctx context.Context, request core.ChatRequest) (*core.ChatResponse, error) {
				body, _ := json.Marshal(request.Messages)
				if strings.Contains(string(body), "data:image/gif;base64,") {
					return vision.Chat(ctx, request)
				}
				providerCalls++
				if providerCalls == 1 {
					return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "read-forward", Type: "function", Function: core.ToolCallFunction{Name: qqextend.ToolName, Arguments: `{"action":"read_forward","arguments":{"message_id":"101"}}`}}}}}, nil
				}
				if providerCalls == 2 {
					last, _ := request.Messages[len(request.Messages)-1].Content.(string)
					var page struct{ Nodes []qqReadForwardNode }
					if err := json.Unmarshal([]byte(last), &page); err != nil || len(page.Nodes) != 1 || page.Nodes[0].ImageRef == "" {
						t.Fatalf("missing image reference: %s err=%v", last, err)
					}
					imageRef = page.Nodes[0].ImageRef
					args, _ := json.Marshal(map[string]any{"message_id": imageRef})
					return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "inspect-forward", Type: "function", Function: core.ToolCallFunction{Name: tools.InspectMessageImageToolName, Arguments: string(args)}}}}}, nil
				}
				if !strings.Contains(string(body), "forwarded image description marker") || strings.Contains(string(body), encoded) {
					t.Errorf("incorrect inspection result: %s", body)
				}
				return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "fixture summary"}}, nil
			}}
			engine := newTestEngine(provider)
			engine.Scope = c.Scope
			if dedicatedVision {
				engine.VisionProvider = vision
			}
			engine.ToolRegistry[qqextend.ToolName] = tools.QQTool()
			engine.ToolRegistry[tools.InspectMessageImageToolName] = tools.InspectMessageImageTool(engine)
			delivered := &deliveryCapture{}
			result := engine.RunMessagesWithContext([]llm.ChatMessage{{Role: "user", Content: "fixture request"}}, llm.RunContext{
				SessionID: c.historyKey(event), ActorPlatform: "qq", ActorUserID: fmt.Sprint(testUserID),
				SendHook: delivered.hook,
				PlatformActionHook: c.qqActionHook(event, nil, func([]tools.Msg) { t.Error("image read counted as delivery") }, engine),
				LoadObservedImage: func(ctx context.Context, id string, index int) ([]byte, error) {
					return c.loadObservedImage(ctx, event, resolvedReplyContext{}, nil, id, index)
				},
			})
			if result.Error != nil || delivered.plainText() != "fixture summary" || providerCalls != 3 || imageCalls != 1 || imageRef == "" {
				t.Fatalf("result=%+v delivered=%q dialogue=%d images=%d ref=%q", result, delivered.plainText(), providerCalls, imageCalls, imageRef)
			}
		})
	}
}
