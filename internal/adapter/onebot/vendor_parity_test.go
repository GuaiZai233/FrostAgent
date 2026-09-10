package onebot

import (
	"FrostAgent/internal/adapter/onebot/content"
	"FrostAgent/internal/core"
	"FrostAgent/internal/model"
	"FrostAgent/internal/sticker"
	"FrostAgent/internal/tools"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const (
	testBotID   int64 = 20001
	testUserID  int64 = 10001
	testGroupID int64 = 30001
)

// ---------------------------------------------------------------------------
// 1. Inbound Parsing & Normalization Parity Matrix
// ---------------------------------------------------------------------------

func TestVendorParity_GroupText(t *testing.T) {
	napcatRaw := []byte(`[{"type":"text","data":{"text":"hello from group"}}]`)
	luckylilliaRaw := []byte(`[{"type":"text","data":{"text":"hello from group"}}]`)

	napcatSegs := ParseMessageSegments(napcatRaw)
	llSegs := ParseMessageSegments(luckylilliaRaw)

	napcatText := extractUserText(napcatSegs, napcatRaw)
	llText := extractUserText(llSegs, luckylilliaRaw)

	if napcatText != llText || napcatText != "hello from group" {
		t.Fatalf("group text mismatch: napcat=%q, luckylillia=%q", napcatText, llText)
	}
}

func TestVendorParity_PrivateText(t *testing.T) {
	napcatRaw := []byte(`[{"type":"text","data":{"text":"hello private"}}]`)
	luckylilliaRaw := []byte(`[{"type":"text","data":{"text":"hello private"}}]`)

	napcatSegs := ParseMessageSegments(napcatRaw)
	llSegs := ParseMessageSegments(luckylilliaRaw)

	napcatText := extractUserText(napcatSegs, napcatRaw)
	llText := extractUserText(llSegs, luckylilliaRaw)

	if napcatText != llText || napcatText != "hello private" {
		t.Fatalf("private text mismatch: napcat=%q, luckylillia=%q", napcatText, llText)
	}
}

func TestVendorParity_At(t *testing.T) {
	napcatEvent := model.OneBotEvent{
		SelfID:      testBotID,
		MessageType: "group",
		Message:     []byte(`[{"type":"at","data":{"qq":"20001"}},{"type":"text","data":{"text":" please help"}}]`),
	}
	llEvent := model.OneBotEvent{
		SelfID:      testBotID,
		MessageType: "group",
		Message:     []byte(`[{"type":"at","data":{"qq":20001}},{"type":"text","data":{"text":" please help"}}]`),
	}

	if !IsMentionedBot(napcatEvent) {
		t.Fatalf("napcat at was not recognized as mentioning bot")
	}
	if !IsMentionedBot(llEvent) {
		t.Fatalf("luckylillia at was not recognized as mentioning bot")
	}

	napcatText := extractUserText(ParseMessageSegments(napcatEvent.Message), napcatEvent.Message)
	llText := extractUserText(ParseMessageSegments(llEvent.Message), llEvent.Message)

	if napcatText != llText || napcatText != "[@20001]  please help" {
		t.Fatalf("at text mismatch: napcat=%q, luckylillia=%q", napcatText, llText)
	}
}

func TestVendorParity_Reply(t *testing.T) {
	napcatEvent := model.OneBotEvent{
		SelfID:      testBotID,
		MessageType: "group",
		Message:     []byte(`[{"type":"reply","data":{"id":"101"}},{"type":"text","data":{"text":"quoting"}}]`),
	}
	llEvent := model.OneBotEvent{
		SelfID:      testBotID,
		MessageType: "group",
		Message:     []byte(`[{"type":"reply","data":{"id":-201}},{"type":"text","data":{"text":"quoting"}}]`),
	}

	napcatMsgID, ok := replyMessageID(napcatEvent)
	if !ok || napcatMsgID != 101 {
		t.Fatalf("napcat reply message id = %d (ok=%v), want 101", napcatMsgID, ok)
	}
	llMsgID, ok := replyMessageID(llEvent)
	if !ok || llMsgID != -201 {
		t.Fatalf("luckylillia reply message id = %d (ok=%v), want -201", llMsgID, ok)
	}

	napcatText := extractUserText(ParseMessageSegments(napcatEvent.Message), napcatEvent.Message)
	llText := extractUserText(ParseMessageSegments(llEvent.Message), llEvent.Message)

	if !strings.Contains(napcatText, "[回复:101]") {
		t.Fatalf("napcat reply text missing placeholder: %q", napcatText)
	}
	if !strings.Contains(llText, "[回复:-201]") {
		t.Fatalf("luckylillia reply text missing placeholder: %q", llText)
	}
}

func TestVendorParity_OrdinaryImage(t *testing.T) {
	napcatRaw := []byte(`[{"type":"image","data":{"url":"https://example.com/photo.png","sub_type":0}}]`)
	llRaw := []byte(`[{"type":"image","data":{"url":"https://example.com/photo.png","subType":0}}]`)

	napcatSegs := ParseMessageSegments(napcatRaw)
	llSegs := ParseMessageSegments(llRaw)

	if !content.IsContainImage(napcatSegs) || !content.IsContainImage(llSegs) {
		t.Fatalf("ordinary image should be recognized as containing image")
	}

	napcatStickers := stickerSourcesFromSegments(napcatSegs)
	llStickers := stickerSourcesFromSegments(llSegs)
	if len(napcatStickers) != 0 || len(llStickers) != 0 {
		t.Fatalf("ordinary image should NOT be recognized as sticker: napcat=%v, ll=%v", napcatStickers, llStickers)
	}

	napcatText := extractUserText(napcatSegs, napcatRaw)
	llText := extractUserText(llSegs, llRaw)
	if napcatText != "[图片]" || llText != "[图片]" {
		t.Fatalf("ordinary image placeholder mismatch: napcat=%q, ll=%q", napcatText, llText)
	}
}

func TestVendorParity_StickerImageSubtype(t *testing.T) {
	// NapCat uses snake_case sub_type=1
	napcatRaw := []byte(`[{"type":"image","data":{"url":"https://example.com/sticker.png","sub_type":1}}]`)
	// LuckyLillia uses camelCase subType=1
	llRaw := []byte(`[{"type":"image","data":{"url":"https://example.com/sticker.png","subType":1}}]`)

	napcatSegs := ParseMessageSegments(napcatRaw)
	llSegs := ParseMessageSegments(llRaw)

	napcatSources := stickerSourcesFromSegments(napcatSegs)
	llSources := stickerSourcesFromSegments(llSegs)

	if len(napcatSources) != 1 || napcatSources[0] != "https://example.com/sticker.png" {
		t.Fatalf("napcat sticker sources = %v, want ['https://example.com/sticker.png']", napcatSources)
	}
	if len(llSources) != 1 || llSources[0] != "https://example.com/sticker.png" {
		t.Fatalf("luckylillia sticker sources = %v, want ['https://example.com/sticker.png']", llSources)
	}

	// Normalization ensures both keys are present and recognized as sticker subtype
	if !isStickerSubType(llSegs[0].Data["sub_type"]) || !isStickerSubType(llSegs[0].Data["subType"]) {
		t.Fatalf("luckylillia segment normalization failed: %+v", llSegs[0].Data)
	}

	napcatText := extractUserText(napcatSegs, napcatRaw)
	llText := extractUserText(llSegs, llRaw)
	if napcatText != "[图片]" || llText != "[图片]" {
		t.Fatalf("sticker text placeholder mismatch: napcat=%q, ll=%q", napcatText, llText)
	}
}

func TestVendorParity_MarketFace(t *testing.T) {
	// NapCat: image with emoji metadata
	napcatRaw := []byte(`[{"type":"image","data":{"url":"https://gxh.vip.qq.com/club/item/parcel/item/12/123456/raw300.gif","emoji_id":"123456","emoji_package_id":789}}]`)
	// LuckyLillia: native mface segment
	llRaw := []byte(`[{"type":"mface","data":{"emoji_id":"123456","emoji_package_id":"789"}}]`)

	napcatSegs := ParseMessageSegments(napcatRaw)
	llSegs := ParseMessageSegments(llRaw)

	// Both must be recognized as image
	if !content.IsContainImage(napcatSegs) || !content.IsContainImage(llSegs) {
		t.Fatalf("market face must be recognized as containing image")
	}

	// Both must be recognized as market face
	if !content.IsMarketFaceSegment(napcatSegs[0]) || !content.IsMarketFaceSegment(llSegs[0]) {
		t.Fatalf("market face segment check failed")
	}

	// Both must resolve to the identical official VIP QQ CDN URL
	wantURL := "https://gxh.vip.qq.com/club/item/parcel/item/12/123456/raw300.gif"
	napcatSrc := content.SegmentImageSource(napcatSegs[0])
	llSrc := content.SegmentImageSource(llSegs[0])
	if napcatSrc != wantURL {
		t.Fatalf("napcat market face url = %q, want %q", napcatSrc, wantURL)
	}
	if llSrc != wantURL {
		t.Fatalf("luckylillia market face url = %q, want %q", llSrc, wantURL)
	}

	// Both must be captured as sticker sources
	napcatStickers := stickerSourcesFromSegments(napcatSegs)
	llStickers := stickerSourcesFromSegments(llSegs)
	if len(napcatStickers) != 1 || napcatStickers[0] != wantURL {
		t.Fatalf("napcat sticker sources = %v", napcatStickers)
	}
	if len(llStickers) != 1 || llStickers[0] != wantURL {
		t.Fatalf("luckylillia sticker sources = %v", llStickers)
	}

	// Canonical placeholder in text extraction must be identical: [图片]
	napcatText := extractUserText(napcatSegs, napcatRaw)
	llText := extractUserText(llSegs, llRaw)
	if napcatText != "[图片]" || llText != "[图片]" {
		t.Fatalf("market face text placeholder mismatch: napcat=%q, ll=%q", napcatText, llText)
	}
}

func TestVendorParity_RecordVoice(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "napcat record", raw: []byte(`[{"type":"record","data":{"file":"voice.amr"}}]`)},
		{name: "luckylillia record", raw: []byte(`[{"type":"record","data":{"file":"voice.silk"}}]`)},
		{name: "alias voice", raw: []byte(`[{"type":"voice","data":{"file":"voice.ogg"}}]`)},
		{name: "alias audio", raw: []byte(`[{"type":"audio","data":{"file":"voice.mp3"}}]`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			segs := ParseMessageSegments(tt.raw)
			text := extractUserText(segs, tt.raw)
			if text != "[语音]" {
				t.Fatalf("%s text = %q, want '[语音]'", tt.name, text)
			}
		})
	}
}

