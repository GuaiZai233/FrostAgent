package main

import (
	"FrostAgent/internal/adapter/astrbot"
	"FrostAgent/internal/adapter/onebot"
	"FrostAgent/internal/billing"
	"FrostAgent/internal/core"
	"FrostAgent/internal/frontend"
	"FrostAgent/internal/groupsummary"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/memory"
	"FrostAgent/internal/provider/llm/openai"
	"FrostAgent/internal/service/botstatus"
	"FrostAgent/internal/service/dialogue"
	logsvc "FrostAgent/internal/service/logs"
	memsvc "FrostAgent/internal/service/memory"
	"FrostAgent/internal/service/settings"
	stickersvc "FrostAgent/internal/service/sticker"
	"FrostAgent/internal/sticker"
	"FrostAgent/internal/tools"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	pbconnect "FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"

	"github.com/joho/godotenv"
)

// 全局引擎实例
var GlobalEngine *llm.Engine

// 全局 memory store
var globalStore *memory.Store

// Sticker subsystem
var globalStickerStore *sticker.Store
var globalStickerStealer *sticker.Stealer
var globalStickerSummarizer *sticker.Summarizer

const version = "0.1.0"

// brainPath returns the path to brain.json, defaulting to data/brain.json.
func brainPath() string {
	if p := os.Getenv("BRAIN_PATH"); p != "" {
		return p
	}
	return "data/brain.json"
}

// catalogPath returns the independent, replaceable reflection catalog path.
func catalogPath() string {
	return filepath.Join(filepath.Dir(brainPath()), "memory_catalog.json")
}

// groupSummaryPath keeps durable summaries beside brain.json without mixing
// them into the memory store.
func groupSummaryPath() string {
	return filepath.Join(filepath.Dir(brainPath()), "group_summaries.json")
}

// dialoguePath returns the path to dialogue.yml, defaulting to eval/dialogue/dialogue.yml.
func dialoguePath() string {
	if p := os.Getenv("DIALOGUE_PATH"); p != "" {
		return p
	}
	return "eval/dialogue/dialogue.yml"
}

func stickerDir() string {
	return filepath.Join(filepath.Dir(brainPath()), "sticker")
}

// ensureDataDir ensures the data directory exists for brain.json.
func ensureDataDir() {
	dir := filepath.Dir(brainPath())
	os.MkdirAll(dir, 0755)
}

func durationFromEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		logs.Warn(
			logs.SYSTEM,
			fmt.Sprintf("%s=%q 不是有效的正数时长，使用默认值 %s", name, raw, fallback),
		)
		return fallback
	}
	return value
}

func positiveIntFromEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		logs.Warn(logs.SYSTEM, fmt.Sprintf("%s=%q 不是有效的正整数，使用默认值 %d", name, raw, fallback))
		return fallback
	}
	return value
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

	// Initialize LLM clients
	llmHTTPClient := llm.NewClient() // HTTP client wrapper for SubAgentTool
	llmClient := openai.NewClient(os.Getenv("UPSTREAM_ENDPOINT"), os.Getenv("UPSTREAM_API_KEY"))
	memoryConfig := memory.DefaultConfig()
	memoryConfig.ReflectTimeout = durationFromEnv(
		"MEMORY_REFLECTION_TIMEOUT",
		memoryConfig.ReflectTimeout,
	)
	reflectionLLMClient := openai.NewClientWithTimeout(
		os.Getenv("UPSTREAM_ENDPOINT"),
		os.Getenv("UPSTREAM_API_KEY"),
		memoryConfig.ReflectTimeout,
	)

	// Initialize memory system
	globalStore = memory.NewStore(brainPath())
	reader := memory.NewReader(globalStore, 20)
	writer := memory.NewWriter(globalStore)
	writer.SetLLM(llmClient, os.Getenv("MODEL_NAME"))
	groupSummaryStore, err := groupsummary.NewStore(groupSummaryPath())
	if err != nil {
		logs.Error(logs.SYSTEM, fmt.Sprintf("群聊总结存储不可用，已保护原文件: %v", err))
	}
	sessionManager := llm.NewSessionManager()
	sessionManager.SetGroupSummaryStore(groupSummaryStore)
	groupCompactBufferSize := positiveIntFromEnv("GROUP_COMPACT_BUFFER_SIZE", 20)
	groupCompactMaxBufferSize := positiveIntFromEnv("GROUP_COMPACT_MAX_BUFFER_SIZE", 0)
	groupCompactMinInterval := durationFromEnv("GROUP_COMPACT_MIN_INTERVAL", 30*time.Second)
	groupCompactor := llm.NewGroupCompactor(
		llmClient,
		groupSummaryStore,
		os.Getenv("MODEL_NAME"),
		groupCompactBufferSize,
		groupCompactMinInterval,
	)
	if groupCompactMaxBufferSize > 0 {
		if groupCompactMaxBufferSize < groupCompactBufferSize {
			logs.Warn(
				logs.SYSTEM,
				fmt.Sprintf(
					"GROUP_COMPACT_MAX_BUFFER_SIZE (%d) < GROUP_COMPACT_BUFFER_SIZE (%d)，已自动修正为 %d 以确保压缩能正常触发",
					groupCompactMaxBufferSize,
					groupCompactBufferSize,
					groupCompactBufferSize,
				),
			)
		}
		groupCompactor.SetMaxBufferSize(groupCompactMaxBufferSize)
	}
	// Gateway: owner + visibility filtering
	gateway := memory.NewGateway()
	// Reflection: background, owner-isolated topic catalog generation
	catalog := memory.NewCatalogStore(catalogPath())
	reflector := memory.NewReflector(
		globalStore,
		catalog,
		reflectionLLMClient,
		os.Getenv("MODEL_NAME"),
		memoryConfig,
	)
	logs.Info(
		logs.SYSTEM,
		fmt.Sprintf("✓ 记忆反思独立超时: %s", memoryConfig.ReflectTimeout),
	)
	reflections := memory.NewReflectionManager(reflector)

	// Register tools
	registry := make(map[string]tools.Tool)
	sendMsgTool := tools.SendMsgTool()
	registry[sendMsgTool.Name()] = sendMsgTool
	staySilentTool := tools.StaySilentTool()
	registry[staySilentTool.Name()] = staySilentTool

	subAgentTool := tools.SubAgentTool(llmHTTPClient)
	registry[subAgentTool.Name()] = subAgentTool

	// Initialize sticker subsystem
	var stickerVision sticker.VisionCaller
	if visionModel := os.Getenv("VISUAL_MODEL_NAME"); visionModel != "" {
		stickerVision = &sticker.LLMVisionCaller{
			Provider:  llmClient,
			ModelName: visionModel,
		}
	}
	var err2 error
	globalStickerStore, err2 = sticker.NewStore(stickerDir())
	if err2 != nil {
		logs.Error(logs.SYSTEM, fmt.Sprintf("sticker store init failed: %v", err2))
	} else {
		globalStickerSummarizer = sticker.NewSummarizer(globalStickerStore, stickerVision)
		globalStickerStealer = sticker.NewStealer(globalStickerStore, globalStickerSummarizer)
		stickerTool := tools.SendStickerTool(globalStickerStore)
		registry[stickerTool.Name()] = stickerTool
		logs.Info(logs.SYSTEM, "✓ 表情包摘取子系统已初始化")
		globalStickerSummarizer.EnqueueUnsummarized()
	}

	executorMap := make(map[string]llm.ToolExecutor)
	for name, tool := range registry {
		executorMap[name] = tool
	}

	// Initialize billing system
	billingCfg := billing.LoadConfigFromEnv()
	billingClient, err := billing.InitBillingClient(billingCfg)
	if err != nil {
		logs.Error(logs.SYSTEM, fmt.Sprintf("计费系统初始化失败: %v", err))
	}

	// Initialize dialogue examples prompt for persona enhancement
	var dialoguePrompt string
	dPath := dialoguePath()
	if prompt, err := llm.LoadDialoguePrompt(dPath); err != nil {
		if !os.IsNotExist(err) {
			logs.Warn(logs.SYSTEM, fmt.Sprintf("加载示例对话失败 (%s): %v", dPath, err))
		}
	} else if prompt != "" {
		dialoguePrompt = prompt
		logs.Info(logs.SYSTEM, fmt.Sprintf("✓ 加载人设示例对话: %s", dPath))
	}

	dispatcher := core.NewDefaultDispatcher()

	GlobalEngine = &llm.Engine{
		MaxIterations:  5,
		ToolRegistry:   executorMap,
		Provider:       llmClient,
		BaseURL:        os.Getenv("UPSTREAM_ENDPOINT"),
		APIKey:         os.Getenv("UPSTREAM_API_KEY"),
		ModelName:      os.Getenv("MODEL_NAME"),
		SessionManager: sessionManager,
		Dispatcher:     dispatcher,
		StartedAt:      time.Now(),
		Version:        version,
		// Billing components
		BillingClient: billingClient,
		BillingConfig: billingCfg,
		// Memory components
		MemoryReader:      reader,
		MemoryWriter:      writer,
		MemoryGateway:     gateway,
		MemoryCatalog:     catalog,
		MemoryReflections: reflections,
		GroupCompactor:    groupCompactor,
		GroupSummaryStore: groupSummaryStore,
		// Persona dialogue prompt
		DialoguePrompt: dialoguePrompt,
	}
	logs.Info(
		logs.SYSTEM,
		fmt.Sprintf(
			"✓ 群聊 running compact 已启用 (buffer: %d, min interval: %s)",
			groupCompactBufferSize,
			groupCompactMinInterval,
		),
	)

	logs.Info(logs.SYSTEM, "✓ 智能体引擎初始化完成")
	// Register memory tool (must be after GlobalEngine assignment)
	memTool := tools.NewMemoryTool(GlobalEngine)
	GlobalEngine.ToolRegistry[memTool.Name()] = memTool
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

	memoryPath, memoryHandler := pbconnect.NewMemoryServiceHandler(
		memsvc.New(globalStore, GlobalEngine.MemoryReflections),
	)
	mux.Handle(memoryPath, memoryHandler)

	dialogueServicePath, dialogueHandler := pbconnect.NewDialogueServiceHandler(
		dialogue.New(dialoguePath(), GlobalEngine),
	)
	mux.Handle(dialogueServicePath, dialogueHandler)

	if globalStickerStore != nil {
		stickerSvc := stickersvc.New(globalStickerStore, globalStickerSummarizer)
		stickerPath, stickerHandler := pbconnect.NewStickerServiceHandler(stickerSvc)
		mux.Handle(stickerPath, stickerHandler)
		mux.HandleFunc("/api/sticker/", stickerSvc.ImageHandler())
	}

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

	// OneBot WebSocket 服务
	if os.Getenv("ENABLE_ONEBOT_ADAPTER") != "false" {
		onebotPath := os.Getenv("ONEBOT_WS_PATH")
		if onebotPath == "" {
			onebotPath = "/ws/frostagent"
		}
		onebotAdapter := onebot.NewAdapter(GlobalEngine)
		if globalStickerStealer != nil {
			onebotAdapter.SetStealer(globalStickerStealer)
		}
		if GlobalEngine != nil && GlobalEngine.Dispatcher != nil {
			GlobalEngine.Dispatcher.RegisterAdapter(onebotAdapter)
		}
		http.HandleFunc(onebotPath, onebotAdapter.Handler())
		logs.Info(logs.WEBSOCKET, fmt.Sprintf("✓ OneBot WebSocket 适配器已挂载: %s", onebotPath))
	}

	// AstrBot WebSocket 服务
	if os.Getenv("ENABLE_ASTRBOT_ADAPTER") != "false" {
		astrbotPath := os.Getenv("ASTRBOT_WS_PATH")
		if astrbotPath == "" {
			astrbotPath = "/ws/astrbot"
		}
		astrbotAdapter := astrbot.NewAdapter(GlobalEngine)
		if globalStickerStealer != nil {
			astrbotAdapter.SetStealer(globalStickerStealer)
		}
		if GlobalEngine != nil && GlobalEngine.Dispatcher != nil {
			GlobalEngine.Dispatcher.RegisterAdapter(astrbotAdapter)
		}
		http.HandleFunc(astrbotPath, astrbotAdapter.Handler())
		logs.Info(logs.WEBSOCKET, fmt.Sprintf("✓ AstrBot WebSocket 适配器已挂载: %s", astrbotPath))
	}

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
