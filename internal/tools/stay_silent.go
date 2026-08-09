package tools

import (
	"FrostAgent/internal/llm"
	"encoding/json"
	"fmt"
	"strings"
)

// StaySilentTool creates the explicit terminal decision used when the current
// private or group message genuinely needs no reply. The tool accepts no
// arguments and must be invoked alone; exclusivity is enforced by the agent
// loop before any sibling tool can execute.
func StaySilentTool() Tool {
	return Tool{
		name:        llm.StaySilentToolName,
		description: "当且仅当当前消息确实不值得回复时保持沉默。可用于无意义字符、单个无上下文表情或明显无需插话的内容。用户明确要求回复时应正常回答。此工具会立即结束本轮，必须单独调用，不能与任何其他工具同时使用。",
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