func TestVendorParity_VideoAndFile(t *testing.T) {
	videoRaw := []byte(`[{"type":"video","data":{"file":"clip.mp4"}}]`)
	videoSegs := ParseMessageSegments(videoRaw)
	if text := extractUserText(videoSegs, videoRaw); text != "[视频]" {
		t.Fatalf("video text = %q, want '[视频]'", text)
	}

	fileRaw := []byte(`[{"type":"file","data":{"file":"doc.pdf","name":"doc.pdf"}}]`)
	fileSegs := ParseMessageSegments(fileRaw)
	if text := extractUserText(fileSegs, fileRaw); text != "[文件:doc.pdf]" {
		t.Fatalf("file text = %q, want '[文件:doc.pdf]'", text)
	}

	// File with only file field (LuckyLillia variant)
	fileRaw2 := []byte(`[{"type":"file","data":{"file":"doc.pdf"}}]`)
	fileSegs2 := ParseMessageSegments(fileRaw2)
	if text := extractUserText(fileSegs2, fileRaw2); text != "[文件:doc.pdf]" {
		t.Fatalf("file text = %q, want '[文件:doc.pdf]'", text)
	}
}

func TestVendorParity_SenderNicknameCardFallback(t *testing.T) {
	// Case 1: Both Card and Nickname present and distinct
	e1 := model.OneBotEvent{
		UserID: testUserID,
		Sender: &model.OneBotSender{UserID: testUserID, Nickname: "UserNick", Card: "UserCard"},
	}
	if name := senderDisplayName(e1); name != "UserCard（UserNick）" {
		t.Fatalf("both card and nick: got %q, want 'UserCard（UserNick）'", name)
	}
	ctx1 := senderContext(e1)
	if ctx1["card"] != "UserCard" || ctx1["nickname"] != "UserNick" {
		t.Fatalf("senderContext mismatch: %+v", ctx1)
	}

	// Case 2: Card only
	e2 := model.OneBotEvent{
		UserID: testUserID,
		Sender: &model.OneBotSender{UserID: testUserID, Card: "UserCardOnly"},
	}
	if name := senderDisplayName(e2); name != "UserCardOnly" {
		t.Fatalf("card only: got %q, want 'UserCardOnly'", name)
	}

	// Case 3: Nickname only (common in LuckyLillia or private chat)
	e3 := model.OneBotEvent{
		UserID: testUserID,
		Sender: &model.OneBotSender{UserID: testUserID, Nickname: "UserNickOnly"},
	}
	if name := senderDisplayName(e3); name != "UserNickOnly" {
		t.Fatalf("nick only: got %q, want 'UserNickOnly'", name)
	}

	// Case 4: Neither (empty sender)
	e4 := model.OneBotEvent{
		UserID: testUserID,
		Sender: nil,
	}
	if name := senderDisplayName(e4); name != "未提供" {
		t.Fatalf("empty sender: got %q, want '未提供'", name)
	}
}

