package onebot

import (
	"FrostAgent/internal/billing"
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/model"
	"FrostAgent/internal/tools"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// mockLLMProvider 实现 core.LLMProvider，返回预设的响应
type mockLLMProvider struct {
	mu       sync.Mutex
	reqCount int
	// responses 按顺序返回，如果为空则返回默认文本
	responses []*core.ChatResponse
	errs      []error
}

func (m *mockLLMProvider) Chat(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := m.reqCount
	m.reqCount++

	if idx < len(m.errs) && m.errs[idx] != nil {
		return nil, m.errs[idx]
	}

	if idx < len(m.responses) {
		return m.responses[idx], nil
	}

	// 默认返回纯文本
	return &core.ChatResponse{
		Message: core.ChatMessage{
			Role:    core.RoleAssistant,
			Content: "你好！这是默认的模拟回复。",
		},
		Usage: &core.Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
	}, nil
}

// newTestEngine 创建一个用于测试的 Engine
func newTestEngine(provider core.LLMProvider) *llm.Engine {
	return &llm.Engine{
		MaxIterations:  3,
		ToolRegistry:   make(map[string]llm.ToolExecutor),
		Provider:       provider,
		BaseURL:        "http://mock",
		APIKey:         "mock-key",
		ModelName:      "mock-model",
		SessionManager: llm.NewSessionManager(),
	}
}

// startWSTestServer 启动一个带 WebSocket handler 的测试服务器
func startWSTestServer(engine *llm.Engine) (*httptest.Server, string) {
	mux := http.NewServeMux()
	adapter := NewAdapter(engine)
	if engine != nil && engine.Dispatcher != nil {
		engine.Dispatcher.RegisterAdapter(adapter)
	}
	mux.HandleFunc("/ws/frostagent", adapter.Handler())
	srv := httptest.NewServer(mux)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/frostagent"
	return srv, wsURL
}

func TestHandleWSPrivateMessage(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	// 连接 WebSocket
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	// 构造一个私聊消息事件
	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "private",
		UserID:      987654,
		MessageID:   1,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"你好"}}]`),
	}
	eventBytes, _ := json.Marshal(event)

	// 发送
	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	// 读取响应
	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	// 解析响应
	var action model.OneBotAction
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析响应失败: %v\n原始响应: %s", err, string(respBytes))
	}

	// 验证 action
	if action.Action != "send_private_msg" {
		t.Errorf("期望 action=send_private_msg, 实际=%s", action.Action)
	}

	params, ok := action.Params.(map[string]interface{})
	if !ok {
		t.Fatalf("params 不是 map: %T", action.Params)
	}

	// 验证 user_id
	if fmt.Sprintf("%v", params["user_id"]) != "987654" {
		t.Errorf("期望 user_id=987654, 实际=%v", params["user_id"])
	}

	// 验证 message 是默认文本回复
	if msg, ok := params["message"].(string); ok {
		if msg != "你好！这是默认的模拟回复。" {
			t.Errorf("期望回复内容=%q, 实际=%q", "你好！这是默认的模拟回复。", msg)
		}
	} else {
		t.Errorf("message 不是字符串: %T", params["message"])
	}

	t.Logf("✅ 私聊消息测试通过，回复内容: %v", params["message"])
}

func TestHandleWSGroupMessageMentioned(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	// 构造一个群聊 @ 机器人消息
	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "group",
		GroupID:     20002,
		UserID:      987654,
		MessageID:   2,
		Message: json.RawMessage(fmt.Sprintf(
			`[{"type":"at","data":{"qq":"123456"}},{"type":"text","data":{"text":"你好"}}]`,
		)),
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	// 首次遇到群聊时先收到非阻塞的群信息查询。
	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取群信息查询失败: %v", err)
	}
	var groupInfoAction model.OneBotAction
	if err := json.Unmarshal(respBytes, &groupInfoAction); err != nil {
		t.Fatalf("解析群信息查询失败: %v\n原始响应: %s", err, string(respBytes))
	}
	if groupInfoAction.Action != "get_group_info" {
		t.Fatalf("期望首个 action=get_group_info, 实际=%s", groupInfoAction.Action)
	}
	groupInfoResponse := map[string]interface{}{
		"status":  "ok",
		"retcode": 0,
		"data": map[string]interface{}{
			"group_id":   event.GroupID,
			"group_name": "测试群",
		},
		"echo": groupInfoAction.Echo,
	}
	responseBytes, _ := json.Marshal(groupInfoResponse)
	if err := conn.WriteMessage(websocket.TextMessage, responseBytes); err != nil {
		t.Fatalf("发送群信息响应失败: %v", err)
	}

	_, respBytes, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取群消息回复失败: %v", err)
	}

	var action model.OneBotAction
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析响应失败: %v\n原始响应: %s", err, string(respBytes))
	}

	if action.Action != "send_group_msg" {
		t.Errorf("期望 action=send_group_msg, 实际=%s", action.Action)
	}

	params, ok := action.Params.(map[string]interface{})
	if !ok {
		t.Fatalf("params 不是 map: %T", action.Params)
	}

	if fmt.Sprintf("%v", params["group_id"]) != "20002" {
		t.Errorf("期望 group_id=20002, 实际=%v", params["group_id"])
	}

	t.Logf("✅ 群聊@消息测试通过，回复内容: %v", params["message"])
}

func TestSenderContextKeepsDistinctNicknameAndCard(t *testing.T) {
	event := model.OneBotEvent{
		UserID: 10001,
		Sender: &model.OneBotSender{
			UserID:   10001,
			Nickname: "QQ昵称",
			Card:     "群名片",
		},
	}
	context := senderContext(event)
	if context["nickname"] != "QQ昵称" || context["card"] != "群名片" {
		t.Fatalf("expected both nickname and card, got %#v", context)
	}
}

func TestSenderContextDeduplicatesEqualNicknameAndCard(t *testing.T) {
	event := model.OneBotEvent{
		UserID: 10001,
		Sender: &model.OneBotSender{
			Nickname: "同一个名字",
			Card:     "同一个名字",
		},
	}
	context := senderContext(event)
	if context["nickname"] != "同一个名字" {
		t.Fatalf("expected nickname retained, got %#v", context)
	}
	if _, ok := context["card"]; ok {
		t.Fatalf("expected duplicate card omitted, got %#v", context)
	}
}

func TestSenderDisplayNameIncludesCardAndNickname(t *testing.T) {
	event := model.OneBotEvent{
		Sender: &model.OneBotSender{
			Nickname: "FoxUser",
			Card:     "foxcard",
		},
	}
	if got := senderDisplayName(event); got != "foxcard（FoxUser）" {
		t.Fatalf("unexpected sender display name: %q", got)
	}
}

func TestSenderDisplayNameDeduplicatesEqualCardAndNickname(t *testing.T) {
	event := model.OneBotEvent{
		Sender: &model.OneBotSender{
			Nickname: "同一个名字",
			Card:     "同一个名字",
		},
	}
	if got := senderDisplayName(event); got != "同一个名字" {
		t.Fatalf("unexpected sender display name: %q", got)
	}
}

func TestGroupInfoResponseUpdatesConnectionCache(t *testing.T) {
	conn := newWSConnection(nil)
	conn.pendingGroupByID[123456] = "group-echo"
	conn.pendingGroupByEcho["group-echo"] = pendingGroupInfo{
		GroupID:     123456,
		RequestedAt: time.Now(),
	}

	raw := []byte(`{"status":"ok","retcode":0,"data":{"group_id":123456,"group_name":" 群名称\n "},"echo":"group-echo"}`)
	if !conn.handleAPIResponse(raw) {
		t.Fatal("expected API response to be consumed")
	}
	cached := conn.groupCache[123456]
	if cached.Name != "群名称" {
		t.Fatalf("unexpected cached group name: %q", cached.Name)
	}
	if _, ok := conn.pendingGroupByID[123456]; ok {
		t.Fatal("expected pending group request cleared")
	}
}

func TestHandleWSGroupMessageNotMentioned(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	// 群聊消息但没有 @ 机器人
	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "group",
		GroupID:     1919810,
		UserID:      114514,
		MessageID:   3,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"你好"}}]`),
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	// 设置读超时，因为没有 @ 机器人不应该收到回复
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, err = conn.ReadMessage()
	if err == nil {
		t.Error("期望没有回复（未@机器人），但收到了消息")
	} else {
		t.Logf("✅ 未@机器人无回复测试通过: %v", err)
	}
}

