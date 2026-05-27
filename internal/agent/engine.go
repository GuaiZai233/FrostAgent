package agent

import (
	"fmt"

	"FrostAgent/internal/llm"
	"FrostAgent/internal/tools"
)

// Engine 结构体，用于管理智能体的执行
type Engine struct {
	MaxIterations int
	ToolRegistry  map[string]tools.Tool
	LLMClient     *llm.Client // API 客户端
	BaseURL       string      // API 地址
	APIKey        string      // API 密钥
	ModelName     string      // 模型名称
}

// NewEngine 工厂函数，组装新的引擎
func NewEngine(maxIterations int, registry map[string]tools.Tool) *Engine {
	return &Engine{
		MaxIterations: maxIterations,
		ToolRegistry:  registry,
	}
}

// Run 执行智能体的主循环
func (e *Engine) Run(prompt string) string {
	//初始化上下文
	messages := []llm.ChatMessage{
		{Role: "user", Content: prompt},
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

		// 调用internal/llm 包向大模型发送http请求
		responseMsg, err := e.LLMClient.CallAPI(e.BaseURL, e.APIKey, e.ModelName, messages, modelTools)
		if err != nil {
			return fmt.Sprintf("LLM掉线了: %v", err)
		}

		//记下回复
		messages = append(messages, *responseMsg)

		//是否给出最终答案
		if len(responseMsg.ToolCalls) == 0 {
			fmt.Println("【智能体给出最终答案】")
			return responseMsg.Content
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
			toolMsg := llm.ChatMessage{
				Role:       "tool",
				Content:    toolResult,
				ToolCallID: tc.ID, // 关联到调用ID，方便大模型理解
			}
			messages = append(messages, toolMsg)
		}
		//循环进入下一轮
	}
	return "达到最大迭代次数，未能得出最终答案"

}
