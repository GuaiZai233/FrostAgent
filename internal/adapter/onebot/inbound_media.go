package onebot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot/content"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/model"
)

const (
	// maxInboundMediaBytes bounds the audio/video bytes one turn may hold in
	// memory and inline into model requests (current message plus quotes).
	maxInboundMediaBytes int64 = 20 << 20
	// maxInboundMediaItems bounds how many media segments one turn reads.
	maxInboundMediaItems = 4
	// recordOutputFormat asks the OneBot implementation to transcode QQ's
	// SILK/AMR voice into a container that general models can decode.
	recordOutputFormat = "mp3"
)

var errInboundMediaBudget = errors.New("音视频超出本轮 20 MiB 的读取上限")

// inboundMediaBudget tracks the per-turn byte and item allowance shared by the
// current message and its quotes. Media bytes only live in request memory and
// are dropped with the turn; nothing is written to disk or the database.
type inboundMediaBudget struct {
	remaining int64
	items     int
}

func newInboundMediaBudget() *inboundMediaBudget {
	return &inboundMediaBudget{remaining: maxInboundMediaBytes}
}

// inboundMediaKinds selects which media kinds a consumer can accept.
type inboundMediaKinds struct {
	audio, video bool
}

func (k inboundMediaKinds) accepts(kind content.MediaKind) bool {
	return (kind == content.MediaAudio && k.audio) || (kind == content.MediaVideo && k.video)
}

func (k inboundMediaKinds) any() bool {
	return k.audio || k.video
}

// loadInboundMedia reads accepted audio/video segments into request-local
// content parts. failed counts media segments that are present but could not be
// read, were rejected, or were not accepted by the consumer.
func (c *wsConnection) loadInboundMedia(ctx context.Context, event model.OneBotEvent, segments []content.MessageSegment, kinds inboundMediaKinds, budget *inboundMediaBudget) (parts []core.ContentPart, failed int) {
	for _, seg := range segments {
		kind := content.SegmentMediaKind(seg)
		if kind == content.MediaNone {
			continue
		}
		if !kinds.accepts(kind) {
			failed++
			continue
		}
		if budget.items >= maxInboundMediaItems {
			failed++
			continue
		}
		budget.items++
		data, err := c.fetchInboundMedia(ctx, event, seg, kind, budget.remaining)
		if err == nil {
			var part core.ContentPart
			part, err = content.MediaContentPart(kind, data)
			if err == nil {
				budget.remaining -= int64(len(data))
				parts = append(parts, part)
				continue
			}
		}
		failed++
		if c != nil && c.Scope != nil {
			c.Log().Warn(logs.WEBSOCKET, fmt.Sprintf("读取%s消息段失败: %v", mediaKindLabel(kind), err))
		}
	}
	return parts, failed
}

func mediaKindLabel(kind content.MediaKind) string {
	if kind == content.MediaVideo {
		return "视频"
	}
	return "音频"
}

func (c *wsConnection) fetchInboundMedia(ctx context.Context, event model.OneBotEvent, seg content.MessageSegment, kind content.MediaKind, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, errInboundMediaBudget
	}
	if declared := fileDataInt(seg.Data, "file_size", "size"); declared > limit {
		return nil, errInboundMediaBudget
	}
	if data, ok, err := content.SegmentInlineMedia(seg); ok {
		if err != nil {
			return nil, err
		}
		return limitInboundMedia(data, limit)
	}
	if c == nil || c.mock {
		return nil, errors.New("消息段没有可读取的音视频数据")
	}
	switch {
	case seg.Type == "file":
		return c.fetchInboundMediaFile(ctx, event, seg, limit)
	case kind == content.MediaAudio:
		return c.fetchInboundRecord(ctx, seg, limit)
	default:
		return c.fetchInboundVideo(ctx, seg, limit)
	}
}

func limitInboundMedia(data []byte, limit int64) ([]byte, error) {
	if int64(len(data)) > limit {
		return nil, errInboundMediaBudget
	}
	return data, nil
}

