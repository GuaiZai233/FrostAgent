package main

import (
	"FrostAgent/internal/llm"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

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
	// 添加通用中间件
	engine.Use(corsMiddleware())
	engine.Use(authMiddleware())
	engine.Use(rateLimitMiddleware())

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


type rateBucket struct {
	count int
	reset time.Time
}

var (
	rateLimitMu      sync.Mutex
	rateLimitBuckets = map[string]rateBucket{}
)

func authMiddleware() gin.HandlerFunc {
	token := strings.TrimSpace(os.Getenv("HTTP_API_TOKEN"))
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodOptions || c.FullPath() == "/health" || token == "" {
			c.Next()
			return
		}
		provided := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
		if provided == "" {
			provided = strings.TrimSpace(c.GetHeader("X-API-Key"))
		}
		if provided != token {
			c.AbortWithStatusJSON(http.StatusUnauthorized, AgentResponse{Error: "unauthorized"})
			return
		}
		c.Next()
	}
}

func rateLimitMiddleware() gin.HandlerFunc {
	limit := 60
	window := time.Minute
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodOptions || c.FullPath() == "/health" {
			c.Next()
			return
		}
		now := time.Now()
		key := c.ClientIP()
		rateLimitMu.Lock()
		bucket := rateLimitBuckets[key]
		if now.After(bucket.reset) {
			bucket = rateBucket{reset: now.Add(window)}
		}
		bucket.count++
		rateLimitBuckets[key] = bucket
		allowed := bucket.count <= limit
		retryAfter := int(time.Until(bucket.reset).Seconds())
		rateLimitMu.Unlock()
		if !allowed {
			if retryAfter < 1 {
				retryAfter = 1
			}
			c.Header("Retry-After", strconv.Itoa(retryAfter))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, AgentResponse{Error: "rate limit exceeded"})
			return
		}
		c.Next()
	}
}
