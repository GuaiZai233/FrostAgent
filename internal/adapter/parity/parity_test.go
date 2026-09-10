package parity

import (
	"FrostAgent/internal/tools"
	"testing"
)

func TestDecorationStickerBypass(t *testing.T) {
	// Base message contains sticker -> should not be decorated with reply or at
	base := []tools.OneBotSegment{
		{
			Type: "image",
			Data: map[string]any{
				"file":     "base64://stickerdata",
				"sub_type": 1,
			},
		},
	}

	res := WrapGroupReplyOneBot(base, 1001, 2002, true, true)
	if len(res) != 1 || res[0].Type != "image" {
		t.Fatalf("expected sticker not to receive reply or at, got: %+v", res)
	}

	// Another sticker shape: mface
	baseMface := []tools.OneBotSegment{
		{
			Type: "mface",
			Data: map[string]any{"id": "test_mface"},
		},
	}
	resMface := WrapGroupReplyOneBot(baseMface, 1001, 2002, true, true)
	if len(resMface) != 1 || resMface[0].Type != "mface" {
		t.Fatalf("expected mface not to receive reply or at, got: %+v", resMface)
	}

	// Sticker flag in data
	baseFlag := []tools.OneBotSegment{
		{
			Type: "image",
			Data: map[string]any{
				"is_sticker": true,
			},
		},
	}
	resFlag := WrapGroupReplyOneBot(baseFlag, 1001, 2002, true, true)
	if len(resFlag) != 1 || resFlag[0].Type != "image" {
		t.Fatalf("expected is_sticker image not to receive reply or at, got: %+v", resFlag)
	}
}

func TestDecorationQuoteDeduplication(t *testing.T) {
	// Base message already has a reply quote -> should not add duplicate reply
	base := []tools.OneBotSegment{
		{
			Type: "reply",
			Data: map[string]any{"id": "1001"},
		},
		{
			Type: "text",
			Data: map[string]any{"text": "Hello"},
		},
	}

	res := WrapGroupReplyOneBot(base, 1001, 2002, true, true)
	replyCount := 0
	atCount := 0
	for _, seg := range res {
		if seg.Type == "reply" {
			replyCount++
		}
		if seg.Type == "at" {
			atCount++
		}
	}

	if replyCount != 1 {
		t.Errorf("expected exactly 1 reply segment, got %d", replyCount)
	}
	if atCount != 1 {
		t.Errorf("expected 1 at segment added, got %d", atCount)
	}
}

func TestDecorationMentionDeduplication(t *testing.T) {
	// Base message already has an at segment targeting the user -> should not add duplicate at
	base := []tools.OneBotSegment{
		{
			Type: "at",
			Data: map[string]any{"qq": "2002"},
		},
		{
			Type: "text",
			Data: map[string]any{"text": "Hello"},
		},
	}

	res := WrapGroupReplyOneBot(base, 1001, 2002, true, true)
	replyCount := 0
	atCount := 0
	for _, seg := range res {
		if seg.Type == "reply" {
			replyCount++
		}
		if seg.Type == "at" {
			atCount++
		}
	}

	if replyCount != 1 {
		t.Errorf("expected 1 reply segment added, got %d", replyCount)
	}
	if atCount != 1 {
		t.Errorf("expected exactly 1 at segment (no duplicate), got %d", atCount)
	}
}

func TestDecorationNormalWrap(t *testing.T) {
	base := []tools.OneBotSegment{
		{
			Type: "text",
			Data: map[string]any{"text": "Hello world"},
		},
	}

	res := WrapGroupReplyOneBot(base, 1001, 2002, true, true)
	if len(res) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(res))
	}
	if res[0].Type != "reply" || res[0].Data["id"] != "1001" {
		t.Errorf("expected first segment to be reply: %+v", res[0])
	}
	if res[1].Type != "at" || res[1].Data["qq"] != "2002" {
		t.Errorf("expected second segment to be at: %+v", res[1])
	}
	if res[2].Type != "text" || res[2].Data["text"] != "Hello world" {
		t.Errorf("expected third segment to be text: %+v", res[2])
	}
}

