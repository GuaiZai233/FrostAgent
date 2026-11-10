package tools

import (
	"encoding/json"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"strings"
)

// StaySilentTool creates the explicit terminal decision used when the current
// private or group message genuinely needs no reply. The tool accepts no
// arguments and must be invoked alone; exclusivity is enforced by the agent
// loop before any sibling tool can execute.
func StaySilentTool() Tool {
	return Tool{
		name:        llm.StaySilentToolName,
		description: "立即结束本轮会话，且不再输出任何最终文字。如果你认为当前消息不值得回复（如无意义字符、单个无上下文表情或明显无需插话内容），请调用；本轮已调用过任何工具、触发过主动回复逻辑（并已回复完成），然后决定结束时，也必须调用stay_silent结束轮次。用户明确要求回复时应正常回答。",
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
