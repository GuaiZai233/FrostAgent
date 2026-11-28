package onebot

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"

	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

const implementationProbeTimeout = 5 * time.Second

// implementationProbeEnabled lets tests that read raw frames from a fake peer
// opt out of the get_version_info probe.
var implementationProbeEnabled = true

// platformAppendsMentionSpace reports whether the OneBot implementation named by
// get_version_info.app_name already appends a space after every @ element it
// sends. SnowLuma builds it as "@name " (see makeMentionElem), so a separator
// added by FrostAgent would display as two spaces.
func platformAppendsMentionSpace(appName string) bool {
	// 以后也许会做成开关，也会扩充规则，现在先硬编码匹配。——GuaiZai注
	return strings.TrimSpace(appName) == "SnowLuma"
}

// detectImplementation asks the peer which OneBot implementation it is. Until
// it answers (or when it never does) FrostAgent keeps the default behaviour.
func (c *wsConnection) detectImplementation() {
	if c == nil || c.mock || c.conn == nil || !implementationProbeEnabled {
		return
	}
	response, err := c.SendActionAndWait(model.OneBotAction{Action: "get_version_info", Params: map[string]any{}}, implementationProbeTimeout)
	if err != nil {
		return
	}
	var info struct {
		AppName string `json:"app_name"`
	}
	if json.Unmarshal(response.Data, &info) != nil {
		return
	}
	if platformAppendsMentionSpace(info.AppName) {
		c.platformMentionSpace.Store(true)
		if c.engine != nil {
			c.engine.Log().Info(logs.WEBSOCKET, "OneBot 实现 SnowLuma 会在 @ 后自带空格，不再额外补分隔空格")
		}
	}
}

// adaptMentionSpacing removes the horizontal whitespace FrostAgent put in front
// of text that follows an @ segment, for implementations that append their own
// space. The caller's segments are never modified.
func (c *wsConnection) adaptMentionSpacing(action model.OneBotAction) model.OneBotAction {
	if c == nil || !c.platformMentionSpace.Load() {
		return action
	}
	switch action.Action {
	case "send_msg", "send_group_msg", "send_private_msg":
	default:
		return action
	}
	params, ok := action.Params.(map[string]any)
	if !ok {
		return action
	}
	segments, ok := params["message"].([]tools.OneBotSegment)
	if !ok {
		return action
	}
	var adapted []tools.OneBotSegment
	for i := 0; i+1 < len(segments); i++ {
		if segments[i].Type != "at" || segments[i+1].Type != "text" {
			continue
		}
		text, ok := segments[i+1].Data["text"].(string)
		trimmed := strings.TrimLeftFunc(text, func(r rune) bool { return r != '\n' && r != '\r' && unicode.IsSpace(r) })
		if !ok || trimmed == "" || trimmed == text {
			continue
		}
		if adapted == nil {
			adapted = append([]tools.OneBotSegment(nil), segments...)
		}
		data := make(map[string]any, len(segments[i+1].Data))
		for key, value := range segments[i+1].Data {
			data[key] = value
		}
		data["text"] = trimmed
		adapted[i+1].Data = data
	}
	if adapted == nil {
		return action
	}
	cloned := make(map[string]any, len(params))
	for key, value := range params {
		cloned[key] = value
	}
	cloned["message"] = adapted
	action.Params = cloned
	return action
}