func TestHandleWSSendMessageToolHook(t *testing.T) {
	// 模拟 LLM：第一轮调用 send_message 工具
	mock := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "好的，我来发送消息~",
					ToolCalls: []core.ToolCall{
						{
							ID:   "call_001",
							Type: "function",
							Function: core.ToolCallFunction{
								Name:      "send_message",
								Arguments: `{"messages":[{"type":"plain","text":"稍等~ 喵宝正在查询天气！🐱"}]}`,
							},
						},
					},
				},
			},
			{
				// 第二轮：LLM 收到 "消息已发送" 后给出最终回复
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "已发送！",
				},
			},
		},
	}

	engine := newTestEngine(mock)
	// 注册 send_message 工具
	engine.ToolRegistry["send_message"] = tools.SendMsgTool()

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "private",
		UserID:      987654,
		MessageID:   4,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"查询天气"}}]`),
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	// 第一条消息：SendHook 通过 send_message 发出的
	_, resp1Bytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取第一条响应失败: %v", err)
	}

	var action1 model.OneBotAction
	if err := json.Unmarshal(resp1Bytes, &action1); err != nil {
		t.Fatalf("解析第一条响应失败: %v\n原始: %s", err, string(resp1Bytes))
	}

	params1, _ := action1.Params.(map[string]interface{})
	msg1, _ := params1["message"].([]interface{})
	if len(msg1) == 0 {
		t.Fatalf("第一条消息内容为空")
	}
	seg1, _ := msg1[0].(map[string]interface{})
	if seg1["type"] != "text" {
		t.Errorf("期望第一条消息 type=text, 实际=%v", seg1["type"])
	}
	t.Logf("✅ SendHook 发送成功: type=%s, data=%v", seg1["type"], seg1["data"])

	// 第二条消息：LLM 最终文本回复
	_, resp2Bytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取第二条响应失败: %v", err)
	}

	var action2 model.OneBotAction
	if err := json.Unmarshal(resp2Bytes, &action2); err != nil {
		t.Fatalf("解析第二条响应失败: %v\n原始: %s", err, string(resp2Bytes))
	}

	params2, _ := action2.Params.(map[string]interface{})
	msg2, _ := params2["message"].(string)
	if msg2 != "已发送！" {
		t.Errorf("期望最终回复='已发送！', 实际=%q", msg2)
	}
	t.Logf("✅ 最终回复测试通过: %s", msg2)
}

// mockAlcyoneBillingServer 创建一个用于测试计费流程的模拟 Alcyone 服务
func mockAlcyoneBillingServer(t *testing.T) (*httptest.Server, *mockAlcyoneState) {
	state := &mockAlcyoneState{
		reservations: make(map[string]billing.LLMReservationResult),
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/v1/balance" {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.balanceHandler != nil {
				state.balanceHandler(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": billing.BalanceResult{
					Exists:       true,
					Platform:     "qq",
					ExternalID:   "987654",
					BalanceMinor: 10000,
				},
			})
			return
		}

		if r.URL.Path == "/v1/billing/llm/reservations" && r.Method == http.MethodPost {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.reserveHandler != nil {
				state.reserveHandler(w, r)
				return
			}
			var req billing.LLMReserveRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			state.reserveCount++
			resID := fmt.Sprintf("res_%d", state.reserveCount)
			result := billing.LLMReservationResult{
				ReservationID: resID,
				UserUID:       "user_mock_uid",
				Decision:      billing.DecisionReserved,
				Status:        billing.StatusReserved,
				ReservedMinor: req.AmountMinor,
				BalanceMinor:  10000,
			}
			state.reservations[resID] = result
			json.NewEncoder(w).Encode(map[string]interface{}{"data": result})
			return
		}

		if strings.HasPrefix(r.URL.Path, "/v1/billing/llm/reservations/") && strings.HasSuffix(r.URL.Path, "/commit") {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.commitHandler != nil {
				state.commitHandler(w, r)
				return
			}
			var req map[string]int64
			json.NewDecoder(r.Body).Decode(&req)
			actualMinor := req["actual_minor"]
			state.commitCount++
			state.committedActuals = append(state.committedActuals, actualMinor)

			result := billing.LLMReservationResult{
				ReservationID: "res_mock",
				Decision:      billing.DecisionReserved,
				Status:        billing.StatusCommitted,
				BalanceMinor:  10000 - actualMinor,
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"data": result})
			return
		}

		if strings.HasPrefix(r.URL.Path, "/v1/billing/llm/reservations/") && strings.HasSuffix(r.URL.Path, "/release") {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.releaseHandler != nil {
				state.releaseHandler(w, r)
				return
			}
			var req map[string]string
			json.NewDecoder(r.Body).Decode(&req)
			reason := req["reason"]
			state.releaseCount++
			state.releasedReasons = append(state.releasedReasons, reason)

			result := billing.LLMReservationResult{
				ReservationID: "res_mock",
				Decision:      billing.DecisionReserved,
				Status:        billing.StatusReleased,
				BalanceMinor:  10000,
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"data": result})
			return
		}

		http.NotFound(w, r)
	})

	srv := httptest.NewServer(handler)
	return srv, state
}

type mockAlcyoneState struct {
	mu               sync.Mutex
	reserveCount     int
	commitCount      int
	releaseCount     int
	committedActuals []int64
	releasedReasons  []string
	reservations     map[string]billing.LLMReservationResult
	reserveHandler   func(w http.ResponseWriter, r *http.Request)
	commitHandler    func(w http.ResponseWriter, r *http.Request)
	releaseHandler   func(w http.ResponseWriter, r *http.Request)
	balanceHandler   func(w http.ResponseWriter, r *http.Request)
}

func TestWSBilling_WelcomeBonus(t *testing.T) {
	alcyoneSrv, state := mockAlcyoneBillingServer(t)
	defer alcyoneSrv.Close()

	state.reserveHandler = func(w http.ResponseWriter, r *http.Request) {
		state.reserveCount++
		result := billing.LLMReservationResult{
			ReservationID: "res_welcome_1",
			UserUID:       "user_new",
			Decision:      billing.DecisionWelcome,
			Status:        billing.StatusWelcome,
			ReservedMinor: 200,
			BalanceMinor:  10000, // 100 snowflakes bonus
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"data": result})
	}

	state.commitHandler = func(w http.ResponseWriter, r *http.Request) {
		var req map[string]int64
		json.NewDecoder(r.Body).Decode(&req)
		actualMinor := req["actual_minor"]
		state.commitCount++
		state.committedActuals = append(state.committedActuals, actualMinor)
		result := billing.LLMReservationResult{
			ReservationID: "res_welcome_1",
			Status:        billing.StatusCommitted,
			BalanceMinor:  10000 - actualMinor,
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"data": result})
	}

	mockLLM := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "你好！欢迎使用智能体！",
				},
				Usage: &core.Usage{
					PromptTokens:     1000,
					CompletionTokens: 200,
					TotalTokens:      1200,
				},
			},
		},
	}

	engine := newTestEngine(mockLLM)
	billingClient := billing.NewClient(alcyoneSrv.URL, "test-token", 2*time.Second)
	engine.BillingClient = billingClient
	engine.BillingConfig = billing.Config{
		Enabled:          true,
		BaseURL:          alcyoneSrv.URL,
		Timeout:          2 * time.Second,
		MaxOutputTokens:  2048,
		SafetyMultiplier: 1.2,
		ModelName:        "deepseek-chat",
	}

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "private",
		UserID:      987654,
		MessageID:   101,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"你好呀"}}]`),
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	var action model.OneBotAction
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	params, ok := action.Params.(map[string]interface{})
	if !ok {
		t.Fatalf("params 不是 map: %T", action.Params)
	}
	replyMsg, _ := params["message"].(string)

	if !strings.Contains(replyMsg, "🎉 首次对话赠送 100.00 片雪花！") {
		t.Errorf("期望回复包含首充赠送提示，实际回复: %s", replyMsg)
	}
	if !strings.Contains(replyMsg, "❄️ 本次消耗:") || !strings.Contains(replyMsg, "剩余余额:") {
		t.Errorf("期望回复包含消费小票，实际回复: %s", replyMsg)
	}
	if state.reserveCount != 1 || state.commitCount != 0 {
		t.Errorf("期望 reserve=1, commit=0; 实际 reserve=%d, commit=%d", state.reserveCount, state.commitCount)
	}
	t.Logf("✅ 首次对话欢迎奖励测试通过，最终回复: %s", replyMsg)
}

