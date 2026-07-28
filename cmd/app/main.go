package main

import (
	"FrostAgent/internal/adapter/onebot"
	"FrostAgent/internal/frontend"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/memory"
	"FrostAgent/internal/provider/llm/openai"
	"FrostAgent/internal/service/botstatus"
	logsvc "FrostAgent/internal/service/logs"
	"FrostAgent/internal/service/settings"
	"FrostAgent/internal/tools"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	pbconnect "FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"

	"github.com/joho/godotenv"
)

// 全局引擎实例
var GlobalEngine *llm.Engine

const version = "0.1.0"

// brainPath returns the path to brain.json, defaulting to data/brain.json.
func brainPath() string {
	if p := os.Getenv("BRAIN_PATH"); p != "" {
		return p
	}
	return "data/brain.json"
}

// vectorPath returns the path to vectors.json, derived from the brain path.
func vectorPath() string {
	bp := brainPath()
	dir := filepath.Dir(bp)
	name := filepath.Base(bp)
	// e.g. data/brain.json -> data/vectors.json
	name = "vectors.json"
	return filepath.Join(dir, name)
}

// ensureDataDir ensures the data directory exists for brain.json.
func ensureDataDir() {
	dir := filepath.Dir(brainPath())
	os.MkdirAll(dir, 0755)
}

func init() {
	// 加载 .env 文件
	if err := godotenv.Load(); err != nil {
		fmt.Println("未找到 .env 文件，将使用默认配置")
	}

	// 初始化日志系统，缓冲区大小 5000
	logs.Init(5000)

	logs.Info(logs.SYSTEM, "正在初始化智能体引擎...")

	// Ensure data directory exists
	ensureDataDir()

	// Initialize LLM client (both the low-level and provider-level)
	llmClientBase := llm.NewClient()
	llmClient := openai.NewClient(os.Getenv("UPSTREAM_ENDPOINT"), os.Getenv("UPSTREAM_API_KEY"))

	// Initialize memory system
	store := memory.NewStore(brainPath())

	var vs *memory.VectorStore
	embedModel := os.Getenv("EMBEDDING_MODEL")
	if embedModel != "" {
		embedder := openai.NewEmbedder(llmClient, embedModel)
		vs = memory.NewVectorStore(vectorPath(), embedder)
		logs.Info(logs.SYSTEM, fmt.Sprintf("✓ 向量检索已启用 (模型: %s)", embedModel))
	} else {
		logs.Info(logs.SYSTEM, "⚠ 向量检索未配置 (EMBEDDING_MODEL 未设置)，使用关键词检索")
	}

	// Reader: hybrid search (vector + keyword)
	reader := memory.NewReader(store, vs, 20)
	// Writer: auto-extract + auto-index
	writer := memory.NewWriter(store)
	writer.SetLLM(llmClient, os.Getenv("MODEL_NAME"))
	if vs != nil {
		writer.SetVectorStore(vs)
	}
	// Gateway: owner + visibility filtering
	gateway := memory.NewGateway()

	// Register tools
	registry := make(map[string]tools.Tool)
	sendMsgTool := tools.SendMsgTool()
	registry[sendMsgTool.Name()] = sendMsgTool

	subAgentTool := tools.SubAgentTool(llmClientBase)
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
		Provider:       llmClient,
		BaseURL:        os.Getenv("UPSTREAM_ENDPOINT"),
		APIKey:         os.Getenv("UPSTREAM_API_KEY"),
		ModelName:      os.Getenv("MODEL_NAME"),
		SessionManager: llm.NewSessionManager(),
		StartedAt:      time.Now(),
		Version:        version,
		// Memory components
		MemoryReader:  reader,
		MemoryWriter:  writer,
		MemoryGateway: gateway,
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