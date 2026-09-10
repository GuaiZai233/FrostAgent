package astrbot

import (
	"FrostAgent/internal/adapter/parity"
	"FrostAgent/internal/tools"
	"reflect"
	"strconv"
	"testing"
)

type NormalizedSemantics struct {
	QuoteID       string
	QuoteCount    int
	MentionUserID string
	MentionCount  int
	StickerBypass bool
	Ordering      []string
}

func extractSemanticsFromAstrBot(a Action) NormalizedSemantics {
	var sem NormalizedSemantics
	sem.StickerBypass = a.containsSticker()

	for _, msg := range a.Messages {
		switch {
		case parity.IsQuoteType(msg.Type):
			sem.QuoteCount++
			if sem.QuoteID == "" {
				sem.QuoteID = msg.MessageID
			}
			sem.Ordering = append(sem.Ordering, "quote")
		case parity.IsMentionType(msg.Type):
			sem.MentionCount++
			if sem.MentionUserID == "" {
				sem.MentionUserID = msg.MentionUserID
			}
			sem.Ordering = append(sem.Ordering, "mention")
		case msg.Type == "plain" || msg.Type == "text":
			sem.Ordering = append(sem.Ordering, "text")
		case parity.IsStickerType(msg.Type):
			sem.Ordering = append(sem.Ordering, "sticker")
		case msg.Type == "image":
			if parity.IsStickerSubType(msg.SubType) || msg.IsSticker {
				sem.Ordering = append(sem.Ordering, "sticker")
			} else {
				sem.Ordering = append(sem.Ordering, "image")
			}
		default:
			sem.Ordering = append(sem.Ordering, msg.Type)
		}
	}
	return sem
}

func extractSemanticsFromOneBot(segs []tools.OneBotSegment) NormalizedSemantics {
	var sem NormalizedSemantics
	sem.StickerBypass = parity.ContainsStickerOneBot(segs)

	for _, seg := range segs {
		switch {
		case parity.IsQuoteType(seg.Type):
			sem.QuoteCount++
			if sem.QuoteID == "" {
				if id, ok := seg.Data["id"].(string); ok {
					sem.QuoteID = id
				}
			}
			sem.Ordering = append(sem.Ordering, "quote")
		case parity.IsMentionType(seg.Type):
			sem.MentionCount++
			if sem.MentionUserID == "" {
				if qq, ok := seg.Data["qq"].(string); ok {
					sem.MentionUserID = qq
				} else if id, ok := seg.Data["mention_user_id"].(string); ok {
					sem.MentionUserID = id
				}
			}
			sem.Ordering = append(sem.Ordering, "mention")
		case seg.Type == "text":
			sem.Ordering = append(sem.Ordering, "text")
		case parity.IsStickerType(seg.Type):
			sem.Ordering = append(sem.Ordering, "sticker")
		case seg.Type == "image":
			if parity.IsStickerSubType(seg.Data["sub_type"]) || parity.IsStickerSubType(seg.Data["subType"]) {
				sem.Ordering = append(sem.Ordering, "sticker")
			} else if isSticker, ok := seg.Data["is_sticker"].(bool); ok && isSticker {
				sem.Ordering = append(sem.Ordering, "sticker")
			} else {
				sem.Ordering = append(sem.Ordering, "image")
			}
		default:
			sem.Ordering = append(sem.Ordering, seg.Type)
		}
	}
	return sem
}