func TestWSBilling_WelcomeBonus_WithToolCall(t *testing.T) {
	alcyoneSrv, state := mockAlcyoneBillingServer(t)
	defer alcyoneSrv.Close()

	state.reserveHandler = func(w http.ResponseWriter, r *http.Request) {
		state.reserveCount++
		if state.reserveCount == 1 {
			result := billing.LLMReservationResult{
				ReservationID: "res_welcome_1",
				UserUID:       "user_new",
				Decision:      billing.DecisionWelcome,
				Status:        billing.StatusWelcome,
				ReservedMinor: 200,
				BalanceMinor:  10000,
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"data": result})
		} else {
			result := billing.LLMReservationResult{
				ReservationID: "res_tool_turn2",
				UserUID:       "user_new",
				Decision:      billing.DecisionReserved,
				Status:        billing.StatusReserved,
				ReservedMinor: 200,
				BalanceMinor:  10000 - 200,
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"data": result})
		}
	}

	state.commitHandler = func(w http.ResponseWriter, r *http.Request) {
		var req map[string]int64
		json.NewDecoder(r.Body).Decode(&req)
		actualMinor := req["actual_minor"]
		state.commitCount++
		state.committedActuals = append(state.committedActuals, actualMinor)
		result := billing.LLMReservationResult{
			ReservationID: "res_tool_turn2",
			Status:        billing.StatusCommitted,
			BalanceMinor:  9800 - actualMinor,
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"data": result})
	}

	mockLLM := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role: core.RoleAssistant,
					ToolCalls: []core.ToolCall{
						{
							ID:   "call_weather_1",
							Type: "function",
							Function: core.ToolCallFunction{
								Name:      "get_weather",
								Arguments: `{"city":"杭州"}`,
							},
						},
					},
				},
				Usage: &core.Usage{
					PromptTokens:     500,
					CompletionTokens: 50,
					TotalTokens:      550,
				},
			},
			{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "杭州天气晴朗！",
				},
				Usage: &core.Usage{
					PromptTokens:     700,
					CompletionTokens: 100,
					TotalTokens:      800,
				},
			},
		},
	}

	engine := newTestEngine(mockLLM)
	engine.ToolRegistry["get_weather"] = &mockTestTool{name: "get_weather", result: "杭州天气晴朗"}
	billingClient := billing.NewClient(alcyoneSrv.URL, "test-token", 2*time.Second)
	engine.BillingClient = billingClient
	engine.BillingConfig = billing.Config{
		Enabled:          true,
		BaseURL:          alcyoneSrv.URL,
		Timeout:          2 * time.Second,
		MaxOutputTokens:  2048,
		SafetyMultiplier: 1.2,
		ModelName:        "deepseek-chat",
	}

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "private",
		UserID:      987654,
		MessageID:   103,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"查一下杭州天气"}}]`),
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	var action model.OneBotAction
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	params, ok := action.Params.(map[string]interface{})
	if !ok {
		t.Fatalf("params 不是 map: %T", action.Params)
	}
	replyMsg, _ := params["message"].(string)

	if !strings.Contains(replyMsg, "杭州天气晴朗！") {
		t.Errorf("期望包含最终工具回答，实际回复: %s", replyMsg)
	}
	if !strings.Contains(replyMsg, "🎉 首次对话赠送 100.00 片雪花！") {
		t.Errorf("期望包含首充赠送提示，实际回复: %s", replyMsg)
	}
	if state.reserveCount != 2 || state.commitCount != 1 {
		t.Errorf("期望 reserve=2, commit=1; 实际 reserve=%d, commit=%d", state.reserveCount, state.commitCount)
	}
	t.Logf("✅ 首次对话带 Tool Call 闭环测试通过，最终回复: %s", replyMsg)
}

func TestWSBilling_StandardConsumption(t *testing.T) {
	alcyoneSrv, state := mockAlcyoneBillingServer(t)
	defer alcyoneSrv.Close()

	state.reserveHandler = func(w http.ResponseWriter, r *http.Request) {
		state.reserveCount++
		result := billing.LLMReservationResult{
			ReservationID: "res_std_1",
			UserUID:       "user_existing",
			Decision:      billing.DecisionReserved,
			Status:        billing.StatusReserved,
			ReservedMinor: 200,
			BalanceMinor:  5000,
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"data": result})
	}

	state.commitHandler = func(w http.ResponseWriter, r *http.Request) {
		var req map[string]int64
		json.NewDecoder(r.Body).Decode(&req)
		actualMinor := req["actual_minor"]
		state.commitCount++
		state.committedActuals = append(state.committedActuals, actualMinor)
		result := billing.LLMReservationResult{
			ReservationID: "res_std_1",
			Status:        billing.StatusCommitted,
			BalanceMinor:  5000 - actualMinor,
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"data": result})
	}

	mockLLM := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "这是第二次对话的回复。",
				},
				Usage: &core.Usage{
					PromptTokens:     1000,
					CompletionTokens: 200,
					TotalTokens:      1200,
				},
			},
		},
	}

	engine := newTestEngine(mockLLM)
	billingClient := billing.NewClient(alcyoneSrv.URL, "test-token", 2*time.Second)
	engine.BillingClient = billingClient
	engine.BillingConfig = billing.Config{
		Enabled:          true,
		BaseURL:          alcyoneSrv.URL,
		Timeout:          2 * time.Second,
		MaxOutputTokens:  2048,
		SafetyMultiplier: 1.2,
		ModelName:        "deepseek-chat",
	}

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "private",
		UserID:      987654,
		MessageID:   102,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"再聊一句"}}]`),
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	var action model.OneBotAction
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	params, ok := action.Params.(map[string]interface{})
	if !ok {
		t.Fatalf("params 不是 map: %T", action.Params)
	}
	replyMsg, _ := params["message"].(string)

	if strings.Contains(replyMsg, "首次对话赠送") {
		t.Errorf("非首次对话不应包含首充提示，实际回复: %s", replyMsg)
	}
	if !strings.Contains(replyMsg, "❄️ 本次消耗:") || !strings.Contains(replyMsg, "剩余余额:") {
		t.Errorf("期望回复包含消费小票，实际回复: %s", replyMsg)
	}
	if state.reserveCount != 1 || state.commitCount != 1 {
		t.Errorf("期望 reserve=1, commit=1; 实际 reserve=%d, commit=%d", state.reserveCount, state.commitCount)
	}
	t.Logf("✅ 常规对话结算测试通过，最终回复: %s", replyMsg)
}

