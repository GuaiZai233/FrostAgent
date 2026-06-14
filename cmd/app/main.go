package main

import (
	"FrostAgent/internal/adapter/onebot"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/provider/llm/openai"
	"FrostAgent/internal/tools"
	"fmt"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

// 全局引擎实例
var GlobalEngine *llm.Engine

func init() {
	// 加载 .env 文件
	if err := godotenv.Load(); err != nil {
		fmt.Println("未找到 .env 文件，将使用默认配置")
	}

	// 初始化日志系统，缓冲区大小 5000
	logs.Init(5000)

	logs.Info(logs.SYSTEM, "正在初始化智能体引擎...")

	llmClient := llm.NewClient()

	// 注册工具
	registry := make(map[string]tools.Tool)
	// 系统工具
	sendMsgTool := tools.SendMsgTool()
	registry[sendMsgTool.Name()] = sendMsgTool

	subAgentTool := tools.SubAgentTool(llmClient)
	registry[subAgentTool.Name()] = subAgentTool

	// 附加工具
	weatherTool := tools.GetWeatherTool()
	registry[weatherTool.Name()] = weatherTool

	gameVersionTool := tools.GetGameVersionTool()
	registry[gameVersionTool.Name()] = gameVersionTool

	executorMap := make(map[string]llm.ToolExecutor)
	for name, tool := range registry {
		executorMap[name] = tool
	}

	GlobalEngine = &llm.Engine{
		MaxIterations: 5,
		ToolRegistry:  executorMap,
		Provider:      openai.NewClient(os.Getenv("UPSTREAM_ENDPOINT"), os.Getenv("UPSTREAM_API_KEY")),
		//LLMClient:      llmClient,
		BaseURL:        os.Getenv("UPSTREAM_ENDPOINT"),
		APIKey:         os.Getenv("UPSTREAM_API_KEY"),
		ModelName:      os.Getenv("MODEL_NAME"),
		SessionManager: llm.NewSessionManager(),
	}

	// 设置 onebot 的引擎
	//onebot.SetEngine(GlobalEngine)

	logs.Info(logs.SYSTEM, "✓ 智能体引擎初始化完成")
}

func main() {
	// 创建 Gin 路由
	router := gin.Default()

	// 设置路由
	setupRouter(router)

	go func() {
		// 启动服务器
		listenAddr := os.Getenv("LISTEN_ADDR")
		if listenAddr == "" {
			listenAddr = ":8080"
		}
		
		logs.Info(logs.SYSTEM, "🚀 FrostAgent 智能体服务已启动")
		logs.Info(logs.SYSTEM, fmt.Sprintf("📍 监听地址: http://localhost%s", listenAddr))
		logs.Info(logs.SYSTEM, fmt.Sprintf("📝 查询接口: POST http://localhost%s/agent/query", listenAddr))
		logs.Info(logs.SYSTEM, fmt.Sprintf("✓ 健康检查: GET http://localhost%s/health", listenAddr))

		if err := router.Run(listenAddr); err != nil {
			logs.Error(logs.SYSTEM, fmt.Sprintf("服务器启动失败: %v", err))
			os.Exit(1)
		}
	}()

	//reg reverse ws router
	http.HandleFunc("/ws/frostagent", onebot.HandleWS(GlobalEngine))

	// start server
	addr := os.Getenv("WS_LISTEN_ADDR")
	if addr == "" {
		addr = "0.0.0.0:1234"
	}

	logs.Info(logs.WEBSOCKET, fmt.Sprintf("FrostAgent 服务已启动，监听 %s", addr))
	if err := http.ListenAndServe(addr, nil); err != nil {
		logs.Error(logs.WEBSOCKET, fmt.Sprintf("ws服务启动失败: %v", err))
		os.Exit(1)
	}
}
