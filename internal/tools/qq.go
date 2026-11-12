package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/qqextend"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
)

// QQRequest is a closed, platform-independent set of messaging operations.
// Vendor action names are not model inputs; cross-session targets are allowlisted.
type QQRequest struct {
	Action    string      `json:"action"`
	Arguments QQArguments `json:"arguments"`
	// Derived by send_message after validating mention_specific.
	SuppressAutoMention bool `json:"suppress_auto_mention,omitempty"`
}

type QQArguments struct {
	TargetGroupID string          `json:"target_group_id,omitempty"`
	Messages      []Msg           `json:"messages,omitempty"`
	Nodes         []QQForwardNode `json:"nodes,omitempty"`
	MessageID     string          `json:"message_id,omitempty"`
	UserID        string          `json:"user_id,omitempty"`
	EmojiID       string          `json:"emoji_id,omitempty"`
	Set           *bool           `json:"set,omitempty"`
	Source        string          `json:"source,omitempty"`
	Summary       string          `json:"summary,omitempty"`
	Prompt        string          `json:"prompt,omitempty"`
	News          []string        `json:"news,omitempty"`
}

type QQForwardNode struct {
	MessageID string `json:"message_id,omitempty"`
	Messages  []Msg  `json:"messages,omitempty"`
}

// QQMessageData contains only the fields supported by native rich components.
type QQMessageData struct {
	ID             string   `json:"id,omitempty"`
	Type           string   `json:"type,omitempty"`
	URL            string   `json:"url,omitempty"`
	Title          string   `json:"title,omitempty"`
	Content        string   `json:"content,omitempty"`
	Image          string   `json:"image,omitempty"`
	Audio          string   `json:"audio,omitempty"`
	Singer         string   `json:"singer,omitempty"`
	Lat            *float64 `json:"lat,omitempty"`
	Lon            *float64 `json:"lon,omitempty"`
	EmojiID        string   `json:"emoji_id,omitempty"`
	EmojiPackageID string   `json:"emoji_package_id,omitempty"`
	Key            string   `json:"key,omitempty"`
	Summary        string   `json:"summary,omitempty"`
}

var qqHelp = map[string]string{
	"send":            `messages: ordered array (1..50). Common components: {type:plain,text}, {type:mention_user,mention_user_id}, {type:quote,message_id}. Rich components: {type:face,data:{id}}, {type:dice}, {type:rps}, {type:mention_all}, {type:contact,data:{type:qq|group,id}}, {type:music,data:{type:qq|163|kugou|migu|kuwo,id}}, {type:music,data:{type:custom,url,audio,title,image?,content?,singer?}}, {type:share,data:{url,title,content?,image?}}, {type:location,data:{lat,lon,title?,content?}}, {type:mface,data:{emoji_id,emoji_package_id,key,summary?}}. Alternative observed mface: {type:mface,message_id,sticker_index?}; index is zero-based among market stickers, default 0. The adapter loads native metadata only from a trusted message in this conversation. IDs are strings from trusted context; dice/rps are random. Media: {type:image|record|video|file,path OR url,name?,summary?,thumb?,folder_id?,transfer?}. summary is image-only, thumb is video-only (local path or HTTP(S)); folder_id is group-file-only. transfer=auto (default: local path, bounded inline fallback only on explicit path rejection), shared (path only), inline (base64, max 30 MiB). URLs must use HTTP(S). Files are uploaded separately while preserving order; other components are sent as one chain. Per-index statuses: confirmed/rejected/unknown/not_sent. Stop at the first failure; never retry unknown delivery. share/location are rendered as fixed QQ Ark cards (location includes a coordinate map link), since NapCat cannot send those raw segment types. Explicit Ark rejection may fall back to a vendor card API or text link.`,
	"forward_bundle":  `nodes: 1..50 items, each either {message_id} referencing an observed message in this conversation, or {messages:[components as in send]}. New nodes use the bot's identity. Optional source, summary, prompt, news (array of preview strings). File uploads/folders, quotes, mention_all, and local media requiring transfer are not supported inside nodes; use media URLs.`,
	"forward_message": `message_id: exact observed message ID in the current conversation. Native single-message forwarding, falling back to a merged-forward node only when the action is explicitly unsupported.`,
	"poke":            `user_id: optional exact recipient ID, defaults to the current speaker. Private chats can only poke the current peer; group pokes remain in the active group.`,
	"reaction":        `message_id: exact observed ID in the active group; emoji_id: platform reaction ID as string; set: boolean, default true; false removes the reaction.`,
	"recall":          `message_id: exact ID acknowledged as sent by this bot on the current connection in this conversation. Only the bot's own messages can be recalled.`,
}