func TestWSBilling_InsufficientFunds_EarlyAbort(t *testing.T) {
	alcyoneSrv, state := mockAlcyoneBillingServer(t)
	defer alcyoneSrv.Close()

	state.reserveHandler = func(w http.ResponseWriter, r *http.Request) {
		state.reserveCount++
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{
				"code":    "insufficient_funds",
				"message": "insufficient snowflake balance",
			},
		})
	}
	state.balanceHandler = func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": billing.BalanceResult{
				Exists:       true,
				Platform:     "qq",
				ExternalID:   "987654",
				BalanceMinor: 5, // 0.05 snowflakes
			},
		})
	}

	mockLLM := &mockLLMProvider{
		responses: []*core.ChatResponse{},
	}

	engine := newTestEngine(mockLLM)
	billingClient := billing.NewClient(alcyoneSrv.URL, "test-token", 2*time.Second)
	engine.BillingClient = billingClient
	engine.BillingConfig = billing.Config{
		Enabled:          true,
		BaseURL:          alcyoneSrv.URL,
		Timeout:          2 * time.Second,
		MaxOutputTokens:  2048,
		SafetyMultiplier: 1.2,
		ModelName:        "deepseek-chat",
	}

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "private",
		UserID:      987654,
		MessageID:   103,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"帮我写篇论文"}}]`),
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	var action model.OneBotAction
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	params, ok := action.Params.(map[string]interface{})
	if !ok {
		t.Fatalf("params 不是 map: %T", action.Params)
	}
	replyMsg, _ := params["message"].(string)

	if !strings.Contains(replyMsg, "余额不足") || !strings.Contains(replyMsg, "0.05 片") {
		t.Errorf("期望收到余额不足提示信息，实际回复: %s", replyMsg)
	}
	if mockLLM.reqCount != 0 {
		t.Errorf("余额不足时严禁调用 LLM，实际调用次数: %d", mockLLM.reqCount)
	}
	t.Logf("✅ 余额不足早停拒绝对话测试通过: %s", replyMsg)
}

func TestWSBilling_FailClosedOnServiceUnavailable(t *testing.T) {
	alcyoneSrv, state := mockAlcyoneBillingServer(t)
	// Immediately close the mock server to simulate service downtime
	alcyoneSrv.Close()

	mockLLM := &mockLLMProvider{
		responses: []*core.ChatResponse{},
	}

	engine := newTestEngine(mockLLM)
	billingClient := billing.NewClient(alcyoneSrv.URL, "test-token", 500*time.Millisecond)
	engine.BillingClient = billingClient
	engine.BillingConfig = billing.Config{
		Enabled:          true,
		BaseURL:          alcyoneSrv.URL,
		Timeout:          500 * time.Millisecond,
		MaxOutputTokens:  2048,
		SafetyMultiplier: 1.2,
		ModelName:        "deepseek-chat",
	}

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "private",
		UserID:      987654,
		MessageID:   104,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"你好"}}]`),
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	var action model.OneBotAction
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	params, ok := action.Params.(map[string]interface{})
	if !ok {
		t.Fatalf("params 不是 map: %T", action.Params)
	}
	replyMsg, _ := params["message"].(string)

	if !strings.Contains(replyMsg, "计费系统暂时不可用") {
		t.Errorf("计费服务异常时必须 Fail-Closed 拒绝对话，实际回复: %s", replyMsg)
	}
	if mockLLM.reqCount != 0 {
		t.Errorf("计费服务不可用时严禁调用 LLM，实际调用次数: %d", mockLLM.reqCount)
	}
	if state.commitCount != 0 {
		t.Errorf("不应发生 commit 调用")
	}
	t.Logf("✅ 计费服务不可用 Fail-Closed 测试通过: %s", replyMsg)
}

