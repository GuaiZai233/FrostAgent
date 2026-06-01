package main

import (
	"FrostAgent/internal/llm"

	"github.com/gin-gonic/gin"
)

// AgentRequest 接收的请求体
type AgentRequest struct {
	// Input 保留兼容旧接口；Messages 用于多轮/多上下文对话。
	Input    string            `json:"input"`
	Messages []llm.ChatMessage `json:"messages,omitempty"`
}

// AgentResponse 返回的响应体
type AgentResponse struct {
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

// setupRouter 设置和配置路由
func setupRouter(engine *gin.Engine) {
	// 添加 CORS 中间件
	engine.Use(corsMiddleware())

	// 注册路由
	engine.GET("/health", handleHealth)
	engine.POST("/agent/query", handleAgentQuery)
}

// corsMiddleware CORS 跨域中间件
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