func QQTool() Tool {
	return Tool{
		name:        qqextend.ToolName,
		description: "Native QQ rich messaging and interactions. First call action=discover for capabilities and configured target groups, or discover with capability for parameters. Execute send, forward_bundle, forward_message, poke, reaction, recall using arguments. send/forward actions accept target_group_id for cross-session delivery ONLY to an administrator-configured group; omit for the active conversation. Source message IDs must belong to the active conversation. Supports NapCat and SnowLuma. Never retry an unknown delivery. Ordinary replies use send_message.",
		parameter: map[string]any{"type": "object", "properties": map[string]any{
			"action":     map[string]any{"type": "string", "enum": []string{"discover", "send", "forward_bundle", "forward_message", "poke", "reaction", "recall"}},
			"capability": map[string]any{"type": "string", "description": "Capability name to inspect when action=discover."},
			"arguments":  map[string]any{"type": "object", "description": "Parameters returned by discover for this action."},
		}, "required": []string{"action"}, "additionalProperties": false},
		executeResult: func(ctx context.Context, raw string) (core.ToolExecutionResult, error) {
			var input struct {
				Action     string          `json:"action"`
				Capability string          `json:"capability,omitempty"`
				Arguments  json.RawMessage `json:"arguments,omitempty"`
			}
			if err := decodeQQJSON([]byte(raw), &input); err != nil {
				return textResult(""), err
			}
			run, _ := llm.RunContextFromContext(ctx)
			if input.Action == "discover" {
				groups, err := qqextend.ParseTargetGroups(qqextend.ConfiguredTargets(runtimescope.FromContext(ctx)))
				if err != nil {
					groups = []string{}
				}
				if input.Capability != "" {
					help, ok := qqHelp[input.Capability]
					if !ok {
						return textResult(""), fmt.Errorf("unknown QQ capability %q", input.Capability)
					}
					if input.Capability == "send" || strings.HasPrefix(input.Capability, "forward_") {
						help += " Optional target_group_id: exact group ID from target_groups for cross-session delivery; omit to use the active conversation. Source references remain bound to the active conversation. Cross-session sends cannot contain quote components and never inherit automatic reply/@ prefixes."
					}
					data, _ := json.Marshal(map[string]any{"capability": input.Capability, "parameters": help, "target_groups": groups, "available": run.PlatformActionHook != nil && !run.Proactive && !run.Mock})
					return textResult(string(data)), nil
				}
				data, _ := json.Marshal(map[string]any{"capabilities": []string{"send", "forward_bundle", "forward_message", "poke", "reaction", "recall"}, "components": []string{"face", "dice", "rps", "contact", "music", "share", "location", "mention_all", "mface", "image", "record", "video", "file"}, "available": run.PlatformActionHook != nil && !run.Proactive && !run.Mock, "target_groups": groups, "scope": "active conversation; send/forward may target a configured group; inspect a capability before calling"})
				return textResult(string(data)), nil
			}
			if run.Proactive || run.Mock || run.PlatformActionHook == nil {
				return textResult(""), fmt.Errorf("QQ 扩展操作需要活动的 OneBot 对话，且不能用于主动或模拟轮次")
			}
			request := QQRequest{Action: input.Action}
			if err := decodeQQJSON(input.Arguments, &request.Arguments); err != nil {
				return textResult(""), err
			}
			if err := ValidateQQRequest(request); err != nil {
				return textResult(""), err
			}
			for _, msg := range qqRequestMessages(request) {
				if (msg.IsSticker || msg.Type == "mface") && sticker.ActivityLevel(runtimescope.FromContext(ctx).Getenv(sticker.ActivityEnvKey)) == 0 {
					return textResult(""), fmt.Errorf("表情包发送功能已被禁用")
				}
			}
			data, _ := json.Marshal(request)
			return core.ToolExecutionResult{Content: string(data), PlatformActionPayload: string(data)}, nil
		},
	}
}