func TestVendorParity_SelfMessageSentIgnored(t *testing.T) {
	// Both NapCat and LuckyLillia can dispatch post_type="message_sent" for bot-sent messages.
	// FrostAgent must NOT dispatch normal user turn for message_sent.
	event := model.OneBotEvent{
		SelfID:      testBotID,
		PostType:    "message_sent",
		MessageType: "group",
		GroupID:     testGroupID,
		UserID:      testBotID,
		Message:     []byte(`[{"type":"text","data":{"text":"bot echoing"}}]`),
	}

	// processEvent has guard: if event.PostType != "message" { return }
	conn := newWSConnection(nil)
	processEvent(conn, event, nil, nil, nil)
	// No panic, no session created, cleanly dropped
}

// ---------------------------------------------------------------------------
// 2. Outbound Dual-Write Guarantee
// ---------------------------------------------------------------------------

func TestVendorParity_OutboundStickerDualWrite(t *testing.T) {
	toolMsgs := []tools.Msg{
		{
			Type:      "image",
			URL:       "https://example.com/sticker.png",
			IsSticker: true,
		},
	}

	chain, err := tools.BuildOneBotMessage(toolMsgs)
	if err != nil {
		t.Fatalf("BuildOneBotMessage failed: %v", err)
	}
	if len(chain) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(chain))
	}

	seg := chain[0]
	if seg.Type != "image" {
		t.Fatalf("expected segment type 'image', got %q", seg.Type)
	}
	// Must have sub_type = 1 for NapCat
	if seg.Data["sub_type"] != 1 {
		t.Errorf("expected sub_type=1 in outgoing sticker, got %v", seg.Data["sub_type"])
	}
	// Must have subType = 1 for LuckyLillia
	if seg.Data["subType"] != 1 {
		t.Errorf("expected subType=1 in outgoing sticker, got %v", seg.Data["subType"])
	}
}

