package onebot

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/parity"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/gorilla/websocket"
)

func TestOneBotMultimodalFallbackRequest(t *testing.T) {
	for _, tc := range []struct {
		name                string
		native, visionRoute bool
	}{
		{name: "placeholder"},
		{name: "dialogue vision", native: true},
		{name: "dedicated vision", native: true, visionRoute: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialogue, vision := &mockLLMProvider{}, &mockLLMProvider{responses: []*core.ChatResponse{{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "synthetic image description"}}}}
			engine := newTestEngine(dialogue)
			engine.VisionProvider = vision
			engine.Scope = newTestScopeWithEnv(t, map[string]string{"BOT_NAME": "synthetic-bot", "BOT_ALIASES": ""})
			engine.ModelRouter = modelrouter.New(filepath.Join(t.TempDir(), "router.json"), engine.Scope)
			cfg := engine.ModelRouter.Draft()
			cfg.Endpoints = []modelrouter.Endpoint{{ID: "endpoint", DisplayName: "Endpoint", BaseURL: "https://example.com/v1", Enabled: true}}
			cfg.Models = []modelrouter.Model{{ID: "model", DisplayName: "Model", EndpointID: "endpoint", UpstreamModel: "synthetic-model", Enabled: true, Capabilities: []string{"text"}}}
			if tc.native {
				cfg.Models[0].Capabilities = append(cfg.Models[0].Capabilities, "vision")
			}
			cfg.GlobalBindings[modelrouter.WorkloadDialogue] = modelrouter.Binding{Mode: modelrouter.BindingModel, ModelID: "model"}
			cfg.GlobalBindings[modelrouter.WorkloadVision] = modelrouter.Binding{Mode: modelrouter.BindingDisabled}
			if tc.visionRoute {
				cfg.GlobalBindings[modelrouter.WorkloadVision] = modelrouter.Binding{Mode: modelrouter.BindingModel, ModelID: "model"}
			}
			if err := engine.ModelRouter.SaveDraft(cfg); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.ModelRouter.Publish(); err != nil {
				t.Fatal(err)
			}
			srv, url := startReplyWSTestServer(t, engine)
			defer srv.Close()
			conn, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			// Short numeric IDs are synthetic transport fixtures.
			event := model.OneBotEvent{PostType: "message", MessageType: "private", SelfID: 11, UserID: 12, MessageID: 1, Message: json.RawMessage(`[{"type":"image","data":{"file":"base64://R0lGODlh"}}]`)}
			if err := conn.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			var action model.OneBotAction
			if err := conn.ReadJSON(&action); err != nil {
				t.Fatal(err)
			}
			if action.Action != "send_private_msg" {
				t.Fatalf("unexpected action: %+v", action)
			}
			if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 2}}); err != nil {
				t.Fatal(err)
			}
			dialogue.mu.Lock()
			defer dialogue.mu.Unlock()
			if len(dialogue.requests) != 1 {
				t.Fatalf("dialogue calls=%d", len(dialogue.requests))
			}
			messages := dialogue.requests[0].Messages
			last := messages[len(messages)-1].Content
			prompt, _ := last.(string)
			if tc.native && !tc.visionRoute {
				parts, ok := last.([]core.ContentPart)
				if !ok || len(parts) != 2 || parts[1].ImageURL == nil || parts[1].ImageURL.URL != "data:image/gif;base64,R0lGODlh" {
					t.Fatalf("dialogue lost image payload: %+v", last)
				}
				prompt = parts[0].Text
			}
			if !strings.Contains(prompt, "[图片]") || strings.Contains(prompt, parity.NoVisionGuidance) != (!tc.native && !tc.visionRoute) {
				t.Fatalf("incorrect image placeholder or guidance: %s", prompt)
			}
			vision.mu.Lock()
			defer vision.mu.Unlock()
			wantCalls := 0
			if tc.visionRoute {
				wantCalls = 1
				if !strings.Contains(prompt, "synthetic image description") {
					t.Fatalf("vision description missing: %s", prompt)
				}
			}
			if len(vision.requests) != wantCalls {
				t.Fatalf("vision calls=%d want=%d", len(vision.requests), wantCalls)
			}
		})
	}
}