func decodeQQJSON(data []byte, out any) error {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("QQ 参数必须为 JSON 对象")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("QQ 参数无效: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("QQ 参数包含多余 JSON")
	}
	return nil
}

func qqRequestMessages(r QQRequest) []Msg {
	msgs := append([]Msg(nil), r.Arguments.Messages...)
	for _, node := range r.Arguments.Nodes {
		msgs = append(msgs, node.Messages...)
	}
	return msgs
}

func ValidateQQRequest(r QQRequest) error {
	if _, ok := qqHelp[r.Action]; !ok {
		return fmt.Errorf("unknown QQ capability %q", r.Action)
	}
	a := r.Arguments
	// Reject parameters belonging to another capability instead of ignoring a target.
	allowed := map[string]string{"send": "messages target_group_id", "forward_bundle": "nodes source summary prompt news target_group_id", "forward_message": "message_id target_group_id", "poke": "user_id", "reaction": "message_id emoji_id set", "recall": "message_id"}[r.Action]
	b, _ := json.Marshal(a)
	var fields map[string]any
	_ = json.Unmarshal(b, &fields)
	for key := range fields {
		if !strings.Contains(" "+allowed+" ", " "+key+" ") {
			return fmt.Errorf("%s does not accept %s", r.Action, key)
		}
	}
	if a.TargetGroupID != "" {
		if _, err := qqextend.ParseGroupID(a.TargetGroupID); err != nil {
			return err
		}
	}
	switch r.Action {
	case "send":
		return ValidateQQMessages(a.Messages, false)
	case "forward_bundle":
		if len(a.Nodes) == 0 || len(a.Nodes) > 50 {
			return fmt.Errorf("nodes requires 1..50 entries")
		}
		for _, n := range a.Nodes {
			if (n.MessageID == "") == (len(n.Messages) == 0) {
				return fmt.Errorf("node requires either message_id or messages")
			}
			if n.MessageID != "" {
				continue
			}
			if err := ValidateQQMessages(n.Messages, true); err != nil {
				return err
			}
		}
	case "forward_message", "recall", "reaction":
		if strings.TrimSpace(a.MessageID) == "" {
			return fmt.Errorf("message_id is required")
		}
		if r.Action == "reaction" && strings.TrimSpace(a.EmojiID) == "" {
			return fmt.Errorf("emoji_id is required")
		}
	}
	return nil
}

func ValidateQQMessages(messages []Msg, inForward bool) error {
	if len(messages) == 0 || len(messages) > 50 {
		return fmt.Errorf("messages requires 1..50 entries")
	}
	for i, m := range messages {
		if inForward && (m.Type == "file" || m.Type == "quote" || m.Type == "mention_specific" || m.Type == "mention_all" || m.Path != "" || m.Thumb != "" && !webURL(m.Thumb)) {
			return fmt.Errorf("messages[%d] is unsupported inside forward nodes", i)
		}
		if m.Type == "mention_specific" {
			return fmt.Errorf("use send_message for mention_specific")
		}
		if err := validateQQComponent(m); err != nil {
			return fmt.Errorf("messages[%d]: %w", i, err)
		}
	}
	return nil
}

func webURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

