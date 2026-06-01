package main

import (
	"FrostAgent/internal/llm"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// handleAgentQuery 处理智能体查询的接口
func handleAgentQuery(c *gin.Context) {
	var req AgentRequest

	// 绑定 JSON 请求体
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("请求绑定失败: %v\n", err)
		c.JSON(http.StatusBadRequest, AgentResponse{
			Error: "无效的请求: " + err.Error(),
		})
		return
	}

	messages := normalizeRequestMessages(req)
	if len(messages) == 0 {
		c.JSON(http.StatusBadRequest, AgentResponse{
			Error: "input 或 messages 不能为空",
		})
		return
	}

	log.Printf("【收到用户请求】input长度=%d, messages=%d\n", len(req.Input), len(req.Messages))

	// 执行智能体
	result := GlobalEngine.RunMessages(messages)

	c.JSON(http.StatusOK, AgentResponse{
		Result: result,
	})
}

func normalizeRequestMessages(req AgentRequest) []llm.ChatMessage {
	messages := make([]llm.ChatMessage, 0, len(req.Messages)+1)
	for _, msg := range req.Messages {
		msg.Role = strings.TrimSpace(msg.Role)
		if msg.Role == "" || isBlankRequestContent(msg.Content) {
			continue
		}
		messages = append(messages, msg)
	}

	if strings.TrimSpace(req.Input) != "" {
		messages = append(messages, llm.ChatMessage{Role: "user", Content: req.Input})
	}
	return messages
}

func isBlankRequestContent(content any) bool {
	if content == nil {
		return true
	}
	if text, ok := content.(string); ok {
		return strings.TrimSpace(text) == ""
	}
	return false
}

// handleHealth 处理健康检查接口
func handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "FrostAgent 智能体服务运行正常",
	})
}
