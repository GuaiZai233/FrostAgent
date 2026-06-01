package llm

import (
	"fmt"
	"os"
	"time"

	"FrostAgent/internal/tools"
)

// Engine 结构体，用于管理智能体的执行
type Engine struct {
	MaxIterations  int
	ToolRegistry   map[string]tools.Tool
	LLMClient      *Client          // API 客户端
	BaseURL        string           // API 地址
	APIKey         string           // API 密钥
	ModelName      string           // 模型名称
	SessionManager *SessionManager  // 会话上下文管理器
}

// Run 执行智能体的主循环（单次无状态调用）
func (e *Engine) Run(prompt string) string {
	systemPrompt := os.Getenv("SYSTEM_PROMPT")
	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	}
	result := e.runLoop(messages)
	return result
}

// RunWithSession 执行智能体的主循环（带会话上下文）
func (e *Engine) RunWithSession(sessionID string, prompt string) string {
	session := e.SessionManager.GetOrCreate(sessionID)

	// 加锁保护会话内部状态
	session.Lock()
	defer session.Unlock()

	messages := session.Messages

	if len(messages) == 0 {
		systemPrompt := os.Getenv("SYSTEM_PROMPT")
		messages = append(messages, ChatMessage{Role: "system", Content: systemPrompt})
	}

	messages = append(messages, ChatMessage{Role: "user", Content: prompt})

	result := e.runLoop(messages)

	// 修改后的 messages 写回
	session.Messages = e.trimMessagesForSession(messages)
	session.UpdatedAt = time.Now()

	return result
}

// runLoop 核心循环逻辑
func (e *Engine) runLoop(messages []ChatMessage) string {
	var modelTools []any
	for _, t := range e.ToolRegistry {
		modelTools = append(modelTools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
			},
		})
	}

	for i := 0; i < e.MaxIterations; i++ {
		fmt.Printf("【第%d轮思考开始】\n", i+1)

		responseMsg, err := e.LLMClient.CallAPI(e.BaseURL, e.APIKey, e.ModelName, messages, modelTools)
		if err != nil {
			return fmt.Sprintf("LLM掉线了: %v", err)
		}

		messages = append(messages, *responseMsg)

		if len(responseMsg.ToolCalls) == 0 {
			fmt.Println("【智能体给出最终答案】")
			contentStr, _ := responseMsg.Content.(string)
			return contentStr
		}

		for _, tc := range responseMsg.ToolCalls {
			fmt.Printf("【智能体调用工具】%s，参数: %s\n", tc.Function.Name, tc.Function.Arguments)

			var toolResult string
			if tool, exists := e.ToolRegistry[tc.Function.Name]; exists {
				res, err := tool.Execute(tc.Function.Arguments)
				if err != nil {
					toolResult = fmt.Sprintf("工具执行失败: %v", err)
				} else {
					toolResult = res
				}
			} else {
				toolResult = "工具未找到"
			}

			fmt.Println("【工具执行结果】", toolResult)

			toolMsg := ChatMessage{
				Role:       "tool",
				Content:    toolResult,
				ToolCallID: tc.ID,
			}
			messages = append(messages, toolMsg)
		}
	}
	return "达到最大迭代次数，未能得出最终答案"
}

// trimMessagesForSession 改进的裁剪逻辑，确保工具链完整
func (e *Engine) trimMessagesForSession(messages []ChatMessage) []ChatMessage {
	max := e.SessionManager.MaxHistory
	if len(messages) <= max+1 {
		return messages
	}

	// 始终保留第一条 system prompt
	startIdx := len(messages) - max

	// 如果起始位置是一条 tool 消息，必须向前追溯到对应的 assistant 消息
	// 否则 API 会报错：tool message must follow assistant message with tool_calls
	for startIdx > 1 && messages[startIdx].Role == "tool" {
		startIdx--
	}

	trimmed := make([]ChatMessage, 0, len(messages)-startIdx+1)
	trimmed = append(trimmed, messages[0])
	trimmed = append(trimmed, messages[startIdx:]...)
	return trimmed
}