func TestMentionOnlyDetection(t *testing.T) {
	if !IsMentionOnlyOneBot(true, 123456, true, "", false, false) {
		t.Errorf("expected OneBot pure @ to be mention only")
	}
	if !IsMentionOnlyOneBot(true, 123456, true, "   ", false, false) {
		t.Errorf("expected OneBot whitespace-only text to be mention only")
	}
	if !IsMentionOnlyOneBot(true, 123456, true, "[@123456] ", false, false) {
		t.Errorf("expected OneBot extracted token text to be mention only")
	}
	if IsMentionOnlyOneBot(true, 123456, true, "[@123456] [@654321]", false, false) {
		t.Errorf("expected mention of bot and other user to NOT be mention only")
	}
	if IsMentionOnlyOneBot(false, 123456, true, "", false, false) {
		t.Errorf("private message should not be mention only")
	}
	if IsMentionOnlyOneBot(true, 123456, false, "", false, false) {
		t.Errorf("unmentioned message should not be mention only")
	}
	if IsMentionOnlyOneBot(true, 123456, true, "hello", false, false) {
		t.Errorf("message with text should not be mention only")
	}
	if IsMentionOnlyOneBot(true, 123456, true, "[@123456] hello", false, false) {
		t.Errorf("message with token and text should not be mention only")
	}
	if IsMentionOnlyOneBot(true, 123456, true, "", true, false) {
		t.Errorf("message with image should not be mention only")
	}
	if IsMentionOnlyOneBot(true, 123456, true, "", false, true) {
		t.Errorf("message with reply should not be mention only")
	}
	if IsMentionOnlyOneBot(true, 123456, true, "[@123456]", false, true) {
		t.Errorf("message with reply and self token should not be mention only")
	}

	// Raw segment based
	pureAtSegs := []tools.OneBotSegment{
		{Type: "at", Data: map[string]any{"qq": "123456"}},
		{Type: "text", Data: map[string]any{"text": "   "}},
	}
	if !IsMentionOnlyOneBotSegments(true, 123456, pureAtSegs, false, false) {
		t.Errorf("expected pureAtSegs to be mention only")
	}
	if IsMentionOnlyOneBotSegments(true, 123456, pureAtSegs, false, true) {
		t.Errorf("pureAtSegs with hasReply=true should not be mention only")
	}

	replyAndAtSegs := []tools.OneBotSegment{
		{Type: "reply", Data: map[string]any{"id": "999"}},
		{Type: "at", Data: map[string]any{"qq": "123456"}},
		{Type: "text", Data: map[string]any{"text": "   "}},
	}
	if IsMentionOnlyOneBotSegments(true, 123456, replyAndAtSegs, false, false) {
		t.Errorf("replyAndAtSegs should not be mention only")
	}

	atWithContentSegs := []tools.OneBotSegment{
		{Type: "at", Data: map[string]any{"qq": "123456"}},
		{Type: "text", Data: map[string]any{"text": "hello"}},
	}
	if IsMentionOnlyOneBotSegments(true, 123456, atWithContentSegs, false, false) {
		t.Errorf("atWithContentSegs should not be mention only")
	}

	atOtherUserSegs := []tools.OneBotSegment{
		{Type: "at", Data: map[string]any{"qq": "123456"}},
		{Type: "at", Data: map[string]any{"qq": "999999"}},
	}
	if IsMentionOnlyOneBotSegments(true, 123456, atOtherUserSegs, false, false) {
		t.Errorf("atOtherUserSegs should not be mention only")
	}

	atWithMediaSegs := []tools.OneBotSegment{
		{Type: "at", Data: map[string]any{"qq": "123456"}},
		{Type: "image", Data: map[string]any{"file": "abc"}},
	}
	if IsMentionOnlyOneBotSegments(true, 123456, atWithMediaSegs, false, false) {
		t.Errorf("atWithMediaSegs should not be mention only")
	}

	// AstrBot
	if !IsMentionOnlyAstrBot(true, true, "", 0, false) {
		t.Errorf("expected AstrBot pure @ to be mention only")
	}
	if !IsMentionOnlyAstrBot(true, true, "   ", 0, false) {
		t.Errorf("expected AstrBot whitespace text to be mention only")
	}
	if IsMentionOnlyAstrBot(false, true, "", 0, false) {
		t.Errorf("AstrBot private should not be mention only")
	}
	if IsMentionOnlyAstrBot(true, false, "", 0, false) {
		t.Errorf("AstrBot not @ should not be mention only")
	}
	if IsMentionOnlyAstrBot(true, true, "test", 0, false) {
		t.Errorf("AstrBot with text should not be mention only")
	}
	if IsMentionOnlyAstrBot(true, true, "", 1, false) {
		t.Errorf("AstrBot with attachment should not be mention only")
	}
	if IsMentionOnlyAstrBot(true, true, "", 0, true) {
		t.Errorf("AstrBot with reply should not be mention only")
	}

	// Canonical interaction contract
	if !IsMentionOnly(MentionInteraction{
		IsGroup:        true,
		IsMentionedBot: true,
	}) {
		t.Errorf("canonical pure @ interaction should be mention only")
	}
	if IsMentionOnly(MentionInteraction{
		IsGroup:        true,
		IsMentionedBot: true,
		HasReply:       true,
	}) {
		t.Errorf("canonical interaction with reply should not be mention only")
	}
}

func TestBillingParity(t *testing.T) {
	if CanonicalBillingPlatform("aiocqhttp") != "qq" {
		t.Errorf("expected aiocqhttp billing platform to map to qq")
	}
	if CanonicalBillingPlatform("onebot") != "qq" {
		t.Errorf("expected onebot billing platform to map to qq")
	}
	if CanonicalBillingPlatform("qq") != "qq" {
		t.Errorf("expected qq billing platform to map to qq")
	}

	taskID := BillingTaskID("qq", "user_1", "msg_1")
	if taskID != "qq_user_1_msg_1" {
		t.Errorf("expected taskID qq_user_1_msg_1, got %q", taskID)
	}
}