func TestWSBilling_ModelFailureReleasesReservation(t *testing.T) {
	alcyoneSrv, state := mockAlcyoneBillingServer(t)
	defer alcyoneSrv.Close()

	mockLLM := &mockLLMProvider{
		errs: []error{errors.New("mock upstream timeout 504")},
	}

	engine := newTestEngine(mockLLM)
	billingClient := billing.NewClient(alcyoneSrv.URL, "test-token", 2*time.Second)
	engine.BillingClient = billingClient
	engine.BillingConfig = billing.Config{
		Enabled:          true,
		BaseURL:          alcyoneSrv.URL,
		Timeout:          2 * time.Second,
		MaxOutputTokens:  2048,
		SafetyMultiplier: 1.2,
		ModelName:        "deepseek-chat",
	}

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "private",
		UserID:      987654,
		MessageID:   105,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"触发异常"}}]`),
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	var action model.OneBotAction
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	if state.reserveCount != 1 {
		t.Errorf("期望 reserve=1, 实际=%d", state.reserveCount)
	}
	if state.releaseCount != 1 {
		t.Errorf("期望 release=1, 实际=%d", state.releaseCount)
	}
	if len(state.releasedReasons) > 0 && state.releasedReasons[0] != billing.ReasonModelFailed {
		t.Errorf("期望 release 原因=model_failed, 实际=%s", state.releasedReasons[0])
	}
	if state.commitCount != 0 {
		t.Errorf("模型失败时不应发生 commit")
	}
	t.Logf("✅ 模型异常自动释放预扣款测试通过: releaseCount=%d", state.releaseCount)
}

type mockTestTool struct {
	name     string
	executed int
	result   string
	err      error
}

func (m *mockTestTool) Name() string        { return m.name }
func (m *mockTestTool) Description() string { return "test tool description" }
func (m *mockTestTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (m *mockTestTool) Execute(args string) (string, error) {
	m.executed++
	return m.result, m.err
}

func TestWSBilling_ToolLoop_MultiTurnAccumulation(t *testing.T) {
	alcyoneSrv, state := mockAlcyoneBillingServer(t)
	defer alcyoneSrv.Close()

	mockTool := &mockTestTool{
		name:   "search_tool",
		result: "搜索结果: 明天多云转晴",
	}

	mockLLM := &mockLLMProvider{
		responses: []*core.ChatResponse{
			// Turn 1: model requests tool call
			{
				Message: core.ChatMessage{
					Role: core.RoleAssistant,
					ToolCalls: []core.ToolCall{
						{
							ID:   "call_search_1",
							Type: "function",
							Function: core.ToolCallFunction{
								Name:      "search_tool",
								Arguments: `{"query":"天气"}`,
							},
						},
					},
				},
				Usage: &core.Usage{
					PromptTokens:     500,
					CompletionTokens: 50,
					TotalTokens:      550,
				},
			},
			// Turn 2: model provides final answer
			{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "根据查询，明天天气多云转晴！",
				},
				Usage: &core.Usage{
					PromptTokens:     700,
					CompletionTokens: 100,
					TotalTokens:      800,
				},
			},
		},
	}

	engine := newTestEngine(mockLLM)
	engine.ToolRegistry["search_tool"] = mockTool
	billingClient := billing.NewClient(alcyoneSrv.URL, "test-token", 2*time.Second)
	engine.BillingClient = billingClient
	engine.BillingConfig = billing.Config{
		Enabled:          true,
		BaseURL:          alcyoneSrv.URL,
		Timeout:          2 * time.Second,
		MaxOutputTokens:  2048,
		SafetyMultiplier: 1.2,
		ModelName:        "deepseek-chat",
	}

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "private",
		UserID:      987654,
		MessageID:   106,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"明天天气怎么样"}}]`),
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	var action model.OneBotAction
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	params, ok := action.Params.(map[string]interface{})
	if !ok {
		t.Fatalf("params 不是 map: %T", action.Params)
	}
	replyMsg, _ := params["message"].(string)

	if mockTool.executed != 1 {
		t.Errorf("期望 tool 执行 1 次, 实际=%d", mockTool.executed)
	}
	if mockLLM.reqCount != 2 {
		t.Errorf("期望 LLM 调用 2 次, 实际=%d", mockLLM.reqCount)
	}
	if state.reserveCount != 2 {
		t.Errorf("期望 reserve 2 次 (每轮单独预扣), 实际=%d", state.reserveCount)
	}
	if state.commitCount != 2 {
		t.Errorf("期望 commit 2 次 (每轮单独结算), 实际=%d", state.commitCount)
	}
	if !strings.Contains(replyMsg, "输入: 1200, 输出: 150") {
		t.Errorf("期望回执包含两轮累计 token (1200 in, 150 out)，实际回复: %s", replyMsg)
	}
	t.Logf("✅ Tool 循环多轮计费与 Token 累计测试通过: %s", replyMsg)
}

