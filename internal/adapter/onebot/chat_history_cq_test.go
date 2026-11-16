package onebot

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/backup"
	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/model"
)

func TestHistoryMessageTextCQ(t *testing.T) {
	for _, tt := range []struct {
		name    string
		message any
		text    string
		replyTo string
	}{
		{
			name: "media parameters",
			message: "hello[CQ:image,file=https://media.invalid/private?token=fixture-secret&#44;next=value]" +
				"middle[CQ:record,file=base64://Zml4dHVyZS1zZWNyZXQ=]" +
				"[CQ:video,file=file:///fixture/private.mp4][CQ:file,file=fixture-path]" +
				"[CQ:json,data={&#91;fixture&#93;\nkey=value}]end",
			text: "hello[image]middle[record][video][file][json]end",
		},
		{
			name:    "mention and reply",
			message: "[CQ:reply,id=17][CQ:at,qq=42]hello &amp; &#91;text&#93; &#44;[CQ:face,id=1]",
			text:    "[@42]hello & [text] &#44;[face]",
			replyTo: "17",
		},
		{
			name:    "parameter escaping once",
			message: "[CQ:reply,id=fixture&#44;id=one&amp;#91;][CQ:at,qq=all]",
			text:    "[@all]",
			replyTo: "fixture,id=one&#91;",
		},
		{
			name:    "escaped literal CQ",
			message: "&#91;CQ:image,file=literal&#93; &amp;#91;CQ:record,file=literal&amp;#93;",
			text:    "[CQ:image,file=literal] &#91;CQ:record,file=literal&#93;",
		},
		{
			name:    "ordinary URL",
			message: "See https://example.invalid/fixture?a=b=c for details",
			text:    "See https://example.invalid/fixture?a=b=c for details",
		},
		{
			name: "array text is literal",
			message: []map[string]any{
				{"type": "text", "data": map[string]any{"text": "[CQ:image,file=literal] &amp; &#91;text&#93;"}},
				{"type": "image", "data": map[string]any{"file": "base64://fixture"}},
			},
			text: "[CQ:image,file=literal] &amp; &#91;text&#93;[image]",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.message)
			if err != nil {
				t.Fatal(err)
			}
			text, replyTo := historyMessageText(raw)
			if text != tt.text || replyTo != tt.replyTo {
				t.Fatalf("archive = (%q, %q), want (%q, %q)", text, replyTo, tt.text, tt.replyTo)
			}
		})
	}
}