// fetchInboundRecord asks the OneBot implementation to transcode the voice
// message, then falls back to the segment's own download URL.
func (c *wsConnection) fetchInboundRecord(ctx context.Context, seg content.MessageSegment, limit int64) ([]byte, error) {
	params := map[string]any{"out_format": recordOutputFormat}
	if file := fileDataString(seg.Data, "file"); file != "" {
		params["file"] = file
	}
	if fileID := fileDataString(seg.Data, "file_id"); fileID != "" {
		params["file_id"] = fileID
	}
	var actionErr error
	if len(params) > 1 {
		var data []byte
		data, actionErr = c.fetchMediaAction(ctx, "get_record", params, limit)
		if actionErr == nil {
			return data, nil
		}
	}
	if source := fileDataString(seg.Data, "url"); allowedInboundFileURL(source) {
		return c.downloadInboundFile(ctx, source, limit)
	}
	if actionErr != nil {
		return nil, actionErr
	}
	return nil, errors.New("语音消息没有可读取的数据")
}

// fetchInboundVideo downloads the trusted QQ video URL, then falls back to
// the OneBot get_file action.
func (c *wsConnection) fetchInboundVideo(ctx context.Context, seg content.MessageSegment, limit int64) ([]byte, error) {
	var downloadErr error
	if source := fileDataString(seg.Data, "url"); allowedInboundFileURL(source) {
		data, err := c.downloadInboundFile(ctx, source, limit)
		if err == nil {
			return data, nil
		}
		downloadErr = err
	}
	params := map[string]any{}
	if fileID := fileDataString(seg.Data, "file_id"); fileID != "" {
		params["file_id"] = fileID
	} else if file := fileDataString(seg.Data, "file"); file != "" {
		params["file"] = file
	}
	if len(params) > 0 {
		data, err := c.fetchMediaAction(ctx, "get_file", params, limit)
		if err == nil {
			return data, nil
		}
		if downloadErr == nil {
			downloadErr = err
		}
	}
	if downloadErr != nil {
		return nil, downloadErr
	}
	return nil, errors.New("视频消息没有可读取的数据")
}

// fetchInboundMediaFile downloads an uploaded audio/video file through the
// same trusted QQ file URL path as sandbox attachment imports.
func (c *wsConnection) fetchInboundMediaFile(ctx context.Context, event model.OneBotEvent, seg content.MessageSegment, limit int64) ([]byte, error) {
	if source := fileDataString(seg.Data, "url"); allowedInboundFileURL(source) {
		if data, err := c.downloadInboundFile(ctx, source, limit); err == nil {
			return data, nil
		}
	}
	source, err := c.inboundFileURL(ctx, &inboundFile{
		fileID:  fileDataString(seg.Data, "file_id", "id"),
		groupID: event.GroupID,
		busID:   fileDataInt(seg.Data, "busid", "bus_id"),
	})
	if err != nil {
		return nil, err
	}
	return c.downloadInboundFile(ctx, source, limit)
}

// fetchMediaAction calls a OneBot file action and reads its Base64 payload or
// trusted QQ download URL. Local file paths from the implementation are never
// opened: the OneBot host may not share this process's filesystem.
func (c *wsConnection) fetchMediaAction(ctx context.Context, action string, params map[string]any, limit int64) ([]byte, error) {
	response, err := c.sendActionAndWaitContext(model.OneBotAction{Action: action, Params: params}, actionACKTimeout(c.Scope), nil, ctx)
	if err != nil {
		return nil, fmt.Errorf("OneBot %s 调用失败", action)
	}
	var data struct {
		Base64 string `json:"base64"`
		URL    string `json:"url"`
		File   string `json:"file"`
	}
	if json.Unmarshal(response.Data, &data) != nil {
		return nil, fmt.Errorf("OneBot %s 返回格式无效", action)
	}
	if encoded := strings.TrimSpace(data.Base64); encoded != "" {
		if int64(len(encoded))/4*3 > limit+3 {
			return nil, errInboundMediaBudget
		}
		decoded, err := content.DecodeInlineMedia(encoded)
		if err != nil {
			return nil, err
		}
		return limitInboundMedia(decoded, limit)
	}
	for _, source := range []string{data.URL, data.File} {
		if strings.HasPrefix(source, "base64://") || strings.HasPrefix(source, "data:") {
			decoded, err := content.DecodeInlineMedia(source)
			if err != nil {
				return nil, err
			}
			return limitInboundMedia(decoded, limit)
		}
		if allowedInboundFileURL(source) {
			return c.downloadInboundFile(ctx, source, limit)
		}
	}
	return nil, fmt.Errorf("OneBot %s 未返回 Base64 或受信任的下载地址", action)
}