// ---------------------------------------------------------------------------
// 3. get_msg Lifecycle Differences & Graceful Degradation
// ---------------------------------------------------------------------------

func TestVendorParity_GetMsgSuccess(t *testing.T) {
	conn := newWSConnection(nil)
	event := model.OneBotEvent{
		MessageType: "group",
		GroupID:     testGroupID,
		UserID:      testUserID,
	}

	respData, err := json.Marshal(map[string]any{
		"message_id":   101,
		"message_type": "group",
		"group_id":     testGroupID,
		"user_id":      testUserID,
		"message": []map[string]any{{
			"type": "text",
			"data": map[string]any{"text": "historical quoted message"},
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	reply := conn.resolveReplyResponse(event, 101, oneBotAPIResponse{
		Status:  "ok",
		RetCode: 0,
		Data:    respData,
	})

	if reply.MessageID != "101" {
		t.Fatalf("expected MessageID='101', got %q", reply.MessageID)
	}
	if !strings.Contains(reply.Prompt, "historical quoted message") {
		t.Fatalf("prompt missing text: %q", reply.Prompt)
	}
}

func TestVendorParity_GetMsgStaleOrNotFound_DegradesGracefully(t *testing.T) {
	// In NapCat, restarting the upstream process clears in-memory MessageUnique mapping,
	// returning retcode 102/100 (message not found) when get_msg is called on a stale ID.
	conn := newWSConnection(nil)
	event := model.OneBotEvent{
		MessageType: "group",
		GroupID:     testGroupID,
		UserID:      testUserID,
	}

	reply := conn.resolveReplyResponse(event, 9999, oneBotAPIResponse{
		Status:  "failed",
		RetCode: 102,
		Message: "MESSAGE_NOT_FOUND",
		Wording: "消息不存在",
	})

	// Must degrade to empty reply context without panic or failure
	if reply.MessageID != "" || reply.Prompt != "" || len(reply.Segments) != 0 {
		t.Fatalf("stale get_msg should degrade to empty, got: %+v", reply)
	}
}

func TestVendorParity_GetMsgDeletedMessage_DegradesGracefully(t *testing.T) {
	// Deleted message lookup failure
	conn := newWSConnection(nil)
	event := model.OneBotEvent{
		MessageType: "group",
		GroupID:     testGroupID,
		UserID:      testUserID,
	}

	reply := conn.resolveReplyResponse(event, 101, oneBotAPIResponse{
		Status:  "failed",
		RetCode: -1,
		Message: "MESSAGE_DELETED",
	})

	if reply.MessageID != "" || reply.Prompt != "" {
		t.Fatalf("deleted message lookup should degrade to empty, got: %+v", reply)
	}
}

func TestVendorParity_GetMsgWrongSession_Rejected(t *testing.T) {
	// get_msg returned a message from a different group (e.g. ID collision or wrong lookup)
	conn := newWSConnection(nil)
	event := model.OneBotEvent{
		MessageType: "group",
		GroupID:     testGroupID,
		UserID:      testUserID,
	}

	respData, _ := json.Marshal(map[string]any{
		"message_id":   101,
		"message_type": "group",
		"group_id":     99999, // different group!
		"user_id":      testUserID,
		"message":      []map[string]any{{"type": "text", "data": map[string]any{"text": "leak"}}},
	})

	reply := conn.resolveReplyResponse(event, 101, oneBotAPIResponse{
		Status:  "ok",
		RetCode: 0,
		Data:    respData,
	})

	if reply.MessageID != "" {
		t.Fatalf("cross-group get_msg must be rejected, got: %+v", reply)
	}
}

// ---------------------------------------------------------------------------
// 4. Quote and Sticker Resolution: Current vs Stale Message ID
// ---------------------------------------------------------------------------

func TestVendorParity_QuoteStickerCurrentMessageID_NoGetMsg(t *testing.T) {
	imageBytes := []byte("GIF89a current sticker")
	conn := newWSConnection(nil)
	event := model.OneBotEvent{
		MessageType: "group",
		GroupID:     testGroupID,
		UserID:      testUserID,
		MessageID:   100,
	}

	currentSegments := []content.MessageSegment{
		{
			Type: "image",
			Data: map[string]any{
				"sub_type": 1,
				"base64":   base64.StdEncoding.EncodeToString(imageBytes),
			},
		},
	}

	// Quoting current message ID resolves from currentSegments without network call
	data, err := conn.loadObservedSticker(context.Background(), event, resolvedReplyContext{}, currentSegments, "100", 0)
	if err != nil {
		t.Fatalf("load current sticker failed: %v", err)
	}
	if string(data) != string(imageBytes) {
		t.Fatalf("sticker data mismatch: got %q, want %q", data, imageBytes)
	}
}

func TestVendorParity_QuoteStickerStaleMessageID_GracefulError(t *testing.T) {
	conn := newWSConnection(nil)
	event := model.OneBotEvent{
		MessageType: "group",
		GroupID:     testGroupID,
		UserID:      testUserID,
		MessageID:   100,
	}

	// Requesting an unknown stale message ID with no get_msg response channel returns clear error
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := conn.loadObservedSticker(ctx, event, resolvedReplyContext{}, nil, "9999", 0)
	if err == nil {
		t.Fatalf("expected error loading stale sticker from unresolvable message ID, got nil")
	}
}

// ---------------------------------------------------------------------------
// 5. Send Failure Semantics: NapCat Partial Success vs LuckyLillia Overall Failure
// ---------------------------------------------------------------------------

func TestVendorParity_OutgoingPreflightFailure(t *testing.T) {
	// FrostAgent pre-flight validates local files upfront: missing or empty local media fails immediately
	// across all component types (image, record, video, file), preventing upstream converter divergence.
	mediaTypes := []string{"image", "record", "video", "file"}

	for _, mt := range mediaTypes {
		missingPath := filepath.Join(t.TempDir(), "nonexistent_"+mt+".dat")
		toolMsgs := []tools.Msg{
			{
				Type: mt,
				Path: missingPath,
			},
		}
		_, err := tools.BuildOneBotMessage(toolMsgs)
		if err == nil {
			t.Fatalf("expected pre-flight error for missing %s file, got nil", mt)
		}
		if !strings.Contains(err.Error(), missingPath) {
			t.Fatalf("error %q should mention missing path %q", err, missingPath)
		}

		emptyPath := filepath.Join(t.TempDir(), "empty_"+mt+".dat")
		if err := os.WriteFile(emptyPath, []byte{}, 0o600); err != nil {
			t.Fatalf("write empty fixture: %v", err)
		}
		toolMsgsEmpty := []tools.Msg{
			{
				Type: mt,
				Path: emptyPath,
			},
		}
		_, errEmpty := tools.BuildOneBotMessage(toolMsgsEmpty)
		if errEmpty == nil {
			t.Fatalf("expected pre-flight error for empty %s file, got nil", mt)
		}
		if !strings.Contains(errEmpty.Error(), "为空") {
			t.Fatalf("error %q should mention '为空' for empty path %q", errEmpty, emptyPath)
		}
	}
}

func TestVendorParity_SendFailure_LuckyLilliaErrorModel(t *testing.T) {
	// LuckyLillia throws exception on media converter error and returns retcode != 0
	provider := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{Role: core.RoleAssistant, Content: "replying"},
			},
		},
	}
	engine := newTestEngine(provider)
	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	event := model.OneBotEvent{
		SelfID:      testBotID,
		PostType:    "message",
		MessageType: "private",
		UserID:      testUserID,
		MessageID:   100,
		Message:     []byte(`[{"type":"text","data":{"text":"hello"}}]`),
	}
	eventBytes, _ := json.Marshal(event)
	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("send event: %v", err)
	}

	// Read outgoing action
	_, actionBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read action: %v", err)
	}
	var act model.OneBotAction
	_ = json.Unmarshal(actionBytes, &act)

	// Simulate LuckyLillia returning retcode != 0 (overall failure)
	llErrResponse, _ := json.Marshal(map[string]any{
		"status":  "failed",
		"retcode": 1200,
		"message": "CONVERTER_ERROR",
		"wording": "媒体转换异常",
		"echo":    act.Echo,
	})
	_ = conn.WriteMessage(websocket.TextMessage, llErrResponse)

	// Verify that the assistant history was NOT committed
	time.Sleep(50 * time.Millisecond)
	session := engine.SessionManager.GetOrCreate("private:10001")
	history := session.Snapshot()
	for _, m := range history {
		if m.Role == string(core.RoleAssistant) {
			t.Fatalf("LuckyLillia error must prevent committing assistant history, found: %+v", m)
		}
	}
	// Verify delivery failure was recorded
	fail := session.TakeDeliveryFailure()
	if fail == nil || fail.RetCode != 1200 {
		t.Fatalf("expected DeliveryFailure with RetCode=1200, got: %+v", fail)
	}
}

