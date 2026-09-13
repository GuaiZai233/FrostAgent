package astrbot

import (
	"FrostAgent/internal/adapter/parity"
	"FrostAgent/internal/core"
	"FrostAgent/internal/tools"
	"reflect"
	"strconv"
	"strings"
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

func TestCrossAdapterInboundMentionOnlyDifferential(t *testing.T) {
	const (
		botSelfID   int64 = 100001
		otherUserID int64 = 200002
	)

	tests := []struct {
		name            string
		isGroup         bool
		isAtBot         bool
		hasReply        bool
		replyMessageID  string
		hasOtherMention bool
		hasOtherContent bool
		hasMediaContent bool
		hasImages       bool
		text            string
		nonTextSegment  *tools.OneBotSegment
		wantMentionOnly bool
	}{
		{
			name:            "pure_at_bot",
			isGroup:         true,
			isAtBot:         true,
			hasReply:        false,
			hasOtherMention: false,
			hasImages:       false,
			text:            "",
			wantMentionOnly: true,
		},
		{
			name:            "whitespace_only_at_bot",
			isGroup:         true,
			isAtBot:         true,
			hasReply:        false,
			hasOtherMention: false,
			hasImages:       false,
			text:            "   \n  \t  ",
			wantMentionOnly: true,
		},
		{
			name:            "reply_and_at_bot_no_text_no_attachment",
			isGroup:         true,
			isAtBot:         true,
			hasReply:        true,
			replyMessageID:  "msg_target_999",
			hasOtherMention: false,
			hasImages:       false,
			text:            "",
			wantMentionOnly: false, // Contract invariant: Reply + @Bot is never mention-only
		},
		{
			name:            "reply_and_at_bot_with_whitespace",
			isGroup:         true,
			isAtBot:         true,
			hasReply:        true,
			replyMessageID:  "msg_target_999",
			hasOtherMention: false,
			hasImages:       false,
			text:            "  ",
			wantMentionOnly: false,
		},
		{
			name:            "at_bot_and_other_user",
			isGroup:         true,
			isAtBot:         true,
			hasReply:        false,
			hasOtherMention: true,
			hasImages:       false,
			text:            "",
			wantMentionOnly: false,
		},
		{
			name:            "at_bot_with_face",
			isGroup:         true,
			isAtBot:         true,
			hasReply:        false,
			hasOtherMention: false,
			hasOtherContent: true,
			hasImages:       false,
			text:            "",
			nonTextSegment: &tools.OneBotSegment{
				Type: "face",
				Data: map[string]any{"id": "14"},
			},
			wantMentionOnly: false,
		},
		{
			name:            "at_bot_with_record",
			isGroup:         true,
			isAtBot:         true,
			hasReply:        false,
			hasOtherMention: false,
			hasOtherContent: true,
			hasImages:       false,
			text:            "",
			nonTextSegment: &tools.OneBotSegment{
				Type: "record",
				Data: map[string]any{"file": "voice.amr"},
			},
			wantMentionOnly: false,
		},
		{
			name:            "at_bot_with_plain_text",
			isGroup:         true,
			isAtBot:         true,
			hasReply:        false,
			hasOtherMention: false,
			hasImages:       false,
			text:            "hello bot",
			wantMentionOnly: false,
		},
		{
			name:            "at_bot_with_image",
			isGroup:         true,
			isAtBot:         true,
			hasReply:        false,
			hasOtherMention: false,
			hasImages:       true,
			text:            "",
			wantMentionOnly: false,
		},
		{
			name:            "at_bot_with_failed_image_extraction",
			isGroup:         true,
			isAtBot:         true,
			hasReply:        false,
			hasOtherMention: false,
			hasOtherContent: false,
			hasMediaContent: true,
			hasImages:       false, // image extraction/conversion failed, attachments is empty
			text:            "",
			nonTextSegment: &tools.OneBotSegment{
				Type: "image",
				Data: map[string]any{"file": "corrupted_or_failed"},
			},
			wantMentionOnly: false,
		},
		{
			name:            "private_at_bot",
			isGroup:         false,
			isAtBot:         true,
			hasReply:        false,
			hasOtherMention: false,
			hasImages:       false,
			text:            "",
			wantMentionOnly: false,
		},
		{
			name:            "unmentioned_group_message",
			isGroup:         true,
			isAtBot:         false,
			hasReply:        false,
			hasOtherMention: false,
			hasImages:       false,
			text:            "",
			wantMentionOnly: false,
		},
		{
			name:            "reply_at_bot_and_text",
			isGroup:         true,
			isAtBot:         true,
			hasReply:        true,
			replyMessageID:  "msg_target_999",
			hasOtherMention: false,
			hasImages:       false,
			text:            "check this out",
			wantMentionOnly: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgType := "group"
			if !tt.isGroup {
				msgType = "private"
			}

			// 1. Evaluate via AstrBot adapter path.
			// In AstrBot, At and non-text components are omitted from Content and
			// instead preserved as structural metadata (has_other_mention, has_other_content).
			astrbotEvent := Event{
				MessageType: msgType,
				IsAt:        tt.isAtBot,
				Content:     tt.text,
				Metadata: map[string]any{
					"has_other_mention": tt.hasOtherMention,
					"has_other_content": tt.hasOtherContent,
					"has_media_content": tt.hasMediaContent || tt.hasImages,
					"has_images":        tt.hasMediaContent || tt.hasImages,
				},
			}
			if tt.hasReply {
				astrbotEvent.Metadata["reply_message_id"] = tt.replyMessageID
			}
			if tt.hasImages {
				astrbotEvent.Attachments = []core.Attachment{{Type: core.AttachmentTypeImage}}
			}
			astrbotResult := isMentionOnlyInteraction(astrbotEvent)

			// 2. Evaluate via OneBot raw segments adapter path
			var onebotSegs []tools.OneBotSegment
			if tt.hasReply {
				onebotSegs = append(onebotSegs, tools.OneBotSegment{
					Type: "reply",
					Data: map[string]any{"id": tt.replyMessageID},
				})
			}
			if tt.isAtBot {
				onebotSegs = append(onebotSegs, tools.OneBotSegment{
					Type: "at",
					Data: map[string]any{"qq": strconv.FormatInt(botSelfID, 10)},
				})
			}
			if tt.hasOtherMention {
				onebotSegs = append(onebotSegs, tools.OneBotSegment{
					Type: "at",
					Data: map[string]any{"qq": strconv.FormatInt(otherUserID, 10)},
				})
			}
			if tt.nonTextSegment != nil {
				onebotSegs = append(onebotSegs, *tt.nonTextSegment)
			}
			if tt.text != "" {
				onebotSegs = append(onebotSegs, tools.OneBotSegment{
					Type: "text",
					Data: map[string]any{"text": tt.text},
				})
			}
			if tt.hasImages {
				onebotSegs = append(onebotSegs, tools.OneBotSegment{
					Type: "image",
					Data: map[string]any{"file": "http://example.com/img.png"},
				})
			}
			onebotSegmentResult := parity.IsMentionOnlyOneBotSegments(
				tt.isGroup,
				botSelfID,
				onebotSegs,
				tt.hasImages,
				tt.hasReply,
			)

			// 3. Evaluate via OneBot text fallback path
			userTextParts := make([]string, 0, 4)
			if tt.isAtBot {
				userTextParts = append(userTextParts, "[@"+strconv.FormatInt(botSelfID, 10)+"]")
			}
			if tt.hasOtherMention {
				userTextParts = append(userTextParts, "[@"+strconv.FormatInt(otherUserID, 10)+"]")
			}
			if tt.hasOtherContent || tt.hasMediaContent {
				userTextParts = append(userTextParts, "[表情/媒体]")
			}
			if tt.text != "" {
				userTextParts = append(userTextParts, tt.text)
			}
			onebotFallbackUserText := strings.Join(userTextParts, " ")
			onebotFallbackResult := parity.IsMentionOnlyOneBot(
				tt.isGroup,
				botSelfID,
				tt.isAtBot,
				onebotFallbackUserText,
				tt.hasImages || tt.hasMediaContent,
				tt.hasReply,
			)

			// 4. Differential assertions
			if astrbotResult != onebotSegmentResult {
				t.Fatalf("Differential parity mismatch between AstrBot (%v) and OneBot raw segments (%v)", astrbotResult, onebotSegmentResult)
			}
			if astrbotResult != onebotFallbackResult {
				t.Fatalf("Differential parity mismatch between AstrBot (%v) and OneBot text fallback (%v)", astrbotResult, onebotFallbackResult)
			}
			if astrbotResult != tt.wantMentionOnly {
				t.Fatalf("Expected mention_only=%v, got %v", tt.wantMentionOnly, astrbotResult)
			}
		})
	}
}
