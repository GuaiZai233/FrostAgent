package onebot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

func qqOK(id int) oneBotAPIResponse {
	return oneBotAPIResponse{Status: "ok", Data: json.RawMessage(fmt.Sprintf(`{"message_id":%d}`, id))}
}
func qqReject(detail string) (oneBotAPIResponse, error) {
	return oneBotAPIResponse{Status: "failed", RetCode: 1400, Wording: detail}, errors.New(detail)
}
func qqFixtureSender(call func(model.OneBotAction) (oneBotAPIResponse, error)) qqSender {
	return qqSender{event: model.OneBotEvent{SelfID: testBotID, UserID: testUserID, GroupID: testGroupID, MessageType: "group"}, call: call, reference: func(id string) bool { return id == "101" }, own: func(id string) bool { return id == "101" }}
}
func qqItems(t *testing.T, result string) []qqItemResult {
	t.Helper()
	var data struct {
		Results []qqItemResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(result), &data); err != nil {
		t.Fatal(err)
	}
	return data.Results
}

func TestQQInteractionVendorFallbacks(t *testing.T) {
	unset := false
	for _, tc := range []struct {
		name    string
		request tools.QQRequest
		names   []string
		key     string
		value   any
	}{
		{"poke", tools.QQRequest{Action: "poke"}, []string{"send_poke", "group_poke"}, "user_id", testUserID},
		{"reaction remove", tools.QQRequest{Action: "reaction", Arguments: tools.QQArguments{MessageID: "101", EmojiID: "14", Set: &unset}}, []string{"set_msg_emoji_like", "set_group_reaction"}, "is_set", false},
		{"forward single", tools.QQRequest{Action: "forward_message", Arguments: tools.QQArguments{MessageID: "101"}}, []string{"forward_group_single_msg", "send_group_forward_msg", "send_forward_msg"}, "group_id", testGroupID},
		{"forward bundle", tools.QQRequest{Action: "forward_bundle", Arguments: tools.QQArguments{Nodes: []tools.QQForwardNode{{MessageID: "101"}, {Messages: []tools.Msg{{Type: "plain", Text: "fixture"}}}}, News: []string{"preview"}}}, []string{"send_group_forward_msg", "send_forward_msg"}, "group_id", testGroupID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			s := qqFixtureSender(func(action model.OneBotAction) (oneBotAPIResponse, error) {
				calls = append(calls, action.Action)
				if len(calls) < len(tc.names) {
					return qqReject("unknown action")
				}
				if action.Params.(map[string]any)[tc.key] != tc.value {
					t.Errorf("fallback params: %+v", action.Params)
				}
				return qqOK(102), nil
			})
			result, err := s.execute(tc.request)
			if err != nil || !reflect.DeepEqual(calls, tc.names) {
				t.Fatalf("calls=%v result=%+v err=%v", calls, result, err)
			}
			items := qqItems(t, result.Content)
			if items[0].Status != "confirmed" || items[0].Fallback == "" {
				t.Fatalf("%+v", items)
			}
		})
	}
}

func TestQQNoFallbackOnUncertainOrPermissionFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response oneBotAPIResponse
		err      error
		status   string
	}{
		{"timeout", oneBotAPIResponse{}, context.DeadlineExceeded, "unknown"},
		{"disconnect", oneBotAPIResponse{}, errors.New("connection closed"), "unknown"},
		{"async", oneBotAPIResponse{Status: "async", RetCode: 1}, errors.New("async"), "unknown"},
		{"permission", oneBotAPIResponse{Status: "failed", RetCode: 100, Wording: "permission denied"}, errors.New("denied"), "rejected"},
		{"ambiguous wording", oneBotAPIResponse{Status: "failed", RetCode: 100, Wording: "send failed"}, errors.New("failed"), "rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := qqFixtureSender(func(model.OneBotAction) (oneBotAPIResponse, error) { calls++; return tc.response, tc.err })
			result, err := s.execute(tools.QQRequest{Action: "poke"})
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || qqItems(t, result.Content)[0].Status != tc.status {
				t.Fatalf("calls=%d result=%s", calls, result.Content)
			}
		})
	}
}