func TestCrossAdapterDecorationDifferential(t *testing.T) {
	tests := []struct {
		name           string
		isGroup        bool
		enableReply    bool
		enableAt       bool
		replyMessageID string
		userID         string
		// AstrBot initial messages
		astrbotInitial []ActionMessage
		// OneBot initial segments
		onebotInitial []tools.OneBotSegment
	}{
		{
			name:           "normal_group_reply_both_enabled",
			isGroup:        true,
			enableReply:    true,
			enableAt:       true,
			replyMessageID: "20002",
			userID:         "10001",
			astrbotInitial: []ActionMessage{
				{Type: "plain", Text: "Hello world"},
			},
			onebotInitial: []tools.OneBotSegment{
				{Type: "text", Data: map[string]any{"text": "Hello world"}},
			},
		},
		{
			name:           "sticker_bypass_image_subtype_1",
			isGroup:        true,
			enableReply:    true,
			enableAt:       true,
			replyMessageID: "20002",
			userID:         "10001",
			astrbotInitial: []ActionMessage{
				{Type: "image", SubType: 1, URL: "http://example.com/sticker.png"},
			},
			onebotInitial: []tools.OneBotSegment{
				{Type: "image", Data: map[string]any{"sub_type": 1, "file": "base64://sticker"}},
			},
		},
		{
			name:           "sticker_bypass_mface",
			isGroup:        true,
			enableReply:    true,
			enableAt:       true,
			replyMessageID: "20002",
			userID:         "10001",
			astrbotInitial: []ActionMessage{
				{Type: "mface"},
			},
			onebotInitial: []tools.OneBotSegment{
				{Type: "mface", Data: map[string]any{"id": "test_mface"}},
			},
		},
		{
			name:           "sticker_bypass_flag",
			isGroup:        true,
			enableReply:    true,
			enableAt:       true,
			replyMessageID: "20002",
			userID:         "10001",
			astrbotInitial: []ActionMessage{
				{Type: "image", IsSticker: true},
			},
			onebotInitial: []tools.OneBotSegment{
				{Type: "image", Data: map[string]any{"is_sticker": true}},
			},
		},
		{
			name:           "existing_quote_deduplication",
			isGroup:        true,
			enableReply:    true,
			enableAt:       true,
			replyMessageID: "20002",
			userID:         "10001",
			astrbotInitial: []ActionMessage{
				{Type: "quote", MessageID: "20002"},
				{Type: "plain", Text: "Hello world"},
			},
			onebotInitial: []tools.OneBotSegment{
				{Type: "reply", Data: map[string]any{"id": "20002"}},
				{Type: "text", Data: map[string]any{"text": "Hello world"}},
			},
		},
		{
			name:           "existing_mention_deduplication",
			isGroup:        true,
			enableReply:    true,
			enableAt:       true,
			replyMessageID: "20002",
			userID:         "10001",
			astrbotInitial: []ActionMessage{
				{Type: "mention_user", MentionUserID: "10001"},
				{Type: "plain", Text: "Hello world"},
			},
			onebotInitial: []tools.OneBotSegment{
				{Type: "at", Data: map[string]any{"qq": "10001"}},
				{Type: "text", Data: map[string]any{"text": "Hello world"}},
			},
		},
		{
			name:           "reply_enabled_at_disabled",
			isGroup:        true,
			enableReply:    true,
			enableAt:       false,
			replyMessageID: "20002",
			userID:         "10001",
			astrbotInitial: []ActionMessage{
				{Type: "plain", Text: "Hello world"},
			},
			onebotInitial: []tools.OneBotSegment{
				{Type: "text", Data: map[string]any{"text": "Hello world"}},
			},
		},
		{
			name:           "reply_disabled_at_enabled",
			isGroup:        true,
			enableReply:    false,
			enableAt:       true,
			replyMessageID: "20002",
			userID:         "10001",
			astrbotInitial: []ActionMessage{
				{Type: "plain", Text: "Hello world"},
			},
			onebotInitial: []tools.OneBotSegment{
				{Type: "text", Data: map[string]any{"text": "Hello world"}},
			},
		},
		{
			name:           "both_disabled",
			isGroup:        true,
			enableReply:    false,
			enableAt:       false,
			replyMessageID: "20002",
			userID:         "10001",
			astrbotInitial: []ActionMessage{
				{Type: "plain", Text: "Hello world"},
			},
			onebotInitial: []tools.OneBotSegment{
				{Type: "text", Data: map[string]any{"text": "Hello world"}},
			},
		},
		{
			name:           "private_message_no_decoration",
			isGroup:        false,
			enableReply:    true,
			enableAt:       true,
			replyMessageID: "20002",
			userID:         "10001",
			astrbotInitial: []ActionMessage{
				{Type: "plain", Text: "Hello world"},
			},
			onebotInitial: []tools.OneBotSegment{
				{Type: "text", Data: map[string]any{"text": "Hello world"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 1. Run through AstrBot normalization
			env := map[string]string{}
			if tt.enableReply {
				env["ENABLE_REPLY_IN_GROUP_MSG"] = "true"
			} else {
				env["ENABLE_REPLY_IN_GROUP_MSG"] = "false"
			}
			if tt.enableAt {
				env["ENABLE_AT_IN_GROUP_MSG"] = "true"
			} else {
				env["ENABLE_AT_IN_GROUP_MSG"] = "false"
			}
			scope := newAstrBotRuntimeScope(t, env)

			msgType := "group"
			if !tt.isGroup {
				msgType = "private"
			}
			astrbotAction := Action{
				Action:         "send_message",
				MessageType:    msgType,
				ReplyMessageID: tt.replyMessageID,
				UserID:         tt.userID,
				Messages:       tt.astrbotInitial,
			}
			normalizedAstrBot := astrbotAction.withConfiguredGroupMentionScope(scope).withConfiguredGroupReplyScope(scope)
			astrbotSemantics := extractSemanticsFromAstrBot(normalizedAstrBot)

			// 2. Run through OneBot normalization
			replyIDInt, _ := strconv.ParseInt(tt.replyMessageID, 10, 64)
			userIDInt, _ := strconv.ParseInt(tt.userID, 10, 64)
			var normalizedOneBot []tools.OneBotSegment
			if tt.isGroup {
				normalizedOneBot = parity.WrapGroupReplyOneBot(tt.onebotInitial, replyIDInt, userIDInt, tt.enableReply, tt.enableAt)
			} else {
				normalizedOneBot = tt.onebotInitial
			}
			onebotSemantics := extractSemanticsFromOneBot(normalizedOneBot)

			// 3. Differential assertion: observable semantics must match identically
			if !reflect.DeepEqual(astrbotSemantics, onebotSemantics) {
				t.Fatalf("Differential parity mismatch for %q:\nAstrBot: %+v\nOneBot:  %+v", tt.name, astrbotSemantics, onebotSemantics)
			}
		})
	}
}
