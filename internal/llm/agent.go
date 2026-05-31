package llm

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"FrostAgent/internal/tools"
)

const (
	defaultMaxContextMessages = 20
	defaultMaxContextChars    = 24000
)

// Engine 结构体，用于管理智能体的执行
type Engine struct {
	MaxIterations      int
	ToolRegistry       map[string]tools.Tool
	LLMClient          *Client // API 客户端
	BaseURL            string  // API 地址
	APIKey             string  // API 密钥
	ModelName          string  // 模型名称
	MaxContextMessages int     // 最大保留消息数（包含 system），0 使用默认值
	MaxContextChars    int     // 近似上下文字符上限，0 使用默认值
}

// Run 使用单条用户输入执行智能体，保留原有调用方式。
func (e *Engine) Run(prompt string) string {
	return e.RunMessages([]ChatMessage{{Role: "user", Content: prompt}})
}

// RunWithMessages 兼容分支早期命名，内部委托给 RunMessages。
func (e *Engine) RunWithMessages(messages []ChatMessage) string {
	return e.RunMessages(messages)
}

// RunMessages 使用调用方传入的消息历史执行智能体。
// messages 支持连续对话列表；如果没有 system 消息，会自动补上 SYSTEM_PROMPT。
func (e *Engine) RunMessages(messages []ChatMessage) string {
	messages = e.prepareMessages(messages)
	if len(messages) == 0 {
		return "输入不能为空"
	}

	//转换工具注册表为大模型看得懂的形式
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

	//主循环
	for i := 0; i < e.MaxIterations; i++ {
		fmt.Printf("【第%d轮思考开始】\n", i+1)
		messages = e.trimMessages(messages)

		// 调用internal/llm 包向大模型发送http请求，messages 中包含完整多轮上下文
		responseMsg, err := e.LLMClient.CallAPI(e.BaseURL, e.APIKey, e.ModelName, messages, modelTools)
		if err != nil {
			return fmt.Sprintf("LLM掉线了: %v", err)
		}

		//记下回复
		messages = append(messages, *responseMsg)
		messages = e.trimMessages(messages)

		//是否给出最终答案
		if len(responseMsg.ToolCalls) == 0 {
			fmt.Println("【智能体给出最终答案】")
			return stringifyContent(responseMsg.Content)
		}

		//发现有指令，开始干活
		for _, tc := range responseMsg.ToolCalls {
			fmt.Printf("【智能体调用工具】%s，参数: %s\n", tc.Function.Name, tc.Function.Arguments)

			var toolResult string

			//从map中找到工具执行
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

			//把结果包装成role=tool的消息，记录
			toolMsg := ChatMessage{
				Role:       "tool",
				Content:    toolResult,
				ToolCallID: tc.ID, // 关联到调用ID，方便大模型理解
			}
			messages = append(messages, toolMsg)
		}
		messages = e.trimMessages(messages)
		//循环进入下一轮
	}
	return "达到最大迭代次数，未能得出最终答案"
}

func (e *Engine) prepareMessages(messages []ChatMessage) []ChatMessage {
	prepared := make([]ChatMessage, 0, len(messages)+1)
	hasSystem := false
	for _, msg := range messages {
		msg.Role = strings.TrimSpace(msg.Role)
		if msg.Role == "" || (msg.Content == nil && len(msg.ToolCalls) == 0) {
			continue
		}
		if msg.Role == "system" {
			hasSystem = true
		}
		prepared = append(prepared, msg)
	}

	if !hasSystem {
		systemPrompt := os.Getenv("SYSTEM_PROMPT")
		if systemPrompt != "" {
			prepared = append([]ChatMessage{{Role: "system", Content: systemPrompt}}, prepared...)
		}
	}

	return e.trimMessages(prepared)
}

func (e *Engine) trimMessages(messages []ChatMessage) []ChatMessage {
	maxMessages := e.MaxContextMessages
	if maxMessages <= 0 {
		maxMessages = intFromEnv("MAX_CONTEXT_MESSAGES", defaultMaxContextMessages)
	}
	maxChars := e.MaxContextChars
	if maxChars <= 0 {
		maxChars = intFromEnv("MAX_CONTEXT_CHARS", defaultMaxContextChars)
	}

	var systemMessages []ChatMessage
	var nonSystem []ChatMessage
	for _, msg := range messages {
		if msg.Role == "system" {
			systemMessages = append(systemMessages, msg)
		} else {
			nonSystem = append(nonSystem, msg)
		}
	}

	// 优先保留最早的 system，其余消息从尾部保留，避免长期会话无限增长。
	if len(systemMessages) > 1 {
		systemMessages = systemMessages[:1]
	}
	allowedNonSystem := maxMessages - len(systemMessages)
	if allowedNonSystem < 1 {
		allowedNonSystem = 1
	}
	if len(nonSystem) > allowedNonSystem {
		nonSystem = nonSystem[len(nonSystem)-allowedNonSystem:]
	}

	trimmed := append([]ChatMessage{}, systemMessages...)
	trimmed = append(trimmed, nonSystem...)

	for estimateMessagesChars(trimmed) > maxChars && len(trimmed) > len(systemMessages)+1 {
		// 删除最旧的非 system 消息；如果开头是 system，删后一条。
		deleteIndex := 0
		if len(systemMessages) > 0 {
			deleteIndex = len(systemMessages)
		}
		trimmed = append(trimmed[:deleteIndex], trimmed[deleteIndex+1:]...)
	}

	return normalizeToolHistory(trimmed)
}

// normalizeToolHistory 移除没有对应 assistant tool_calls 的孤立 tool 消息，
// 防止裁剪历史后 OpenAI 兼容接口因工具调用链不完整而报错。
func normalizeToolHistory(messages []ChatMessage) []ChatMessage {
	validToolCallIDs := make(map[string]bool)
	for _, msg := range messages {
		if msg.Role != "assistant" {
			continue
		}
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" {
				validToolCallIDs[tc.ID] = true
			}
		}
	}

	filtered := messages[:0]
	for _, msg := range messages {
		if msg.Role == "tool" && !validToolCallIDs[msg.ToolCallID] {
			continue
		}
		filtered = append(filtered, msg)
	}
	return filtered
}

func estimateMessagesChars(messages []ChatMessage) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Role) + len(msg.ToolCallID) + len(stringifyContent(msg.Content))
		for _, tc := range msg.ToolCalls {
			total += len(tc.ID) + len(tc.Type) + len(tc.Function.Name) + len(tc.Function.Arguments)
		}
	}
	return total
}

func stringifyContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		bytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(bytes)
	}
}

func intFromEnv(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