func TestVendorParity_SendSuccess_NapCatPartialModel(t *testing.T) {
	// NapCat may locally filter out an unconvertible element but return status="ok", retcode=0.
	// FrostAgent respects upstream ACK and commits history.
	provider := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{Role: core.RoleAssistant, Content: "napcat success reply"},
			},
		},
	}
	engine := newTestEngine(provider)
	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	event := model.OneBotEvent{
		SelfID:      testBotID,
		PostType:    "message",
		MessageType: "private",
		UserID:      testUserID,
		MessageID:   101,
		Message:     []byte(`[{"type":"text","data":{"text":"hello"}}]`),
	}
	eventBytes, _ := json.Marshal(event)
	_ = conn.WriteMessage(websocket.TextMessage, eventBytes)

	_, actionBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read action: %v", err)
	}
	var act model.OneBotAction
	_ = json.Unmarshal(actionBytes, &act)

	// Simulate NapCat returning ok (even if upstream had partial component drop)
	napcatOkResponse, _ := json.Marshal(map[string]any{
		"status":  "ok",
		"retcode": 0,
		"echo":    act.Echo,
		"data": map[string]any{
			"message_id": 5001,
		},
	})
	_ = conn.WriteMessage(websocket.TextMessage, napcatOkResponse)

	time.Sleep(50 * time.Millisecond)
	session := engine.SessionManager.GetOrCreate("private:10001")
	history := session.Snapshot()
	var committed bool
	for _, m := range history {
		contentStr, _ := m.Content.(string)
		if m.Role == string(core.RoleAssistant) && strings.Contains(contentStr, "napcat success reply") {
			committed = true
			break
		}
	}
	if !committed {
		t.Fatalf("NapCat success ACK must commit assistant history")
	}
}

