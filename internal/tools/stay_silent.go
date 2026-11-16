package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
)

// StaySilentTool creates the explicit terminal decision used when the current
// private or group message genuinely needs no reply. The tool accepts no
// arguments and must be invoked alone; exclusivity is enforced by the agent
// loop before any sibling tool can execute.
func StaySilentTool() Tool {
	return Tool{
		name:        llm.StaySilentToolName,
		description: "立即结束本轮会话，且不再输出任何最终文字。如果你认为当前消息不值得回复（如无意义字符、单个无上下文表情或明显无需插话内容），请调用；本轮调用工具后，如果决定不再回复，也应调用stay_silent结束轮次。群聊概率触发的主动回复在send_message确认送达后由后端自动结束，无需再调用本工具。一旦打算结束轮次，**严禁额外输出补充内容**，而是直接调用本工具。用户明确要求回复时应正常回答。",
		parameter: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
		execute: func(args string) (string, error) {
			args = strings.TrimSpace(args)
			if args == "" {
				args = "{}"
			}
			var params map[string]any
			if err := json.Unmarshal([]byte(args), &params); err != nil {
				return "", fmt.Errorf("stay_silent 参数不是合法 JSON: %w", err)
			}
			if len(params) != 0 {
				return "", fmt.Errorf("stay_silent 不接受参数")
			}
			return "已选择保持沉默", nil
		},
	}
}