func TestWSBilling_ToolLoop_InsufficientFundsOnSecondTurn(t *testing.T) {
	alcyoneSrv, state := mockAlcyoneBillingServer(t)
	defer alcyoneSrv.Close()

	mockTool := &mockTestTool{
		name:   "search_tool",
		result: "搜索结果: 内容",
	}

	state.reserveHandler = func(w http.ResponseWriter, r *http.Request) {
		state.reserveCount++
		if state.reserveCount == 1 {
			// 第一轮正常
			result := billing.LLMReservationResult{
				ReservationID: "res_turn_1",
				Decision:      billing.DecisionReserved,
				Status:        billing.StatusReserved,
				ReservedMinor: 200,
				BalanceMinor:  300,
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"data": result})
			return
		}
		// 第二轮余额不足
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{
				"code":    "insufficient_funds",
				"message": "insufficient snowflake balance",
			},
		})
	}

	mockLLM := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role: core.RoleAssistant,
					ToolCalls: []core.ToolCall{
						{
							ID:   "call_search_1",
							Type: "function",
							Function: core.ToolCallFunction{
								Name:      "search_tool",
								Arguments: `{"query":"test"}`,
							},
						},
					},
				},
				Usage: &core.Usage{
					PromptTokens:     500,
					CompletionTokens: 50,
					TotalTokens:      550,
				},
			},
		},
	}

	engine := newTestEngine(mockLLM)
	engine.ToolRegistry["search_tool"] = mockTool
	billingClient := billing.NewClient(alcyoneSrv.URL, "test-token", 2*time.Second)
	engine.BillingClient = billingClient
	engine.BillingConfig = billing.Config{
		Enabled:          true,
		BaseURL:          alcyoneSrv.URL,
		Timeout:          2 * time.Second,
		MaxOutputTokens:  2048,
		SafetyMultiplier: 1.2,
		ModelName:        "deepseek-chat",
	}

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "private",
		UserID:      987654,
		MessageID:   107,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"执行多步任务"}}]`),
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	var action model.OneBotAction
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	params, ok := action.Params.(map[string]interface{})
	if !ok {
		t.Fatalf("params 不是 map: %T", action.Params)
	}
	replyMsg, _ := params["message"].(string)

	if state.reserveCount != 2 {
		t.Errorf("期望 reserve 尝试 2 次, 实际=%d", state.reserveCount)
	}
	if state.commitCount != 1 {
		t.Errorf("期望第一轮成功 commit (不退款), commitCount 实际=%d", state.commitCount)
	}
	if !strings.Contains(replyMsg, "余额不足") {
		t.Errorf("期望中途余额不足提示，实际回复: %s", replyMsg)
	}
	t.Logf("✅ Tool 循环中途余额不足早停测试通过: %s", replyMsg)
}

func TestWSBilling_ToolCall_CommitFailureStopsTool(t *testing.T) {
	alcyoneSrv, state := mockAlcyoneBillingServer(t)
	defer alcyoneSrv.Close()

	mockTool := &mockTestTool{
		name:   "search_tool",
		result: "搜索结果",
	}

	state.commitHandler = func(w http.ResponseWriter, r *http.Request) {
		state.commitCount++
		// 模拟结算端点异常
		http.Error(w, `{"error":"internal_error"}`, http.StatusInternalServerError)
	}

	mockLLM := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role: core.RoleAssistant,
					ToolCalls: []core.ToolCall{
						{
							ID:   "call_search_1",
							Type: "function",
							Function: core.ToolCallFunction{
								Name:      "search_tool",
								Arguments: `{"query":"test"}`,
							},
						},
					},
				},
				Usage: &core.Usage{
					PromptTokens:     500,
					CompletionTokens: 50,
					TotalTokens:      550,
				},
			},
		},
	}

	engine := newTestEngine(mockLLM)
	engine.ToolRegistry["search_tool"] = mockTool
	billingClient := billing.NewClient(alcyoneSrv.URL, "test-token", 2*time.Second)
	engine.BillingClient = billingClient
	engine.BillingConfig = billing.Config{
		Enabled:          true,
		BaseURL:          alcyoneSrv.URL,
		Timeout:          2 * time.Second,
		MaxOutputTokens:  2048,
		SafetyMultiplier: 1.2,
		ModelName:        "deepseek-chat",
	}

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "private",
		UserID:      987654,
		MessageID:   108,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"搜索资料"}}]`),
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	var action model.OneBotAction
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	params, ok := action.Params.(map[string]interface{})
	if !ok {
		t.Fatalf("params 不是 map: %T", action.Params)
	}
	replyMsg, _ := params["message"].(string)

	if mockTool.executed != 0 {
		t.Errorf("Tool Call commit 失败时严禁执行工具，实际执行次数=%d", mockTool.executed)
	}
	if !strings.Contains(replyMsg, "计费结算失败") && !strings.Contains(replyMsg, "已终止后续工具执行") {
		t.Errorf("期望包含结算失败终止提示，实际回复: %s", replyMsg)
	}
	t.Logf("✅ Tool Call commit 失败禁止执行工具测试通过: %s", replyMsg)
}