func TestVendorParity_ActionACKTimeout(t *testing.T) {
	// When platform action times out, FrostAgent records delivery failure and refuses history commit.
	// Uses a real WebSocket peer that accepts the action frame and deliberately withholds any ACK response.
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	receivedAction := make(chan []byte, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			receivedAction <- msg
			// Deliberately do not send any ACK response to simulate upstream timeout/drop
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial dummy upstream: %v", err)
	}
	defer clientConn.Close()

	conn := newWSConnection(clientConn)
	act := model.OneBotAction{
		Action: "send_private_msg",
		Params: map[string]any{"user_id": testUserID, "message": "hello"},
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := conn.SendActionAndWait(act, 50*time.Millisecond)
		errCh <- err
	}()

	// Verify upstream actually received the action over the wire
	select {
	case data := <-receivedAction:
		var sentAct model.OneBotAction
		if err := json.Unmarshal(data, &sentAct); err != nil {
			t.Fatalf("unmarshal sent action: %v", err)
		}
		if sentAct.Action != "send_private_msg" {
			t.Fatalf("received action = %q, want 'send_private_msg'", sentAct.Action)
		}
	case <-time.After(time.Second):
		t.Fatalf("upstream did not receive action message over websocket")
	}

	// Verify SendActionAndWait timed out with expected error message
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatalf("expected timeout error, got nil")
		}
		if !strings.Contains(err.Error(), "timeout") {
			t.Fatalf("expected timeout error containing 'timeout', got: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("SendActionAndWait did not return after timeout")
	}
}