func TestQQConversationAndOwnMessageAuthority(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request tools.QQRequest
		private bool
		own     bool
	}{
		{"cross conversation recall", tools.QQRequest{Action: "recall", Arguments: tools.QQArguments{MessageID: "999"}}, false, true},
		{"other author recall", tools.QQRequest{Action: "recall", Arguments: tools.QQArguments{MessageID: "101"}}, false, false},
		{"cross conversation forward", tools.QQRequest{Action: "forward_message", Arguments: tools.QQArguments{MessageID: "999"}}, false, true},
		{"cross conversation node", tools.QQRequest{Action: "forward_bundle", Arguments: tools.QQArguments{Nodes: []tools.QQForwardNode{{MessageID: "999"}}}}, false, true},
		{"cross private poke", tools.QQRequest{Action: "poke", Arguments: tools.QQArguments{UserID: "10002"}}, true, true},
		{"private reaction", tools.QQRequest{Action: "reaction", Arguments: tools.QQArguments{MessageID: "101", EmojiID: "14"}}, true, true},
		{"private all mention", tools.QQRequest{Action: "send", Arguments: tools.QQArguments{Messages: []tools.Msg{{Type: "mention_all"}}}}, true, true},
		{"untrusted quote", tools.QQRequest{Action: "send", Arguments: tools.QQArguments{Messages: []tools.Msg{{Type: "quote", MessageID: "999"}, {Type: "plain", Text: "fixture"}}}}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := qqFixtureSender(func(model.OneBotAction) (oneBotAPIResponse, error) {
				t.Fatal("untrusted action reached transport")
				return qqOK(102), nil
			})
			if tc.private {
				s.event.MessageType = "private"
			}
			s.own = func(string) bool { return tc.own }
			if _, err := s.execute(tc.request); err == nil {
				t.Fatal("untrusted action accepted")
			}
		})
	}
	c := newWSConnection(nil)
	defer c.Close()
	session := "fixture-session"
	c.rememberMessageSession(101, session)
	if c.ownMessageSessionMatches(101, session) {
		t.Fatal("incoming observation became own message")
	}
	c.rememberActionMessageSession(qqOK(102), session)
	if !c.ownMessageSessionMatches(102, session) || c.ownMessageSessionMatches(102, "other") {
		t.Fatal("sent ownership mismatch")
	}
	c.rememberMessageSession(102, "collision")
	if c.ownMessageSessionMatches(102, session) {
		t.Fatal("collision retained recall authority")
	}
	other := newWSConnection(nil)
	defer other.Close()
	if other.ownMessageSessionMatches(102, session) {
		t.Fatal("reconnected transport inherited authority")
	}
}

func TestQQOrderedPartialResultsAndPreflight(t *testing.T) {
	var calls []string
	confirmed := 0
	s := qqFixtureSender(func(action model.OneBotAction) (oneBotAPIResponse, error) {
		calls = append(calls, action.Action)
		if len(calls) == 2 {
			return oneBotAPIResponse{}, context.DeadlineExceeded
		}
		return qqOK(102), nil
	})
	s.confirmed = func(oneBotAPIResponse, []tools.Msg) { confirmed++ }
	result, err := s.send([]tools.Msg{{Type: "plain", Text: "prefix"}, {Type: "file", URL: "https://example.invalid/file", Name: "fixture.txt", FolderID: "folder-fixture"}, {Type: "plain", Text: "suffix"}})
	if err != nil {
		t.Fatal(err)
	}
	items := qqItems(t, result.Content)
	if len(calls) != 2 || calls[0] != "send_group_msg" || calls[1] != "upload_group_file" || confirmed != 1 || result.MessagesSent != 1 || items[0].Status != "confirmed" || items[1].Status != "unknown" || items[2].Status != "not_sent" {
		t.Fatalf("calls=%v confirmed=%d result=%s", calls, confirmed, result.Content)
	}
	calls = nil
	_, err = s.send([]tools.Msg{{Type: "plain", Text: "prefix"}, {Type: "image", Path: filepath.Join(t.TempDir(), "missing.png")}})
	if err == nil || len(calls) != 0 {
		t.Fatal("preflight allowed partial send before invalid local file")
	}
}

