package main

import (
	"FrostAgent/internal/adapter/onebot"
	"FrostAgent/internal/frontend"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/provider/llm/openai"
	"FrostAgent/internal/service/botstatus"
	logsvc "FrostAgent/internal/service/logs"
	"FrostAgent/internal/service/settings"
	"FrostAgent/internal/tools"
	"fmt"
	"net/http"
	"os"
	"time"

	pbconnect "FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"

	"github.com/joho/godotenv"
)

// 全局引擎实例
var GlobalEngine *llm.Engine

const version = "0.1.0"

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
	sendMsgTool := tools.SendMsgTool()
	registry[sendMsgTool.Name()] = sendMsgTool

	subAgentTool := tools.SubAgentTool(llmClient)
	registry[subAgentTool.Name()] = subAgentTool

	weatherTool := tools.GetWeatherTool()
	registry[weatherTool.Name()] = weatherTool

	gameVersionTool := tools.GetGameVersionTool()
	registry[gameVersionTool.Name()] = gameVersionTool

	executorMap := make(map[string]llm.ToolExecutor)
	for name, tool := range registry {
		executorMap[name] = tool
	}

	GlobalEngine = &llm.Engine{
		MaxIterations:  5,
		ToolRegistry:   executorMap,
		Provider:       openai.NewClient(os.Getenv("UPSTREAM_ENDPOINT"), os.Getenv("UPSTREAM_API_KEY")),
		BaseURL:        os.Getenv("UPSTREAM_ENDPOINT"),
		APIKey:         os.Getenv("UPSTREAM_API_KEY"),
		ModelName:      os.Getenv("MODEL_NAME"),
		SessionManager: llm.NewSessionManager(),
		StartedAt:      time.Now(),
		Version:        version,
	}

	logs.Info(logs.SYSTEM, "✓ 智能体引擎初始化完成")
}

func main() {
	mux := http.NewServeMux()

	// ConnectRPC 服务注册
	botPath, botHandler := pbconnect.NewBotStatusServiceHandler(botstatus.New(GlobalEngine, version))
	mux.Handle(botPath, botHandler)

	settingsPath, settingsHandler := pbconnect.NewSettingsServiceHandler(settings.New(".env"))
	mux.Handle(settingsPath, settingsHandler)

	logsPath, logsHandler := pbconnect.NewLogServiceHandler(logsvc.New())
	mux.Handle(logsPath, logsHandler)

	// 前端 SPA（兜底，放在最后）
	mux.Handle("/", frontend.Handler())

	// CORS 中间件
	handler := corsMiddleware(mux)

	// HTTP 服务 (ConnectRPC + 前端)
	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8080"
	}

	go func() {
		logs.Info(logs.SYSTEM, "🚀 FrostAgent 智能体服务已启动")
		logs.Info(logs.SYSTEM, fmt.Sprintf("📍 管理面板: http://localhost%s", listenAddr))
		logs.Info(logs.SYSTEM, fmt.Sprintf("📡 ConnectRPC: http://localhost%s/frostagent.v1.BotStatusService/GetOverview", listenAddr))

		if err := http.ListenAndServe(listenAddr, handler); err != nil {
			logs.Error(logs.SYSTEM, fmt.Sprintf("HTTP 服务启动失败: %v", err))
			os.Exit(1)
		}
	}()

	// OneBot WebSocket 服务（保持不变）
	http.HandleFunc("/ws/frostagent", onebot.HandleWS(GlobalEngine))

	wsAddr := os.Getenv("WS_LISTEN_ADDR")
	if wsAddr == "" {
		wsAddr = "0.0.0.0:1234"
	}

	logs.Info(logs.WEBSOCKET, fmt.Sprintf("FrostAgent WebSocket 服务已启动，监听 %s", wsAddr))
	if err := http.ListenAndServe(wsAddr, nil); err != nil {
		logs.Error(logs.WEBSOCKET, fmt.Sprintf("WS 服务启动失败: %v", err))
		os.Exit(1)
	}
}

// corsMiddleware 作为标准 http.Handler 包装器
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}

		next.ServeHTTP(w, r)
	})
}
