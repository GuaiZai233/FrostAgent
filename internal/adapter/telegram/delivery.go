package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/chatflow"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
)

func (a *Adapter) SetStickerStore(store *sticker.Store) { a.stickers = store }

func (a *Adapter) deliver(ctx context.Context, delivery chatflow.Delivery) (deliveryErr error) {
	var confirmed strings.Builder
	confirmedSend := false
	onText := func(text string) { confirmedSend = true; confirmed.WriteString(text) }
	defer func() {
		if deliveryErr != nil && confirmedSend {
			deliveryErr = &chatflow.DeliveryError{Err: deliveryErr, DeliveredText: confirmed.String(), Confirmed: true}
		}
	}()
	if delivery.Action == "noop" {
		return nil
	}
	chatID, err := parseID(delivery.Message.TargetID)
	if err != nil {
		return err
	}
	if delivery.Action == "thinking_status" {
		return a.client.call(ctx, "sendChatAction", map[string]any{"chat_id": chatID, "action": "typing"}, nil, "", nil)
	}
	if delivery.Action != "send_message" {
		return errors.New("unsupported Telegram delivery action")
	}
	parts := delivery.Parts
	if len(parts) == 0 {
		if delivery.Message.Content == "" && len(delivery.Message.Attachments) == 0 {
			return errors.New("Telegram message is empty")
		}
		if delivery.Message.Content != "" {
			if err := a.sendText(ctx, chatID, delivery.Message.Content, nil, 0, onText); err != nil {
				return err
			}
		}
		for _, att := range delivery.Message.Attachments {
			if err := a.sendAttachment(ctx, chatID, att, 0); err != nil {
				return err
			}
			confirmedSend = true
		}
		return nil
	}

	var text strings.Builder
	var entities []entity
	replyID := int64(0)
	sent := false
	flush := func() error {
		if text.Len() == 0 {
			return nil
		}
		if err := a.sendText(ctx, chatID, text.String(), entities, replyID, onText); err != nil {
			return err
		}
		text.Reset()
		entities = nil
		replyID = 0
		sent = true
		return nil
	}
	for _, part := range parts {
		switch part.Type {
		case "plain", "text":
			text.WriteString(part.Text)
		case "mention_user":
			id, err := parseID(part.MentionUserID)
			if err != nil || id < 0 {
				return errors.New("Telegram mention requires a positive numeric user ID")
			}
			label := "@" + part.MentionUserID
			entities = append(entities, entity{Type: "text_link", Offset: len(utf16.Encode([]rune(text.String()))), Length: len(label), URL: "tg://user?id=" + part.MentionUserID})
			text.WriteString(label)
		case "quote":
			prefix := strconv.FormatInt(chatID, 10) + "/"
			if !strings.HasPrefix(part.MessageID, prefix) {
				return errors.New("Telegram quote belongs to another chat")
			}
			id, err := strconv.ParseInt(strings.TrimPrefix(part.MessageID, prefix), 10, 64)
			if err != nil || id <= 0 {
				return errors.New("invalid Telegram reply message ID")
			}
			replyID = id
		case "image", "file", "record", "audio", "video":
			if err := flush(); err != nil {
				return err
			}
			if part.IsSticker {
				data, err := a.stickerBytes(part.URL)
				if err != nil {
					return err
				}
				if err = a.client.call(ctx, "sendPhoto", mediaParameters(chatID, replyID), data, "sticker.png", nil); err != nil {
					return err
				}
			} else {
				// A model/tool-supplied path must never turn into a host file read.
				source := part.URL
				if source == "" {
					source = part.Path
				}
				kind := core.AttachmentTypeImage
				switch part.Type {
				case "file":
					kind = core.AttachmentTypeFile
				case "record", "audio":
					kind = core.AttachmentTypeAudio
				case "video":
					kind = core.AttachmentTypeVideo
				}
				if err := a.sendAttachment(ctx, chatID, core.Attachment{Type: kind, URL: source}, replyID); err != nil {
					return err
				}
			}
			confirmedSend = true
			replyID = 0
			sent = true
		default:
			return fmt.Errorf("unsupported Telegram message component %q", part.Type)
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if !sent && len(delivery.Message.Attachments) == 0 {
		return errors.New("Telegram message is empty")
	}
	return nil
}

func (a *Adapter) sendText(ctx context.Context, chatID int64, text string, entities []entity, replyID int64, onText func(string)) error {
	units := utf16.Encode([]rune(text))
	for start := 0; start < len(units); {
		end := min(start+4096, len(units))
		if end < len(units) && units[end-1] >= 0xD800 && units[end-1] <= 0xDBFF {
			end--
		}
		for _, e := range entities {
			if e.Offset < end && e.Offset+e.Length > end {
				end = e.Offset
			}
		}
		if end <= start {
			return errors.New("Telegram entity exceeds the message limit")
		}
		params := map[string]any{"chat_id": chatID, "text": string(utf16.Decode(units[start:end]))}
		var chunkEntities []entity
		for _, e := range entities {
			if e.Offset >= start && e.Offset+e.Length <= end {
				e.Offset -= start
				chunkEntities = append(chunkEntities, e)
			}
		}
		if len(chunkEntities) > 0 {
			params["entities"] = chunkEntities
		}
		if replyID > 0 {
			params["reply_parameters"] = map[string]any{"message_id": replyID, "allow_sending_without_reply": true}
		}
		if err := a.client.call(ctx, "sendMessage", params, nil, "", nil); err != nil {
			return err
		}
		if onText != nil {
			onText(string(utf16.Decode(units[start:end])))
		}
		replyID = 0
		start = end
	}
	return nil
}

func (a *Adapter) sendAttachment(ctx context.Context, chatID int64, att core.Attachment, replyID int64) error {
	method, field := "sendPhoto", "photo"
	switch att.Type {
	case core.AttachmentTypeImage:
	case core.AttachmentTypeFile:
		method, field = "sendDocument", "document"
	case core.AttachmentTypeAudio:
		method, field = "sendAudio", "audio"
	case core.AttachmentTypeVideo:
		method, field = "sendVideo", "video"
	default:
		return errors.New("unsupported Telegram attachment type")
	}
	params := map[string]any{"chat_id": chatID}
	if replyID > 0 {
		params["reply_parameters"] = map[string]any{"message_id": replyID, "allow_sending_without_reply": true}
	}
	if len(att.Content) > 0 {
		if len(att.Content) > maxMediaBytes {
			return errors.New("Telegram upload exceeds 8 MiB")
		}
		name := att.Name
		if name == "" {
			name = "attachment"
		}
		return a.client.call(ctx, method, params, att.Content, name, nil)
	}
	source, err := url.Parse(att.URL)
	if err != nil || (source.Scheme != "https" && source.Scheme != "http") || source.Host == "" || source.User != nil {
		return errors.New("Telegram attachment requires image bytes or an HTTP(S) URL; local paths are unsupported")
	}
	params[field] = att.URL
	return a.client.call(ctx, method, params, nil, "", nil)
}

func (a *Adapter) stickerBytes(source string) ([]byte, error) {
	u, err := url.Parse(source)
	if err != nil || u.IsAbs() || u.Host != "" || a.stickers == nil {
		return nil, errors.New("Telegram sticker source is unavailable")
	}
	_, tail, ok := strings.Cut(u.Path, "/api/sticker/")
	id, suffix, found := strings.Cut(tail, "/")
	if !ok || !found || suffix != "image" {
		return nil, errors.New("Telegram sticker source is invalid")
	}
	filename, ok := a.stickers.FilePath(id)
	if !ok {
		return nil, errors.New("Telegram sticker is unavailable")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, errors.New("Telegram sticker cannot be read")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxMediaBytes+1))
	if err != nil || len(data) > maxMediaBytes {
		return nil, errors.New("Telegram sticker exceeds the upload limit")
	}
	return data, nil
}

func mediaParameters(chatID, replyID int64) map[string]any {
	params := map[string]any{"chat_id": chatID}
	if replyID > 0 {
		params["reply_parameters"] = map[string]any{"message_id": replyID, "allow_sending_without_reply": true}
	}
	return params
}