func TestOneBotHistoryCQMediaPersistence(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	engine.Scope = newTestScopeWithEnv(t, map[string]string{chathistory.GroupWhitelistKey: "42"})
	db := enableHistoryTest(t, engine)
	c := &wsConnection{engine: engine}
	message := "原文 keyword[CQ:reply,id=17][CQ:at,qq=43]" +
		"[CQ:image,file=https://media.invalid/signed?token=fixture-secret&#44;part=one]" +
		"[CQ:record,file=base64://Zml4dHVyZS1zZWNyZXQ=]尾文"
	want := "原文 keyword[@43][image][record]尾文"
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	c.recordIncomingHistory(model.OneBotEvent{
		PostType: "message", MessageType: "group", GroupID: 42, UserID: 43,
		MessageID: 1, Message: raw,
	})
	expected := map[string]string{"1": want}
	for i, action := range []model.OneBotAction{
		{Action: "send_private_msg", Params: map[string]any{"user_id": 42, "message": message}},
		{Action: "send_group_msg", Params: map[string]any{"group_id": 42, "message": message}},
		{Action: "send_msg", Params: map[string]any{"user_id": 42, "message": message, "auto_escape": false}},
		{Action: "send_group_forward_msg", Params: map[string]any{"group_id": 42, "messages": []map[string]any{
			{"type": "node", "data": map[string]any{"content": message}},
		}}},
	} {
		id := strconv.Itoa(i + 2)
		c.recordHistoryAction(action, oneBotAPIResponse{Data: json.RawMessage(`{"message_id":` + id + `}`)}, "delivered")
		expected[id] = want
		if action.Action == "send_group_forward_msg" {
			expected[id] = "[forward]" + want
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := engine.ChatHistory.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := db.SQL.QueryContext(ctx, `SELECT message_id, content, search_text, reply_to FROM chat_history`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, text, searchText, replyTo string
		if err := rows.Scan(&id, &text, &searchText, &replyTo); err != nil {
			t.Fatal(err)
		}
		if text != expected[id] || searchText != strings.ToLower(expected[id]) {
			t.Errorf("persisted message %s = (%q, %q), want %q", id, text, searchText, expected[id])
		}
		if id != "5" && replyTo != "17" {
			t.Errorf("persisted message %s lost reply: %q", id, replyTo)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if count != len(expected) {
		t.Fatalf("persisted %d messages, want %d", count, len(expected))
	}
	for _, scope := range []chathistory.Scope{
		{Platform: "qq", MessageType: "group", TargetID: "42"},
		{Platform: "qq", MessageType: "private", TargetID: "42"},
	} {
		found, err := engine.ChatHistory.Store.Search(ctx, scope, chathistory.Query{Keywords: []string{"keyword"}})
		if err != nil || len(found) == 0 {
			t.Fatalf("original text is not searchable: %v, %v", found, err)
		}
		found, err = engine.ChatHistory.Store.Search(ctx, scope, chathistory.Query{
			Keywords: []string{"media.invalid", "fixture-secret", "Zml4dHVyZS1zZWNyZXQ"}, Match: "any",
		})
		if err != nil || len(found) != 0 {
			t.Fatalf("media payload is searchable: %v, %v", found, err)
		}
	}
	archive, err := backup.BuildInstanceZIP(db, "fixture-instance", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := backup.DecodeInstanceZIP(archive)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ChatHistory == nil || len(decoded.ChatHistory.Records) != len(expected) {
		t.Fatalf("backup lost chat history: %#v", decoded.ChatHistory)
	}
	for _, record := range decoded.ChatHistory.Records {
		if record.Content != expected[record.MessageID] {
			t.Errorf("backup content = %q, want %q", record.Content, expected[record.MessageID])
		}
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range reader.File {
		stream, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(stream)
		closeErr := stream.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("read ZIP member %s: %v, %v", member.Name, err, closeErr)
		}
		for _, secret := range []string{"media.invalid", "fixture-secret", "base64://", "Zml4dHVyZS1zZWNyZXQ"} {
			if bytes.Contains(data, []byte(secret)) {
				t.Errorf("ZIP member %s contains media payload %q", member.Name, secret)
			}
		}
	}
}

func TestOneBotHistoryAutoEscape(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	enableHistoryTest(t, engine)
	c := &wsConnection{engine: engine}
	literal := "[CQ:reply,id=17][CQ:image,file=literal] &amp; &#91;text&#93;"
	c.recordHistoryAction(model.OneBotAction{
		Action: "send_private_msg", Params: map[string]any{"user_id": 42, "message": literal, "auto_escape": true},
	}, oneBotAPIResponse{Data: json.RawMessage(`{"message_id":1}`)}, "delivered")
	c.recordHistoryAction(model.OneBotAction{
		Action: "send_private_msg", Params: map[string]any{"user_id": 42, "auto_escape": true, "message": []map[string]any{
			{"type": "text", "data": map[string]any{"text": literal}},
			{"type": "image", "data": map[string]any{"file": "base64://fixture-media"}},
		}},
	}, oneBotAPIResponse{Data: json.RawMessage(`{"message_id":2}`)}, "delivered")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := engine.ChatHistory.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	records, err := engine.ChatHistory.Store.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("auto_escape archive: %#v", records)
	}
	expected := map[string]string{"1": literal, "2": literal + "[image]"}
	for _, record := range records {
		if record.Content != expected[record.MessageID] {
			t.Errorf("auto_escape content = %q, want %q", record.Content, expected[record.MessageID])
		}
		if record.ReplyTo != "" {
			t.Errorf("literal reply CQ became reply metadata: %q", record.ReplyTo)
		}
	}
}