func TestWSBilling_Vision_InsufficientBalance_EarlyAbort(t *testing.T) {
	alcyoneSrv, state := mockAlcyoneBillingServer(t)
	defer alcyoneSrv.Close()

	state.balanceHandler = func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": billing.BalanceResult{
				Exists:       true,
				Platform:     "qq",
				ExternalID:   "987654",
				BalanceMinor: 0, // 0 balance
			},
		})
	}

	mockLLM := &mockLLMProvider{}
	engine := newTestEngine(mockLLM)
	billingClient := billing.NewClient(alcyoneSrv.URL, "test-token", 2*time.Second)
	engine.BillingClient = billingClient
	engine.BillingConfig = billing.Config{
		Enabled:          true,
		BaseURL:          alcyoneSrv.URL,
		Timeout:          2 * time.Second,
		MaxOutputTokens:  2048,
		SafetyMultiplier: 1.2,
		ModelName:        "deepseek-chat",
	}

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "private",
		UserID:      987654,
		MessageID:   109,
		Message:     json.RawMessage(`[{"type":"image","data":{"file":"http://example.com/test.jpg"}}]`),
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	var action model.OneBotAction
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	params, ok := action.Params.(map[string]interface{})
	if !ok {
		t.Fatalf("params 不是 map: %T", action.Params)
	}
	replyMsg, _ := params["message"].(string)

	if mockLLM.reqCount != 0 {
		t.Errorf("视觉快速失败拦截时不应调用 LLM，实际调用次数=%d", mockLLM.reqCount)
	}
	if state.reserveCount != 0 {
		t.Errorf("视觉快速失败拦截时不应发生 reserve，实际=%d", state.reserveCount)
	}
	if !strings.Contains(replyMsg, "余额不足") {
		t.Errorf("期望收到余额不足提示，实际回复: %s", replyMsg)
	}
	t.Logf("✅ 视觉前置余额检查与快速失败测试通过: %s", replyMsg)
}

