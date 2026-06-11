package main

import (
	"FrostAgent/internal/adapter/onebot"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/tools"
	"fmt"
	"log"
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
		log.Println("未找到 .env 文件，将使用默认配置")
	}

	fmt.Println("【初始化】正在初始化智能体引擎...")

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
		//LLMClient:      llmClient,
		BaseURL:        os.Getenv("UPSTREAM_ENDPOINT"),
		APIKey:         os.Getenv("UPSTREAM_API_KEY"),
		ModelName:      os.Getenv("MODEL_NAME"),
		SessionManager: llm.NewSessionManager(),
	}

	// 设置 onebot 的引擎
	//onebot.SetEngine(GlobalEngine)

	//fmt.Println("【初始化】✓ 智能体引擎初始化完成")
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
		fmt.Printf("\n🚀 FrostAgent 智能体服务已启动\n")
		fmt.Printf("📍 监听地址: http://localhost%s\n", listenAddr)
		fmt.Printf("📝 查询接口: POST http://localhost%s/agent/query\n", listenAddr)
		fmt.Printf("✓ 健康检查: GET http://localhost%s/health\n\n", listenAddr)

		if err := router.Run(listenAddr); err != nil {
			log.Fatalf("服务器启动失败: %v", err)
		}
	}()

	//reg reverse ws router
	http.HandleFunc("/ws/frostagent", onebot.HandleWS(GlobalEngine))

	// start server
	addr := os.Getenv("WS_LISTEN_ADDR")
	if addr == "" {
		addr = "0.0.0.0:1234"
	}

	log.Printf("FrostAgent 服务已启动，监听 %s\n", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("ws服务启动失败: %v", err)
	}
}
