package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/chatflow"
	"github.com/GuaiZai233/FrostAgent/internal/core"
)

type user struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}
type chat struct {
	ID      int64  `json:"id"`
	Type    string `json:"type"`
	Title   string `json:"title"`
	IsForum bool   `json:"is_forum"`
}
type entity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	User   *user  `json:"user,omitempty"`
	URL    string `json:"url,omitempty"`
}
type photo struct {
	FileID string `json:"file_id"`
	Size   int64  `json:"file_size"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}
type message struct {
	ID              int64    `json:"message_id"`
	From            *user    `json:"from"`
	Chat            chat     `json:"chat"`
	Date            int64    `json:"date"`
	Text            string   `json:"text"`
	Caption         string   `json:"caption"`
	Entities        []entity `json:"entities"`
	CaptionEntities []entity `json:"caption_entities"`
	Photos          []photo  `json:"photo"`
	Reply           *message `json:"reply_to_message"`
	ThreadID        int64    `json:"message_thread_id"`
}
type update struct {
	ID      int64    `json:"update_id"`
	Message *message `json:"message"`
}

func messageID(chatID, id int64) string { return fmt.Sprintf("%d/%d", chatID, id) }

func utf16Text(text string, offset, length int) string {
	units := utf16.Encode([]rune(text))
	if offset < 0 || length < 0 || offset > len(units) || length > len(units)-offset {
		return ""
	}
	return string(utf16.Decode(units[offset : offset+length]))
}

func (a *Adapter) normalize(ctx context.Context, m *message, bot user) (chatflow.Input, bool, error) {
	if m == nil || m.ID <= 0 || m.From == nil || m.From.ID <= 0 || m.From.IsBot || m.Chat.ID == 0 || m.Chat.IsForum || m.ThreadID != 0 {
		return chatflow.Input{}, false, nil
	}
	if m.Chat.Type != "private" && m.Chat.Type != "group" && m.Chat.Type != "supergroup" {
		return chatflow.Input{}, false, nil
	}
	if m.Text == "" && m.Caption == "" && len(m.Photos) == 0 {
		return chatflow.Input{}, false, nil
	}
	incoming, err := a.incoming(ctx, m)
	if err != nil {
		return chatflow.Input{}, false, err
	}
	input := chatflow.Input{Message: incoming}
	text, entities := m.Text, m.Entities
	if text == "" {
		text, entities = m.Caption, m.CaptionEntities
	}
	units := utf16.Encode([]rune(text))
	otherMention := false
	// Telegram entity offsets count UTF-16 code units, including astral emoji.
	for i := len(entities) - 1; i >= 0; i-- {
		e := entities[i]
		segment := utf16Text(text, e.Offset, e.Length)
		isBot := (e.Type == "mention" && bot.Username != "" && strings.EqualFold(segment, "@"+bot.Username)) || (e.Type == "text_mention" && e.User != nil && e.User.ID == bot.ID)
		if isBot {
			input.IsAt, input.IsWake = true, true
			if e.Offset >= 0 && e.Length > 0 && e.Offset+e.Length <= len(units) {
				units = append(units[:e.Offset], units[e.Offset+e.Length:]...)
			}
		} else if e.Type == "mention" || e.Type == "text_mention" {
			otherMention = true
		}
		if e.Type == "bot_command" && e.Offset == 0 {
			_, username, directed := strings.Cut(segment, "@")
			if !directed || strings.EqualFold(username, bot.Username) {
				input.IsWake = true
			}
		}
	}
	input.Message.Content = strings.TrimSpace(string(utf16.Decode(units)))
	input.Message.Metadata["has_other_mention"] = otherMention
	input.Message.Metadata["thinking_status_supported"] = true
	if m.Reply != nil && m.Reply.Chat.ID == m.Chat.ID && m.Reply.ID > 0 {
		quoted, err := a.incoming(ctx, m.Reply)
		if err != nil {
			return chatflow.Input{}, false, err
		}
		input.Quote = &quoted
		if m.Reply.From != nil && m.Reply.From.ID == bot.ID {
			input.IsWake, input.IsAt = true, true
		}
	}
	return input, true, nil
}

func (a *Adapter) incoming(ctx context.Context, m *message) (core.IncomingMessage, error) {
	kind := "private"
	groupID := ""
	if m.Chat.Type != "private" {
		kind, groupID = "group", strconv.FormatInt(m.Chat.ID, 10)
	}
	target := strconv.FormatInt(m.Chat.ID, 10)
	text := m.Text
	if text == "" {
		text = m.Caption
	}
	incoming := core.IncomingMessage{ID: messageID(m.Chat.ID, m.ID), Platform: "telegram", AdapterID: "telegram",
		MessageType: kind, SessionID: "telegram:" + kind + ":" + target, GroupID: groupID, GroupName: m.Chat.Title,
		Content: text, CreatedAt: time.Unix(m.Date, 0), Metadata: map[string]any{}}
	if m.From != nil {
		incoming.UserID = strconv.FormatInt(m.From.ID, 10)
		incoming.SenderName = strings.TrimSpace(m.From.FirstName + " " + m.From.LastName)
		if incoming.SenderName == "" {
			incoming.SenderName = m.From.Username
		}
	}
	if len(m.Photos) > 0 {
		var selected *photo
		for i := range m.Photos {
			p := &m.Photos[i]
			if p.Size <= maxMediaBytes && (selected == nil || p.Width*p.Height > selected.Width*selected.Height) {
				selected = p
			}
		}
		if selected == nil {
			return incoming, errMediaRejected
		}

		data, err := a.client.downloadPhoto(ctx, selected.FileID)
		if err != nil {
			return incoming, err
		}
		incoming.Attachments = []core.Attachment{{Type: core.AttachmentTypeImage, Content: data, MessageID: incoming.ID}}
	}
	incoming.Metadata["has_media_content"] = len(incoming.Attachments) > 0
	incoming.Metadata["has_images"] = len(incoming.Attachments) > 0
	return incoming, nil
}