func TestWSBilling_OversizedInput_EarlyAbort(t *testing.T) {
	alcyoneSrv, state := mockAlcyoneBillingServer(t)
	defer alcyoneSrv.Close()

	mockLLM := &mockLLMProvider{}
	engine := newTestEngine(mockLLM)
	billingClient := billing.NewClient(alcyoneSrv.URL, "test-token", 2*time.Second)
	engine.BillingClient = billingClient
	engine.BillingConfig = billing.Config{
		Enabled:          true,
		BaseURL:          alcyoneSrv.URL,
		Timeout:          2 * time.Second,
		MaxOutputTokens:  2048,
		SafetyMultiplier: 1.2,
		ModelName:        "deepseek-chat",
	}

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	oversizedText := strings.Repeat("超长消息测试", 6000) // ~36,000 runes
	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "private",
		UserID:      987654,
		MessageID:   110,
		Message:     json.RawMessage(fmt.Sprintf(`[{"type":"text","data":{"text":%q}}]`, oversizedText)),
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	var action model.OneBotAction
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	params, ok := action.Params.(map[string]interface{})
	if !ok {
		t.Fatalf("params 不是 map: %T", action.Params)
	}
	replyMsg, _ := params["message"].(string)

	if mockLLM.reqCount != 0 {
		t.Errorf("超长单条消息拦截时不应调用 LLM，实际调用次数=%d", mockLLM.reqCount)
	}
	if state.reserveCount != 0 {
		t.Errorf("超长单条消息拦截时不应发生 reserve")
	}
	if !strings.Contains(replyMsg, "单条消息长度过长") {
		t.Errorf("期望收到单条消息过长提示，实际回复: %s", replyMsg)
	}
	t.Logf("✅ 单条超大输入拒绝拦截测试通过: %s", replyMsg)
}