func TestVendorParity_StickerObservation_ConnectionScoped(t *testing.T) {
	// Verifies that sticker observations are scoped to the connection generation,
	// preventing cross-upstream/reconnect stale message ID lookups or ID collisions.
	store, err := sticker.NewStore(filepath.Join(t.TempDir(), "stickers"))
	if err != nil {
		t.Fatalf("create sticker store: %v", err)
	}
	stealer := sticker.NewStealer(store, nil)

	conn1 := newWSConnection(nil)
	conn1.stealer = stealer
	conn2 := newWSConnection(nil)
	conn2.stealer = stealer

	if conn1.generation == "" || conn2.generation == "" || conn1.generation == conn2.generation {
		t.Fatalf("each connection must have a unique generation: conn1=%q, conn2=%q", conn1.generation, conn2.generation)
	}

	event1 := model.OneBotEvent{
		PostType:    "message",
		MessageType: "group",
		GroupID:     testGroupID,
		UserID:      testUserID,
		MessageID:   12345,
		Message:     []byte(`[{"type":"image","data":{"url":"https://example.com/s1.png","sub_type":1}}]`),
	}
	conn1.observeStickers(event1)

	loaderCalled := false
	loader := func(ctx context.Context, msgID string, idx int) ([]byte, error) {
		loaderCalled = true
		return []byte("GIF89a payload"), nil
	}

	// 1. Connection 1 can steal its observed sticker
	loaderCalled = false
	_, resolvedID, err := stealer.StealObservedScoped(context.Background(), historyKey(event1), "", 0, conn1.generation, loader)
	if err != nil || resolvedID != "12345" || !loaderCalled {
		t.Fatalf("conn1 steal failed: resolvedID=%q err=%v", resolvedID, err)
	}

	// 2. Connection 2 (different upstream or reconnected) must NOT pick conn1's sticker when message_id is omitted
	loaderCalled = false
	_, _, err = stealer.StealObservedScoped(context.Background(), historyKey(event1), "", 0, conn2.generation, loader)
	if err != sticker.ErrStickerNotInScope {
		t.Fatalf("conn2 should not pick conn1's sticker with empty message_id, got err=%v", err)
	}
	if loaderCalled {
		t.Fatalf("loader must not be called when sticker is not in scope")
	}

	// 3. Explicit message_id from conn1 passed to conn2 must be rejected to prevent stale handle lookup
	loaderCalled = false
	_, _, err = stealer.StealObservedScoped(context.Background(), historyKey(event1), "12345", 0, conn2.generation, loader)
	if err != sticker.ErrStickerNotInScope {
		t.Fatalf("conn2 should reject conn1's explicit message_id, got err=%v", err)
	}

	// 4. When conn1 disconnects, its observations are cleared
	stealer.ClearObservedScope(conn1.generation)
	_, _, err = stealer.StealObservedScoped(context.Background(), historyKey(event1), "12345", 0, "", loader)
	if err != sticker.ErrStickerNotInScope {
		t.Fatalf("cleared observation should not be retrievable, got err=%v", err)
	}
}

