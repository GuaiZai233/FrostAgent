package onebot

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot/content"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

const qqForwardImagePrefix = "qq-forward-image:"

// A reference contains only a trusted containing message ID and record indices.
// It is an image locator, never a live message ID or an upstream resource ID.
func qqForwardImageRef(a tools.QQArguments, nodeIndex int) string {
	parts := []string{a.MessageID, strconv.Itoa(a.ForwardIndex)}
	for _, step := range a.Path {
		parts = append(parts, strconv.Itoa(step.NodeIndex), strconv.Itoa(step.ForwardIndex))
	}
	parts = append(parts, strconv.Itoa(nodeIndex))
	return qqForwardImagePrefix + strings.Join(parts, ":")
}

func parseQQForwardImageRef(ref string) (tools.QQArguments, int, error) {
	invalid := fmt.Errorf("转发图片引用无效，请使用 read_forward 返回的 image_ref")
	if len(ref) > 256 || !strings.HasPrefix(ref, qqForwardImagePrefix) {
		return tools.QQArguments{}, 0, invalid
	}
	parts := strings.Split(strings.TrimPrefix(ref, qqForwardImagePrefix), ":")
	if len(parts) < 3 || len(parts) > 9 || len(parts)%2 == 0 {
		return tools.QQArguments{}, 0, invalid
	}
	mid, ok := numericMessageID(parts[0])
	if !ok {
		return tools.QQArguments{}, 0, invalid
	}
	indices := make([]int, len(parts)-1)
	for i, part := range parts[1:] {
		index, err := strconv.Atoi(part)
		if err != nil || index < 0 {
			return tools.QQArguments{}, 0, invalid
		}
		indices[i] = index
	}
	a := tools.QQArguments{MessageID: strconv.FormatInt(mid, 10), ForwardIndex: indices[0]}
	for i := 1; i < len(indices)-1; i += 2 {
		a.Path = append(a.Path, tools.QQForwardStep{NodeIndex: indices[i], ForwardIndex: indices[i+1]})
	}
	return a, indices[len(indices)-1], nil
}

func (c *wsConnection) loadQQForwardImage(ctx context.Context, event model.OneBotEvent, ref string, imageIndex int) ([]byte, error) {
	a, nodeIndex, err := parseQQForwardImageRef(ref)
	if err != nil {
		return nil, err
	}
	run, _ := llm.RunContextFromContext(ctx)
	if c.mock || run.Mock || run.Proactive {
		return nil, fmt.Errorf("转发图片读取不能用于模拟或主动轮次")
	}
	raw, call, err := c.qqForwardSource(ctx, event, run.Turn, &a)
	if err != nil {
		return nil, err
	}
	records, err := loadQQForwardRecords(raw, a, call)
	if err != nil {
		return nil, err
	}
	if nodeIndex >= len(records) {
		return nil, fmt.Errorf("转发 node_index %d 不存在", nodeIndex)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if run.Turn.Interrupted() {
		return nil, context.Canceled
	}
	return imageDataFromSegments(ctx, qqForwardImageSegments(records[nodeIndex].content), imageIndex)
}

// Tokenize CQ before unescaping, so escaped CQ examples never become attachments.
// Keep unreadable image slots to preserve the ordinal exposed by image_count.
func qqForwardImageSegments(raw json.RawMessage) []content.MessageSegment {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return ParseMessageSegments(raw)
	}
	var segments []content.MessageSegment
	for _, code := range historyCQCode.FindAllString(text, -1) {
		kind, params, _ := strings.Cut(code[len("[CQ:"):len(code)-1], ",")
		if kind != "image" && kind != "mface" {
			continue
		}
		data := map[string]any{}
		for _, param := range strings.Split(params, ",") {
			key, value, ok := strings.Cut(param, "=")
			if ok {
				data[key] = historyCQDataEscape.Replace(value)
			}
		}
		segments = append(segments, content.MessageSegment{Type: kind, Data: data})
	}
	return content.NormalizeMessageSegments(segments)
}
