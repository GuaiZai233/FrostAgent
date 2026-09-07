package instance

import (
	"FrostAgent/internal/adapter/astrbot"
	"FrostAgent/internal/adapter/onebot"
	"FrostAgent/internal/billing"
	"FrostAgent/internal/core"
	"FrostAgent/internal/groupsummary"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/memory"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/runtimescope"
	"FrostAgent/internal/service/botstatus"
	"FrostAgent/internal/service/dialogue"
	logsvc "FrostAgent/internal/service/logs"
	memsvc "FrostAgent/internal/service/memory"
	routersvc "FrostAgent/internal/service/modelrouter"
	"FrostAgent/internal/service/settings"
	stickersvc "FrostAgent/internal/service/sticker"
	"FrostAgent/internal/sticker"
	"FrostAgent/internal/tools"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	pbconnect "FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
)

const version = "0.1.0"

type Runtime struct {
	Scope   *runtimescope.Scope
	Engine  *llm.Engine
	Handler http.Handler
	Onebot  *onebot.Adapter
	Astrbot *astrbot.Adapter
}

func buildRuntime(dir, configDir, prefix string, config, global *instanceconfig.Store, logger *logs.Store, shared *dialogue.Service, billingClient *billing.Client, enabled bool) (*Runtime, error) {
	if config.AccessError() != nil {
		return nil, config.AccessError()
	}
	if enabled && config.Error() != nil {
		return nil, config.Error()
	}
	if err := safeTree(dir); err != nil {
		return nil, err
	}
	scope := runtimescope.New(config, global, logger)
	if !enabled {
		scope.Cancel()
	}
	success := false
	defer func() {
		if !success {
			scope.Cancel()
			scope.Wait()
		}
	}()
	routerManager := modelrouter.New(filepath.Join(configDir, "model_router.json"), scope)
	if err := routerManager.LoadError(); err != nil {
		if enabled {
			return nil, err
		}
		scope.Log().Error(logs.SYSTEM, fmt.Sprintf("模型路由配置不可用，已进入未配置状态: %v", err))
	}
	foregroundProvider := routerManager.Provider(modelrouter.WorkloadDialogue, false, 120*time.Second)
	visionProvider := routerManager.Provider(modelrouter.WorkloadVision, false, 120*time.Second)
	subagentProvider := routerManager.Provider(modelrouter.WorkloadSubagent, false, 120*time.Second)
	memoryProvider := routerManager.Provider(modelrouter.WorkloadMemoryExtract, false, 120*time.Second)
	memoryConfig := memory.DefaultConfig()
	memoryConfig.ReflectTimeout = durationFromEnv(scope,
		"MEMORY_REFLECTION_TIMEOUT",
		memoryConfig.ReflectTimeout,
	)
	reflectionProvider := routerManager.Provider(modelrouter.WorkloadReflection, false, memoryConfig.ReflectTimeout)

	// Initialize memory system
	store := memory.NewStore(filepath.Join(dir, "brain.json"))
	reader := memory.NewReader(store, 20)
	writer := memory.NewWriter(store)
	writer.Scope = scope
	writer.SetLLM(memoryProvider, "model-router-memory-extract")
	groupSummaryStore, err := groupsummary.NewStore(filepath.Join(dir, "group_summaries.json"))
	if err != nil {
		return nil, err
	}
	sessionManager := llm.NewSessionManager(scope)
	sessionManager.SetGroupSummaryStore(groupSummaryStore)
	groupCompactBufferSize := positiveIntFromEnv(scope, "GROUP_COMPACT_BUFFER_SIZE", 20)
	groupCompactMaxBufferSize := positiveIntFromEnv(scope, "GROUP_COMPACT_MAX_BUFFER_SIZE", 0)
	groupCompactMinInterval := durationFromEnv(scope, "GROUP_COMPACT_MIN_INTERVAL", 30*time.Second)
	groupCompactor := llm.NewGroupCompactor(
		routerManager.Provider(modelrouter.WorkloadGroupCompact, false, 120*time.Second),
		groupSummaryStore,
		"model-router-group-compact",
		groupCompactBufferSize,
		groupCompactMinInterval,
	)
	groupCompactor.Scope = scope
	if groupCompactMaxBufferSize > 0 {
		if groupCompactMaxBufferSize < groupCompactBufferSize {
			scope.Log().Warn(
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
	catalog := memory.NewCatalogStore(filepath.Join(dir, "memory_catalog.json"))
	reflector := memory.NewReflector(
		store,
		catalog,
		reflectionProvider,
		"model-router-reflection",
		memoryConfig,
	)
	scope.Log().Info(
		logs.SYSTEM,
		fmt.Sprintf("✓ 记忆反思独立超时: %s", memoryConfig.ReflectTimeout),
	)
	reflector.Scope = scope
	reflections := memory.NewReflectionManager(reflector)
	reflections.Scope = scope

	// Register tools
	registry := make(map[string]tools.Tool)
	sendMsgTool := tools.SendMsgTool()
	registry[sendMsgTool.Name()] = sendMsgTool
	staySilentTool := tools.StaySilentTool()
	registry[staySilentTool.Name()] = staySilentTool

	subAgentTool := tools.SubAgentTool(subagentProvider)
	registry[subAgentTool.Name()] = subAgentTool

	// Initialize sticker subsystem
	stickerVision := &sticker.LLMVisionCaller{Scope: scope,
		Provider:  visionProvider,
		ModelName: "model-router-vision",
	}
	var summarizer *sticker.Summarizer
	var stealer *sticker.Stealer
	stickerStore, err2 := sticker.NewStore(filepath.Join(dir, "sticker"))
	if err2 != nil {
		return nil, err2
	} else {
		summarizer = sticker.NewSummarizer(stickerStore, stickerVision, scope)
		stealer = sticker.NewStealer(stickerStore, summarizer)
		stickerTool := tools.SendStickerTool(stickerStore, prefix)
		registry[stickerTool.Name()] = stickerTool
		stealStickerTool := tools.StealStickerTool(stealer)
		registry[stealStickerTool.Name()] = stealStickerTool
		scope.Log().Info(logs.SYSTEM, "✓ 表情包摘取子系统已初始化")
		if enabled {
			summarizer.EnqueueUnsummarized()
		}
	}

	executorMap := make(map[string]llm.ToolExecutor)
	for name, tool := range registry {
		executorMap[name] = tool
	}

	billingCfg := billing.LoadConfig(scope.Getenv)
	dispatcher := core.NewDefaultDispatcher()

	engine := &llm.Engine{Scope: scope,
		MaxIterations:  5,
		ToolRegistry:   executorMap,
		Provider:       foregroundProvider,
		VisionProvider: visionProvider,
		ModelRouter:    routerManager,
		ModelName:      "model-router",
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
		SharedDialogue: shared.Prompt,
	}
	scope.Log().Info(
		logs.SYSTEM,
		fmt.Sprintf(
			"✓ 群聊 running compact 已启用 (buffer: %d, min interval: %s)",
			groupCompactBufferSize,
			groupCompactMinInterval,
		),
	)

	scope.Log().Info(logs.SYSTEM, "✓ 智能体引擎初始化完成")
	// Register memory tool (must be after engine assignment)
	memTool := tools.NewMemoryTool(engine)
	engine.ToolRegistry[memTool.Name()] = memTool
	mux := http.NewServeMux()
	// ConnectRPC 服务注册
	botPath, botHandler := pbconnect.NewBotStatusServiceHandler(botstatus.New(engine, version))
	mux.Handle(botPath, botHandler)

	settingsPath, settingsHandler := pbconnect.NewSettingsServiceHandler(settings.NewScoped(config, global))
	mux.Handle(settingsPath, settingsHandler)

	routerPath, routerHandler := pbconnect.NewModelRouterServiceHandler(routersvc.New(engine.ModelRouter))
	mux.Handle(routerPath, routerHandler)

	logsPath, logsHandler := pbconnect.NewLogServiceHandler(logsvc.New(logger))
	mux.Handle(logsPath, logsHandler)
	mux.HandleFunc(logs.LogImagePathPrefix, logger.ImageHandler)

	memoryPath, memoryHandler := pbconnect.NewMemoryServiceHandler(
		memsvc.New(store, engine.MemoryReflections),
	)
	mux.Handle(memoryPath, memoryHandler)

	dialogueServicePath, dialogueHandler := pbconnect.NewDialogueServiceHandler(
		shared,
	)
	mux.Handle(dialogueServicePath, dialogueHandler)

	if stickerStore != nil {
		stickerSvc := stickersvc.New(stickerStore, summarizer)
		stickerPath, stickerHandler := pbconnect.NewStickerServiceHandler(stickerSvc)
		mux.Handle(stickerPath, stickerHandler)
		mux.HandleFunc("/api/sticker/", stickerSvc.ImageHandler())
	}

	ob := onebot.NewAdapter(engine)
	ab := astrbot.NewAdapter(engine)
	ob.SetStealer(stealer)
	ab.SetStealer(stealer)
	dispatcher.RegisterAdapter(ob)
	dispatcher.RegisterAdapter(ab)
	if scope.Getenv("ENABLE_ONEBOT_ADAPTER") != "false" {
		mux.HandleFunc("/ws/onebot", ob.Handler())
	}
	if scope.Getenv("ENABLE_ASTRBOT_ADAPTER") != "false" {
		mux.HandleFunc("/ws/astrbot", ab.Handler())
	}
	if !enabled {
		engine.StartedAt = time.Time{}
		logger.Clear()
	}
	success = true
	return &Runtime{scope, engine, mux, ob, ab}, nil
}
func (r *Runtime) Stop() {
	r.Scope.Cancel()
	r.Onebot.CloseConnections()
	r.Astrbot.CloseConnections()
	r.Engine.GroupCompactor.StopTimers()
	r.Scope.Wait()
}
func durationFromEnv(scope *runtimescope.Scope, name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(scope.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		scope.Log().Warn(
			logs.SYSTEM,
			fmt.Sprintf("%s=%q 不是有效的正数时长，使用默认值 %s", name, raw, fallback),
		)
		return fallback
	}
	return value
}

func positiveIntFromEnv(scope *runtimescope.Scope, name string, fallback int) int {
	raw := strings.TrimSpace(scope.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		scope.Log().Warn(logs.SYSTEM, fmt.Sprintf("%s=%q 不是有效的正整数，使用默认值 %d", name, raw, fallback))
		return fallback
	}
	return value
}