func TestVendorParity_QuoteScoping_CrossGenerationIsolation(t *testing.T) {
	// Proves that message_id scoping protects quote/reply segments at the SendHook boundary:
	// an ID observed/sent on connection generation A cannot be emitted as a reply.id on generation B.
	connA := newWSConnection(nil)
	connB := newWSConnection(nil)

	if connA.generation == "" || connB.generation == "" || connA.generation == connB.generation {
		t.Fatalf("connections must have unique generations: connA=%q, connB=%q", connA.generation, connB.generation)
	}

	sessionID := "group:1001"
	// Generation A receives message 10001
	connA.rememberMessageSession(10001, sessionID)
	// Generation B receives message 20002
	connB.rememberMessageSession(20002, sessionID)

	quoteGenA := []tools.Msg{{Type: "quote", MessageID: "10001"}}
	quoteGenB := []tools.Msg{{Type: "quote", MessageID: "20002"}}

	// 1. Quoting 10001 on Generation A succeeds
	if err := validateQuoteMessages(connA, quoteGenA, sessionID); err != nil {
		t.Fatalf("connA should allow quoting its own message 10001: %v", err)
	}

	// 2. Quoting 10001 from Generation A on Generation B MUST fail (preventing stale quote emission)
	if err := validateQuoteMessages(connB, quoteGenA, sessionID); err == nil {
		t.Fatalf("connB unexpectedly allowed quoting generation A message 10001")
	}

	// 3. Quoting 20002 on Generation B succeeds
	if err := validateQuoteMessages(connB, quoteGenB, sessionID); err != nil {
		t.Fatalf("connB should allow quoting its own message 20002: %v", err)
	}

	// 4. Quoting a message from another session on Generation B MUST fail
	otherSession := "group:9999"
	connB.rememberMessageSession(30003, otherSession)
	quoteOtherSession := []tools.Msg{{Type: "quote", MessageID: "30003"}}
	if err := validateQuoteMessages(connB, quoteOtherSession, sessionID); err == nil {
		t.Fatalf("connB unexpectedly allowed quoting cross-session message 30003")
	}

	// 5. Assistant message sent on Generation B recorded via action ACK can be quoted
	ackResp := oneBotAPIResponse{
		RetCode: 0,
		Status:  "ok",
		Data:    []byte(`{"message_id": 40004}`),
	}
	connB.rememberActionMessageSession(ackResp, sessionID)
	quoteBotSent := []tools.Msg{{Type: "quote", MessageID: "40004"}}
	if err := validateQuoteMessages(connB, quoteBotSent, sessionID); err != nil {
		t.Fatalf("connB should allow quoting message 40004 recorded via action ACK: %v", err)
	}
}
