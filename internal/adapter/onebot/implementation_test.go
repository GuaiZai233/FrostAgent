package onebot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

func TestPlatformAppendsMentionSpaceMatchesOnlyReportedSnowLuma(t *testing.T) {
	for name, want := range map[string]bool{
		"SnowLuma":        true,
		" SnowLuma ":      true,
		"snowluma":        false,
		"Blubfish":        false,
		"NapCat.Onebot":   false,
		"Lagrange.OneBot": false,
		"":                false,
	} {
		if got := platformAppendsMentionSpace(name); got != want {
			t.Errorf("platformAppendsMentionSpace(%q) = %v, want %v", name, got, want)
		}
	}
}

func mentionAction(name string, texts ...string) model.OneBotAction {
	segments := []tools.OneBotSegment{{Type: "at", Data: map[string]any{"qq": "12"}}}
	for _, text := range texts {
		segments = append(segments, tools.OneBotSegment{Type: "text", Data: map[string]any{"text": text}})
	}
	return model.OneBotAction{Action: name, Params: map[string]any{"group_id": 303, "message": segments}}
}

func messageTexts(t *testing.T, action model.OneBotAction) []string {
	t.Helper()
	segments := action.Params.(map[string]any)["message"].([]tools.OneBotSegment)
	var texts []string
	for _, segment := range segments {
		if text, ok := segment.Data["text"].(string); ok {
			texts = append(texts, text)
		}
	}
	return texts
}

func TestAdaptMentionSpacing(t *testing.T) {
	enabled := &wsConnection{}
	enabled.peerMentionSpace.Store(true)

	for _, tc := range []struct {
		name  string
		texts []string
		want  []string
	}{
		{"space", []string{" 正文", " 后续"}, []string{"正文", " 后续"}},
		{"tab and unicode space", []string{"\t　正文"}, []string{"正文"}},
		{"newline kept", []string{"\n正文"}, []string{"\n正文"}},
		{"leading spaces before newline", []string{"  \n正文"}, []string{"\n正文"}},
		{"already tight", []string{"正文"}, []string{"正文"}},
		{"blank untouched", []string{" "}, []string{" "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := mentionAction("send_group_msg", tc.texts...)
			before := messageTexts(t, original)
			got := messageTexts(t, enabled.adaptMentionSpacing(original))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if !reflect.DeepEqual(messageTexts(t, original), before) {
				t.Fatal("caller segments were mutated")
			}
		})
	}

	t.Run("disabled by default", func(t *testing.T) {
		action := mentionAction("send_group_msg", " 正文")
		if got := messageTexts(t, (&wsConnection{}).adaptMentionSpacing(action)); got[0] != " 正文" {
			t.Fatalf("default connection changed text: %q", got)
		}
		var nilConn *wsConnection
		if got := messageTexts(t, nilConn.adaptMentionSpacing(action)); got[0] != " 正文" {
			t.Fatalf("nil connection changed text: %q", got)
		}
	})
	t.Run("only text directly after an at", func(t *testing.T) {
		action := model.OneBotAction{Action: "send_group_msg", Params: map[string]any{"message": []tools.OneBotSegment{
			{Type: "text", Data: map[string]any{"text": " 前文"}},
			{Type: "text", Data: map[string]any{"text": " 次文"}},
			{Type: "image", Data: map[string]any{"file": "synthetic"}},
			{Type: "text", Data: map[string]any{"text": " 图后"}},
		}}}
		if got := messageTexts(t, enabled.adaptMentionSpacing(action)); !reflect.DeepEqual(got, []string{" 前文", " 次文", " 图后"}) {
			t.Fatalf("unrelated text changed: %q", got)
		}
	})
	t.Run("other actions", func(t *testing.T) {
		action := mentionAction("send_group_forward_msg", " 正文")
		if got := messageTexts(t, enabled.adaptMentionSpacing(action)); got[0] != " 正文" {
			t.Fatalf("non-message action changed: %q", got)
		}
	})
}

// A fake peer that answers get_version_info makes the real connection adapt (or
// keep) the spacing it puts on the wire.
func TestImplementationProbeControlsMentionSpacingOnTheWire(t *testing.T) {
	implementationProbeEnabled = true
	defer func() { implementationProbeEnabled = false }()

	for _, tc := range []struct {
		appName string
		want    string
	}{
		{"SnowLuma", "正文"},
		{"NapCat.Onebot", " 正文"},
	} {
		t.Run(tc.appName, func(t *testing.T) {
			adapter := NewAdapter(newTestEngine(&mockLLMProvider{}))
			server := httptest.NewServer(http.HandlerFunc(adapter.Handler()))
			defer server.Close()
			client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()

			client.SetReadDeadline(time.Now().Add(3 * time.Second))
			var probe model.OneBotAction
			if err := client.ReadJSON(&probe); err != nil || probe.Action != "get_version_info" {
				t.Fatalf("expected get_version_info probe, got %+v (%v)", probe, err)
			}
			reply := map[string]any{"status": "ok", "retcode": 0, "echo": probe.Echo,
				"data": map[string]any{"app_name": tc.appName, "app_version": "1", "protocol_version": "v11"}}
			if err := client.WriteJSON(reply); err != nil {
				t.Fatal(err)
			}

			var conn *wsConnection
			deadline := time.Now().Add(3 * time.Second)
			for conn = adapter.awarenessConnection(); conn == nil && time.Now().Before(deadline); conn = adapter.awarenessConnection() {
				time.Sleep(5 * time.Millisecond)
			}
			if conn == nil {
				t.Fatal("connection never registered")
			}
			for tc.appName == "SnowLuma" && !conn.peerMentionSpace.Load() && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			// A synthetic, undeliverable send is enough: only the frame matters.
			go conn.SendActionAndWait(mentionAction("send_group_msg", " 正文"), 200*time.Millisecond)
			var sent struct {
				Params struct {
					Message []struct {
						Type string         `json:"type"`
						Data map[string]any `json:"data"`
					} `json:"message"`
				} `json:"params"`
			}
			client.SetReadDeadline(time.Now().Add(3 * time.Second))
			if err := client.ReadJSON(&sent); err != nil {
				t.Fatal(err)
			}
			if len(sent.Params.Message) != 2 || sent.Params.Message[1].Data["text"] != tc.want {
				data, _ := json.Marshal(sent)
				t.Fatalf("wire message = %s, want text %q", data, tc.want)
			}
		})
	}
}
