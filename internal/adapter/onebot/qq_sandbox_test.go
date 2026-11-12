package onebot

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/qqextend"
	"github.com/GuaiZai233/FrostAgent/internal/sandbox"
	"github.com/GuaiZai233/FrostAgent/internal/sandbox/codeinterpreter"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

// Exercises the production sandbox HTTP client and current-source UUID mapping.
// The gateway fixture supplies files independently of the host filesystem.
func sandboxArtifactGateway(t *testing.T, files map[string][]byte, generate func() error) (*codeinterpreter.Client, <-chan string) {
	t.Helper()
	var mu sync.Mutex
	ready := generate == nil
	sessions := make(chan string, 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/shell/exec" || r.Header.Get("X-Auth-Token") != "fixture-token" {
			http.Error(w, "invalid sandbox request", http.StatusBadRequest)
			return
		}
		sessions <- r.URL.Query().Get("user_uuid")
		var req struct {
			Command string `json:"command"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		var output any
		mu.Lock()
		defer mu.Unlock()
		if req.Command == "python3 /sandbox/generate_artifacts.py" && generate != nil {
			if err := generate(); err != nil {
				http.Error(w, "generation failed", 500)
				return
			}
			ready = true
			output = "created /sandbox/chart.png, /sandbox/report.pdf and /sandbox/table.csv"
		} else {
			start := strings.LastIndex(req.Command, " '")
			if start < 0 || !strings.HasSuffix(req.Command, "'") {
				http.Error(w, "invalid file command", 400)
				return
			}
			encoded, err := base64.StdEncoding.DecodeString(req.Command[start+2 : len(req.Command)-1])
			if err != nil {
				http.Error(w, "invalid file request", 400)
				return
			}
			var q struct {
				Path   string `json:"path"`
				Offset int    `json:"offset"`
				Limit  int64  `json:"limit"`
			}
			if json.Unmarshal(encoded, &q) != nil {
				http.Error(w, "invalid file JSON", 400)
				return
			}
			data, ok := files[q.Path]
			if !ready || !ok || q.Offset < 0 || q.Offset > len(data) {
				output = map[string]any{"error": "unreadable"}
			} else if int64(len(data)) > q.Limit {
				output = map[string]any{"error": "size_limit"}
			} else {
				end := min(q.Offset+(384<<10), len(data))
				output = map[string]any{"size": len(data), "fingerprint": "fixture-stable-" + q.Path, "data": base64.StdEncoding.EncodeToString(data[q.Offset:end])}
			}
			dataJSON, _ := json.Marshal(output)
			output = string(dataJSON)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"stdout": output, "stderr": "", "exit_code": 0, "timed_out": false, "stdout_truncated": false, "stderr_truncated": false})
	}))
	t.Cleanup(server.Close)
	client := codeinterpreter.New(sandbox.Config{BaseURL: server.URL, AuthToken: "fixture-token", SessionNamespace: "artifact-fixture"}, codeinterpreter.WithHTTPClient(server.Client()))
	return client, sessions
}

func TestQQURLToSandboxArtifactsThroughTools(t *testing.T) {
	image := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("fixture-image-data"), 40000)...)
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(image)
	}))
	defer imageServer.Close()
	imageURL := imageServer.URL + "/direct-image?id=fixture"
	files := map[string][]byte{"chart.png": image, "report.pdf": []byte("%PDF-1.7\nfixture PDF bytes"), "table.csv": []byte("name,value\nfixture,42\n")}
	client, sessions := sandboxArtifactGateway(t, files, func() error {
		response, err := http.Get(imageURL)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil || !bytes.Equal(data, image) {
			return errors.New("download did not return the image")
		}
		return nil
	})
	finished := make(chan struct{})
	calls := 0
	provider := &mockLLMProvider{customChat: func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		calls++
		transcript, _ := json.Marshal(req.Messages)
		if bytes.Contains(transcript, []byte("base64://")) || bytes.Contains(transcript, []byte(base64.StdEncoding.EncodeToString(image[:300]))) {
			t.Error("file bytes leaked into model context")
		}
		message := core.ChatMessage{Role: core.RoleAssistant}
		switch calls {
		case 1:
			message.ToolCalls = []core.ToolCall{{ID: "generate", Type: "function", Function: core.ToolCallFunction{Name: "execute_command", Arguments: `{"command":"python3 /sandbox/generate_artifacts.py"}`}}}
		case 2:
			args, _ := json.Marshal(map[string]any{"messages": []tools.Msg{{Type: "image", URL: imageURL}, {Type: "image", Path: "/sandbox/chart.png"}, {Type: "file", Path: "sandbox:/sandbox/report.pdf"}, {Type: "file", Path: "/sandbox/table.csv"}}})
			message.ToolCalls = []core.ToolCall{{ID: "deliver", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: string(args)}}}
		case 3:
			result, _ := req.Messages[len(req.Messages)-1].Content.(string)
			items := qqItems(t, result)
			if len(items) != 4 {
				t.Errorf("missing artifact results: %s", result)
			}
			for _, item := range items {
				if item.Status != "confirmed" {
					t.Errorf("artifact not confirmed: %+v", item)
				}
			}
			close(finished)
		default:
			t.Errorf("unexpected model iteration %d", calls)
		}
		return &core.ChatResponse{Message: message}, nil
	}}
	engine := newTestEngine(provider)
	engine.SandboxBackend = client
	engine.ToolRegistry["execute_command"] = tools.ExecuteCommandTool(client)
	engine.ToolRegistry["send_message"] = tools.SendMsgTool()
	server, address := startReplyWSTestServer(t, engine)
	defer server.Close()
	ws, _, err := websocket.DefaultDialer.Dial(address, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	var writeMu sync.Mutex
	write := func(v any) error { writeMu.Lock(); defer writeMu.Unlock(); return ws.WriteJSON(v) }
	actions := make(chan model.OneBotAction, 8)
	readErrors := make(chan error, 1)
	go func() {
		index := 0
		for {
			var action model.OneBotAction
			if err := ws.ReadJSON(&action); err != nil {
				readErrors <- err
				return
			}
			actions <- action
			index++
			if err := write(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 800 + index}}); err != nil {
				readErrors <- err
				return
			}
		}
	}()
	event := model.OneBotEvent{PostType: "message", MessageType: "private", SelfID: testBotID, UserID: testUserID, MessageID: 101, Message: json.RawMessage(`[{"type":"text","data":{"text":"send the image and generated files"}}]`)}
	if err := write(event); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case err := <-readErrors:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("sandbox artifact delivery timed out")
	}
	for index := 0; index < 3; index++ {
		action := <-actions
		params := action.Params.(map[string]any)
		if index == 0 {
			data, _ := json.Marshal(params["message"])
			var segments []tools.OneBotSegment
			if err := json.Unmarshal(data, &segments); err != nil {
				t.Fatal(err)
			}
			if action.Action != "send_private_msg" || len(segments) != 2 || segments[0].Type != "image" || segments[0].Data["file"] != imageURL || segments[1].Data["file"] != "base64://"+base64.StdEncoding.EncodeToString(image) {
				t.Fatal("image URL or sandbox image was not sent as an image")
			}
		} else {
			name := []string{"report.pdf", "table.csv"}[index-1]
			if action.Action != "upload_private_file" || params["name"] != name || params["file"] != "base64://"+base64.StdEncoding.EncodeToString(files[name]) {
				t.Fatalf("artifact upload is incorrect: action=%s name=%v", action.Action, params["name"])
			}
		}
	}
	for len(sessions) > 0 {
		if got := <-sessions; got != client.SessionIDToUUID(fmt.Sprintf("private:%d", testUserID)) {
			t.Fatalf("sandbox source session changed: %s", got)
		}
	}
}

func TestQQCrossSessionSandboxUsesSource(t *testing.T) {
	for _, source := range []string{"private", "group"} {
		t.Run(source, func(t *testing.T) {
			data := []byte("%PDF-1.7\nsource session artifact")
			client, sessions := sandboxArtifactGateway(t, map[string][]byte{"report.pdf": data}, nil)
			actions := make(chan model.OneBotAction, 2)
			conn := qqCrossSessionConnection(t, func(a model.OneBotAction) oneBotAPIResponse { actions <- a; return qqOK(201) })
			conn.Scope = newTestScopeWithEnv(t, map[string]string{qqextend.TargetGroupsKey: "42"})
			defer conn.Scope.Cancel()
			engine := newTestEngine(nil)
			engine.Scope, engine.SandboxBackend = conn.Scope, client
			event := model.OneBotEvent{MessageType: source, GroupID: testGroupID, UserID: testUserID, SelfID: testBotID, MessageID: 101}
			result, err := conn.qqActionHook(event, nil, nil, engine)(context.Background(), `{"action":"send","arguments":{"target_group_id":"42","messages":[{"type":"file","path":"/sandbox/report.pdf"}]}}`)
			if err != nil || result.MessagesSent != 1 || result.ExternalMessagesSent != 1 {
				t.Fatalf("cross-session artifact: %+v %v", result, err)
			}
			action := <-actions
			params := action.Params.(map[string]any)
			if action.Action != "upload_group_file" || fmt.Sprint(params["group_id"]) != "42" || params["file"] != "base64://"+base64.StdEncoding.EncodeToString(data) {
				t.Fatal("sandbox artifact sent to incorrect target")
			}
			if got := <-sessions; got != client.SessionIDToUUID(conn.historyKey(event)) {
				t.Fatal("read used target sandbox instead of source sandbox")
			}
		})
	}
}

func TestQQSandboxPreflightAndBudget(t *testing.T) {
	var limits []int64
	sender := qqSender{event: model.OneBotEvent{MessageType: "private"}, sandboxFile: func(name string, limit int64) ([]byte, error) {
		limits = append(limits, limit)
		return []byte("data"), nil
	}}
	if _, err := sender.prepare([]tools.Msg{{Type: "image", Path: "/sandbox/image.png"}, {Type: "video", Path: "/sandbox/video.mp4", Thumb: "sandbox:///sandbox/thumb.png"}}); err != nil {
		t.Fatal(err)
	}
	if len(limits) != 3 || limits[0] != maxQQInlineBytes || limits[1] != maxQQInlineBytes-4 || limits[2] != maxQQInlineBytes-8 {
		t.Fatalf("sandbox transfer budget not shared: %v", limits)
	}
	for _, mode := range []string{"unreadable", "disabled", "shared", "traversal", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			s := qqSender{event: model.OneBotEvent{MessageType: "private"}, call: func(model.OneBotAction) (oneBotAPIResponse, error) {
				t.Fatal("sent before complete preflight")
				return oneBotAPIResponse{}, nil
			}}
			m := tools.Msg{Type: "file", Path: "/sandbox/report.pdf"}
			s.sandboxFile = func(string, int64) ([]byte, error) { return nil, errors.New("fixture missing file") }
			switch mode {
			case "disabled":
				s.sandboxFile = nil
			case "shared":
				m.Transfer = "shared"
			case "traversal":
				m.Path = "/sandbox/../host"
			case "oversize":
				s.sandboxFile = func(string, int64) ([]byte, error) { return []byte("1234"), nil }
				if _, _, err := s.sandboxSource(m.Path, "auto", 3); err == nil {
					t.Fatal("oversize sandbox read accepted")
				}
				return
			}
			if _, err := s.send([]tools.Msg{{Type: "plain", Text: "prefix"}, m}); err == nil {
				t.Fatal("invalid sandbox file accepted")
			}
		})
	}
}

func TestQQSandboxAndLocalFallbackShareWholeSendBudget(t *testing.T) {
	local := filepath.Join(t.TempDir(), "local.pdf")
	file, err := os.Create(local)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(20 << 20); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	calls := 0
	sender := qqFixtureSender(func(action model.OneBotAction) (oneBotAPIResponse, error) {
		calls++
		if calls == 1 {
			return qqOK(201), nil
		}
		return qqReject("file not found")
	})
	sender.sandboxFile = func(string, int64) ([]byte, error) { return make([]byte, 20<<20), nil }
	result, err := sender.send([]tools.Msg{{Type: "file", Path: "/sandbox/report.pdf"}, {Type: "file", Path: local}})
	if err != nil {
		t.Fatal(err)
	}
	items := qqItems(t, result.Content)
	if calls != 2 || result.MessagesSent != 1 || len(items) != 2 || items[0].Status != "confirmed" || items[1].Status != "rejected" || !strings.Contains(items[1].Error, "inline fallback") {
		t.Fatalf("fallback exceeded whole-send budget: calls=%d result=%+v", calls, result)
	}
}

func TestQQSandboxBudgetCountsLocalPaddingExactly(t *testing.T) {
	local := filepath.Join(t.TempDir(), "local.png")
	if err := os.WriteFile(local, []byte{1}, 0600); err != nil {
		t.Fatal(err)
	}
	sender := qqFixtureSender(nil)
	sender.sandboxFile = func(_ string, limit int64) ([]byte, error) {
		if limit != maxQQInlineBytes-1 {
			t.Fatalf("base64 padding consumed file budget: %d", limit)
		}
		return make([]byte, int(limit)), nil
	}
	parts, err := sender.prepare([]tools.Msg{{Type: "image", Path: local, Transfer: "inline"}, {Type: "file", Path: "/sandbox/report.pdf"}})
	if err != nil || len(parts) != 2 || *parts[0].inlineRemaining != 0 {
		t.Fatalf("exact whole-send limit rejected: parts=%d err=%v", len(parts), err)
	}
}