func TestQQFileTransferExplicitPathRejection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.txt")
	if err := os.WriteFile(path, []byte("fixture bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"auto", "shared", "inline"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			s := qqFixtureSender(func(action model.OneBotAction) (oneBotAPIResponse, error) {
				calls++
				params := action.Params.(map[string]any)
				if action.Action != "upload_group_file" || params["name"] != "shown.txt" || params["folder"] != "folder-fixture" || params["folder_id"] != "folder-fixture" {
					t.Fatalf("upload params: %+v", action)
				}
				inline := strings.HasPrefix(params["file"].(string), "base64://")
				if !inline {
					return qqReject("ENOENT: no such file")
				}
				return qqOK(102), nil
			})
			result, err := s.send([]tools.Msg{{Type: "file", Path: path, Name: "shown.txt", FolderID: "folder-fixture", Transfer: mode}})
			if err != nil {
				t.Fatal(err)
			}
			want := 2
			status := "confirmed"
			if mode == "shared" {
				want = 1
				status = "rejected"
			}
			if mode == "inline" {
				want = 1
			}
			if calls != want || qqItems(t, result.Content)[0].Status != status {
				t.Fatalf("mode=%s calls=%d result=%s", mode, calls, result.Content)
			}
		})
	}
}

func TestQQInlineTransferBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparse.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(maxQQInlineBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if _, err := qqInlineFile(path); err == nil {
		t.Fatal("unbounded inline transfer")
	}
	s := qqFixtureSender(func(model.OneBotAction) (oneBotAPIResponse, error) { return qqReject("file not found") })
	result, err := s.send([]tools.Msg{{Type: "file", Path: path}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "30 MiB") {
		t.Fatal(result.Content)
	}
}

func TestQQCardAndMarketFaceFallbacks(t *testing.T) {
	lat, lon := 0.0, 0.0
	for _, tc := range []struct {
		name     string
		msg      tools.Msg
		wantType string
	}{
		{"share", tools.Msg{Type: "share", Data: &tools.QQMessageData{URL: "https://example.invalid", Title: "fixture"}}, "json"},
		{"location", tools.Msg{Type: "location", Data: &tools.QQMessageData{Lat: &lat, Lon: &lon}}, "text"},
		{"market", tools.Msg{Type: "mface", Data: &tools.QQMessageData{EmojiID: "abcdef012345", EmojiPackageID: "42", Key: "fixture-key"}}, "image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := qqFixtureSender(func(action model.OneBotAction) (oneBotAPIResponse, error) {
				calls++
				if calls == 1 {
					return qqReject("unsupported message type")
				}
				segments := action.Params.(map[string]any)["message"].([]tools.OneBotSegment)
				if segments[0].Type != tc.wantType {
					t.Fatalf("fallback: %+v", segments)
				}
				return qqOK(102), nil
			})
			result, err := s.send([]tools.Msg{tc.msg})
			if err != nil || calls != 2 || qqItems(t, result.Content)[0].Status != "confirmed" {
				t.Fatalf("%+v %v calls=%d", result, err, calls)
			}
		})
	}
}

func TestQQRealWebsocketACKAndTimeout(t *testing.T) {
	for _, ack := range []bool{true, false} {
		t.Run(fmt.Sprint(ack), func(t *testing.T) {
			actions := make(chan model.OneBotAction, 4)
			upgrader := websocket.Upgrader{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer ws.Close()
				for {
					var action model.OneBotAction
					if err := ws.ReadJSON(&action); err != nil {
						return
					}
					actions <- action
					if ack {
						response := qqOK(102)
						response.Echo, _ = json.Marshal(action.Echo)
						if err := ws.WriteJSON(response); err != nil {
							return
						}
					}
				}
			}))
			defer srv.Close()
			ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			c := newWSConnection(ws)
			defer c.Close()
			go func() {
				for {
					var response oneBotAPIResponse
					if err := ws.ReadJSON(&response); err != nil {
						return
					}
					var echo string
					_ = json.Unmarshal(response.Echo, &echo)
					c.deliverMessageResponse(echo, response)
				}
			}()
			s := qqFixtureSender(func(action model.OneBotAction) (oneBotAPIResponse, error) {
				return c.SendActionAndWait(action, 30*time.Millisecond)
			})
			result, err := s.execute(tools.QQRequest{Action: "poke"})
			if err != nil {
				t.Fatal(err)
			}
			want := "unknown"
			if ack {
				want = "confirmed"
			}
			if qqItems(t, result.Content)[0].Status != want {
				t.Fatal(result.Content)
			}
			select {
			case action := <-actions:
				if action.Action != "send_poke" {
					t.Fatal(action.Action)
				}
			default:
				t.Fatal("action was not written")
			}
			select {
			case action := <-actions:
				t.Fatalf("unexpected retry: %+v", action)
			default:
			}
		})
	}
}

