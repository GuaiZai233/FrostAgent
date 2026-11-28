package onebot

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/parity"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/gorilla/websocket"
)

func TestOneBotMediaRouting(t *testing.T) {
	wav := base64.StdEncoding.EncodeToString([]byte("RIFF\x24\x00\x00\x00WAVEfmt synthetic-voice"))
	mp3 := base64.StdEncoding.EncodeToString([]byte("ID3\x04\x00synthetic-transcoded-voice"))
	inlineVoice := `[{"type":"record","data":{"file":"base64://` + wav + `"}}]`
	remoteVoice := `[{"type":"record","data":{"file":"synthetic-voice.amr"}}]`
	for _, tc := range []struct {
		name         string
		message      string
		capabilities []string
		mediaRoute   bool
		getRecord    map[string]any // nil: the action fails
		wantFormat   string         // input_audio format seen by the consuming model
		wantData     string
		wantGuidance string
	}{
		{name: "placeholder", message: inlineVoice, wantGuidance: parity.NoMediaGuidance},
		{name: "dialogue audio", message: inlineVoice, capabilities: []string{"audio"}, wantFormat: "wav", wantData: wav},
		{name: "dialogue without video capability", message: `[{"type":"video","data":{"file":"base64://` + wav + `"}}]`, capabilities: []string{"audio"}, wantGuidance: parity.MediaUnavailableGuidance},
		{name: "dedicated media", message: inlineVoice, mediaRoute: true, wantFormat: "wav", wantData: wav},
		{name: "transcoded record", message: remoteVoice, mediaRoute: true, getRecord: map[string]any{"base64": mp3, "file": "/synthetic/onebot/voice.mp3"}, wantFormat: "mp3", wantData: mp3},
		{name: "unreadable record", message: remoteVoice, mediaRoute: true, wantGuidance: parity.MediaUnavailableGuidance},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialogue := &mockLLMProvider{}
			media := &mockLLMProvider{responses: []*core.ChatResponse{{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "说话内容：synthetic transcription"}}}}
			engine := newTestEngine(dialogue)
			engine.MediaProvider = media
			engine.Scope = newTestScopeWithEnv(t, map[string]string{"BOT_NAME": "synthetic-bot", "BOT_ALIASES": ""})
			engine.ModelRouter = modelrouter.New(filepath.Join(t.TempDir(), "router.json"), engine.Scope)
			cfg := engine.ModelRouter.Draft()
			cfg.Endpoints = []modelrouter.Endpoint{{ID: "endpoint", DisplayName: "Endpoint", BaseURL: "https://example.com/v1", Enabled: true}}
			cfg.Models = []modelrouter.Model{{ID: "model", DisplayName: "Model", EndpointID: "endpoint", UpstreamModel: "synthetic-model", Enabled: true, Capabilities: append([]string{"text"}, tc.capabilities...)}}
			cfg.GlobalBindings[modelrouter.WorkloadDialogue] = modelrouter.Binding{Mode: modelrouter.BindingModel, ModelID: "model"}
			if tc.mediaRoute {
				cfg.GlobalBindings[modelrouter.WorkloadMedia] = modelrouter.Binding{Mode: modelrouter.BindingModel, ModelID: "model"}
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
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			// Short numeric IDs are synthetic transport fixtures.
			event := model.OneBotEvent{PostType: "message", MessageType: "private", SelfID: 11, UserID: 12, MessageID: 1, Message: json.RawMessage(tc.message)}
			if err := conn.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			for {
				var action model.OneBotAction
				if err := conn.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
				if action.Action == "send_private_msg" {
					_ = conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 2}})
					break
				}
				if action.Action != "get_record" {
					t.Fatalf("unexpected action: %+v", action)
				}
				params := action.Params.(map[string]any)
				if params["out_format"] != "mp3" || params["file"] != "synthetic-voice.amr" {
					t.Fatalf("unexpected get_record params: %+v", params)
				}
				if tc.getRecord == nil {
					_ = conn.WriteJSON(map[string]any{"status": "failed", "retcode": 100, "echo": action.Echo, "data": nil})
					continue
				}
				_ = conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": tc.getRecord})
			}

			dialogue.mu.Lock()
			defer dialogue.mu.Unlock()
			media.mu.Lock()
			defer media.mu.Unlock()
			if len(dialogue.requests) != 1 {
				t.Fatalf("dialogue calls=%d", len(dialogue.requests))
			}
			messages := dialogue.requests[0].Messages
			last := messages[len(messages)-1].Content
			prompt, _ := last.(string)
			if !tc.mediaRoute && tc.wantFormat != "" {
				parts, ok := last.([]core.ContentPart)
				if !ok || len(parts) != 2 || parts[1].InputAudio == nil || parts[1].InputAudio.Format != tc.wantFormat || parts[1].InputAudio.Data != tc.wantData {
					t.Fatalf("dialogue lost audio payload: %+v", last)
				}
				prompt = parts[0].Text
			}
			if _, ok := last.([]core.ContentPart); ok && (tc.mediaRoute || tc.wantFormat == "") {
				t.Fatalf("media must not reach the dialogue model: %+v", last)
			}
			if !strings.Contains(prompt, "[语音]") && !strings.Contains(prompt, "[视频]") {
				t.Fatalf("media placeholder missing: %s", prompt)
			}
			if tc.wantGuidance != "" && !strings.Contains(prompt, tc.wantGuidance) {
				t.Fatalf("guidance %q missing: %s", tc.wantGuidance, prompt)
			}
			if tc.wantGuidance == "" && strings.Contains(prompt, "media_guidance") {
				t.Fatalf("unexpected media guidance: %s", prompt)
			}

			wantMediaCalls := 0
			if tc.mediaRoute && tc.wantFormat != "" {
				wantMediaCalls = 1
				request := media.requests[0].Messages[len(media.requests[0].Messages)-1].Content.([]core.ContentPart)
				if len(request) != 2 || request[1].InputAudio == nil || request[1].InputAudio.Format != tc.wantFormat || request[1].InputAudio.Data != tc.wantData {
					t.Fatalf("media model got wrong payload: %+v", request)
				}
				if !strings.Contains(prompt, llm.MediaResultLabel+"说话内容：synthetic transcription") {
					t.Fatalf("transcription missing from dialogue prompt: %s", prompt)
				}
			}
			if len(media.requests) != wantMediaCalls {
				t.Fatalf("media calls=%d want=%d", len(media.requests), wantMediaCalls)
			}

			sessCore, ok := engine.SessionManager.Get(historyKey(event))
			if !ok {
				t.Fatal("session missing")
			}
			sess := sessCore.(*llm.SessionContext)
			if stored, _ := sess.LastActualRequest(); tc.wantData != "" && strings.Contains(stored, tc.wantData) {
				t.Fatalf("checkpointed request kept audio bytes: %s", stored)
			}
			for _, history := range sess.History {
				if text, _ := history.Content.(string); tc.wantData != "" && strings.Contains(text, tc.wantData) {
					t.Fatalf("history kept audio bytes: %s", text)
				}
			}
		})
	}
}