func validateQQComponent(m Msg) error {
	if m.Transfer != "" && m.Transfer != "auto" && m.Transfer != "shared" && m.Transfer != "inline" {
		return fmt.Errorf("transfer must be auto/shared/inline")
	}
	if m.FolderID != "" && m.Type != "file" {
		return fmt.Errorf("folder_id requires file")
	}
	if m.Thumb != "" && m.Type != "video" {
		return fmt.Errorf("thumb requires video")
	}
	if m.Summary != "" && m.Type != "image" {
		return fmt.Errorf("summary requires image")
	}
	switch m.Type {
	case "plain":
		if strings.TrimSpace(m.Text) == "" {
			return fmt.Errorf("text is required")
		}
	case "mention_user":
		if !positiveQQID(m.MentionUserID) {
			return fmt.Errorf("mention_user_id must be a numeric ID")
		}
	case "quote":
		if m.MessageID == "" {
			return fmt.Errorf("message_id is required")
		}
	case "image", "record", "video", "file":
		if (m.Path == "") == (m.URL == "") {
			return fmt.Errorf("provide exactly one of path or url")
		}
		if m.URL != "" && !webURL(m.URL) {
			return fmt.Errorf("url must use HTTP(S)")
		}
		if m.Name != "" && (strings.ContainsAny(m.Name, "/\\\x00") || m.Name == "." || m.Name == "..") {
			return fmt.Errorf("name must be a filename")
		}
		if strings.Contains(m.Thumb, "://") && !webURL(m.Thumb) {
			return fmt.Errorf("thumb must be a path or HTTP(S) URL")
		}
	case "mention_all", "dice", "rps":
		if m.Data != nil {
			return fmt.Errorf("%s does not accept data", m.Type)
		}
	case "face", "contact", "music", "share", "location", "mface":
		if m.Type == "mface" && m.MessageID != "" {
			if m.StickerIndex < 0 || m.Data != nil {
				return fmt.Errorf("observed mface requires message_id, nonnegative sticker_index and no data")
			}
			return nil
		}
		if m.Data == nil {
			return fmt.Errorf("data is required")
		}
		d := m.Data
		for _, u := range []string{d.URL, d.Image, d.Audio} {
			if u != "" && !webURL(u) {
				return fmt.Errorf("card URLs must use HTTP(S)")
			}
		}
		switch m.Type {
		case "face":
			if !decimalString(d.ID) {
				return fmt.Errorf("face id must be numeric")
			}
		case "contact":
			if (d.Type != "qq" && d.Type != "group") || !positiveQQID(d.ID) {
				return fmt.Errorf("contact requires type=qq/group and numeric id")
			}
		case "music":
			if d.Type == "custom" {
				if d.URL == "" || d.Audio == "" || strings.TrimSpace(d.Title) == "" {
					return fmt.Errorf("custom music requires url, audio, title")
				}
			} else if !strings.Contains(" qq 163 kugou migu kuwo ", " "+d.Type+" ") || d.Type == "" || d.ID == "" {
				return fmt.Errorf("music requires a supported provider and id")
			}
		case "share":
			if d.URL == "" || strings.TrimSpace(d.Title) == "" {
				return fmt.Errorf("share requires url and title")
			}
		case "location":
			if d.Lat == nil || d.Lon == nil || math.IsNaN(*d.Lat) || math.IsNaN(*d.Lon) || math.IsInf(*d.Lat, 0) || math.IsInf(*d.Lon, 0) || math.Abs(*d.Lat) > 90 || math.Abs(*d.Lon) > 180 {
				return fmt.Errorf("location requires valid lat/lon")
			}
		case "mface":
			if !marketFaceID(d.EmojiID) || !positiveQQID(d.EmojiPackageID) || d.Key == "" {
				return fmt.Errorf("mface requires emoji_id, emoji_package_id, key")
			}
		}
	default:
		return fmt.Errorf("unsupported component %q", m.Type)
	}
	return nil
}

func decimalString(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func positiveQQID(s string) bool { return decimalString(s) && strings.Trim(s, "0") != "" }

func marketFaceID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