func TestQQQuotedFileAndCardKeepReplyAttached(t *testing.T) {
	for _, tc := range []struct {
		name     string
		message  tools.Msg
		fallback bool
	}{
		{"file", tools.Msg{Type: "file", URL: "https://example.invalid/file"}, false},
		{"share", tools.Msg{Type: "share", Data: &tools.QQMessageData{URL: "https://example.invalid", Title: "fixture"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := qqFixtureSender(func(action model.OneBotAction) (oneBotAPIResponse, error) {
				calls++
				if action.Action != "send_group_msg" {
					t.Fatalf("reply was lost through %s", action.Action)
				}
				segments := action.Params.(map[string]any)["message"].([]tools.OneBotSegment)
				if len(segments) != 2 || segments[0].Type != "reply" {
					t.Fatalf("orphan reply: %+v", segments)
				}
				if tc.fallback && calls == 1 {
					return qqReject("unsupported message type")
				}
				return qqOK(102), nil
			})
			result, err := s.send([]tools.Msg{{Type: "quote", MessageID: "101"}, tc.message})
			if err != nil {
				t.Fatal(err)
			}
			if items := qqItems(t, result.Content); items[0].Status != "confirmed" || items[1].Status != "confirmed" {
				t.Fatal(result.Content)
			}
		})
	}
	s := qqFixtureSender(func(model.OneBotAction) (oneBotAPIResponse, error) {
		t.Fatal("invalid quote/folder combination partially sent")
		return qqOK(102), nil
	})
	if _, err := s.send([]tools.Msg{{Type: "plain", Text: "prefix"}, {Type: "file", URL: "https://example.invalid/earlier-file"}, {Type: "quote", MessageID: "101"}, {Type: "file", URL: "https://example.invalid/file", FolderID: "folder"}}); err == nil {
		t.Fatal("unsupported quoted folder upload accepted")
	}
}

func TestQQObservedMarketStickerUsesTrustedNativeMetadata(t *testing.T) {
	c := newWSConnection(nil)
	defer c.Close()
	event := model.OneBotEvent{SelfID: testBotID, UserID: testUserID, MessageType: "private", MessageID: 101, Message: json.RawMessage(`[{"type":"mface","data":{"emojiId":"abcdef012345","emojiPackageId":"42","key":"fixture-key","summary":"sample"}}]`)}
	c.rememberMessageSession(101, c.historyKey(event))
	resolved, err := c.resolveMarketFace(context.Background(), event, tools.Msg{Type: "mface", MessageID: "101"})
	if err != nil || resolved.Data == nil || resolved.Data.EmojiID != "abcdef012345" || resolved.Data.Key != "fixture-key" {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	if _, err := c.resolveMarketFace(context.Background(), event, tools.Msg{Type: "mface", MessageID: "999"}); err == nil {
		t.Fatal("unobserved source accepted")
	}
	if _, err := c.resolveMarketFace(context.Background(), event, tools.Msg{Type: "mface", MessageID: "101", StickerIndex: 1}); err == nil {
		t.Fatal("invalid index accepted")
	}
}

func TestQQMixedMediaPathFallbackKeepsOrderedChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.png")
	if err := os.WriteFile(path, []byte("fixture bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s := qqFixtureSender(func(action model.OneBotAction) (oneBotAPIResponse, error) {
		calls++
		segments := action.Params.(map[string]any)["message"].([]tools.OneBotSegment)
		if len(segments) != 3 || segments[0].Type != "text" || segments[1].Type != "image" || segments[2].Type != "text" {
			t.Fatalf("chain reordered: %+v", segments)
		}
		if calls == 1 {
			return qqReject("file not found")
		}
		if !strings.HasPrefix(segments[1].Data["file"].(string), "base64://") {
			t.Fatal("missing inline fallback")
		}
		return qqOK(102), nil
	})
	result, err := s.send([]tools.Msg{{Type: "plain", Text: "before"}, {Type: "image", Path: path}, {Type: "plain", Text: "after"}})
	if err != nil || calls != 2 || result.MessagesSent != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
	}
}
