package instance

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/actionscat"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/wsclient"
	"github.com/GuaiZai233/FrostAgent/internal/balancequery"
	"github.com/GuaiZai233/FrostAgent/internal/billing"
	"github.com/GuaiZai233/FrostAgent/internal/buildinfo"
	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/groupsummary"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/mcp"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/sandbox"
	"github.com/GuaiZai233/FrostAgent/internal/sandbox/codeinterpreter"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	actionscatsvc "github.com/GuaiZai233/FrostAgent/internal/service/actionscat"
	"github.com/GuaiZai233/FrostAgent/internal/service/botstatus"
	"github.com/GuaiZai233/FrostAgent/internal/service/dialogue"
	logsvc "github.com/GuaiZai233/FrostAgent/internal/service/logs"
	"github.com/GuaiZai233/FrostAgent/internal/storageusage"
	mcpsvc "github.com/GuaiZai233/FrostAgent/internal/service/mcp"
	memsvc "github.com/GuaiZai233/FrostAgent/internal/service/memory"
	"github.com/GuaiZai233/FrostAgent/internal/service/messages"
	routersvc "github.com/GuaiZai233/FrostAgent/internal/service/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/service/settings"
	skillservice "github.com/GuaiZai233/FrostAgent/internal/service/skills"
	stickersvc "github.com/GuaiZai233/FrostAgent/internal/service/sticker"
	"github.com/GuaiZai233/FrostAgent/internal/sessionstate"
	"github.com/GuaiZai233/FrostAgent/internal/skills"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/GuaiZai233/FrostAgent/internal/tokenusage"
	"github.com/GuaiZai233/FrostAgent/internal/toolhistory"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	pbconnect "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
)

type Runtime struct {
	Scope          *runtimescope.Scope
	Engine         *llm.Engine
	Handler        http.Handler
	Onebot         *onebot.Adapter
	backgroundOnce sync.Once
	onebotClient   bool
}

func buildRuntime(dir, prefix, wsListenAddr string, config, global *instanceconfig.Store, logger *logs.Store, billingClient *billing.Client, mcpManager *mcp.Manager, mcpGetenv func(string) string, sandboxManager *sandbox.ConfigManager, instanceID string, enabled bool, securityController *security.Controller, db *storage.DB, settingsWriteMu tools.ConfigWriteLock) (*Runtime, error) {
	if err := safeTree(dir); err != nil {
		return nil, err
	}
	if err := logger.EnablePersistence(filepath.Join(dir, "logs"), logs.PersistentBudget(config.Get(logs.LogBudgetKey))); err != nil {
		logger.Warn(logs.SYSTEM, "部分历史日志无法恢复，请检查实例持久化状态")
	}
	if config.AccessError() != nil {
		return nil, config.AccessError()
	}
	if enabled && config.Error() != nil {
		return nil, config.Error()
	}
	onebotConnection, onebotError := wsclient.Load(config.Get, "ONEBOT")
	if enabled {
		if onebotError != nil {
			return nil, fmt.Errorf("OneBot connection: %w", onebotError)
		}
	}
	scope := runtimescope.New(config, global, logger)
	scope.SetInstanceID(instanceID)
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
	routerManager := modelrouter.NewSQL(db, instanceID, scope)
	routerManager.EnableRuntimeFallback()
	if err := routerManager.LoadError(); err != nil {
		if enabled {
			return nil, err
		}
		scope.Log().Error(logs.SYSTEM, fmt.Sprintf("模型路由配置不可用，已进入未配置状态: %v", err))
	}
	tokenUsage, usageErr := tokenusage.New(db, instanceID, "")
	if usageErr != nil {
		scope.Log().Error(logs.SYSTEM, fmt.Sprintf("Token 用量无法读取，统计暂不可用: %v", usageErr))
	}
	routerManager.SetUsageStore(tokenUsage)
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
	securityTimeout := resolveSecurityGatewayTimeout(scope, security.DefaultClassifierTimeout)
	securityProvider := routerManager.Provider(modelrouter.WorkloadDialogue, false, securityTimeout)
	if securityController != nil {
		if enabled {
			securityController.SetInstanceProviderWithTimeout(instanceID, securityProvider, "model-router-security-gateway", securityTimeout)
		} else {
			securityController.RemoveInstanceProvider(instanceID)
		}
	}

	// Initialize memory system
	store := memory.NewSQLStore(db, instanceID)
	groupManager := memory.NewSQLGroupManager(db, instanceID, scope)
	reader := memory.NewReader(store, 20)
	writer := memory.NewWriter(store)
	writer.Scope = scope
	writer.SetGroupManager(groupManager)
	writer.SetLLM(memoryProvider, "model-router-memory-extract")
	groupSummaryStore, err := groupsummary.NewSQLStore(db, instanceID)
	if err != nil {
		return nil, err
	}
	sessionManager := llm.NewSessionManager(scope)
	sessionManager.SetGroupSummaryStore(groupSummaryStore)
	checkpoints := sessionstate.New(db, instanceID, filepath.Join(dir, "session-checkpoints"),
		func(message string) { scope.Log().Warn(logs.SYSTEM, message) })
	if err := sessionManager.EnablePersistence(checkpoints); err != nil {
		scope.Log().Warn(logs.SYSTEM, "会话快照恢复不完整，已恢复可读取的记录；请检查存储状态")
	}
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
	groupCompactor.SetGroupManager(groupManager)
	groupCompactor.SetMemoryWriter(writer)
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
	// Gateway: conversation scope filtering
	gateway := memory.NewGateway()
	// Reflection: background, owner-isolated topic catalog generation
	catalog := memory.NewSQLCatalogStore(db, instanceID, memory.ScopePrivate, "", "")
	reflector := memory.NewReflector(
		store,
		catalog,
		reflectionProvider,
		"model-router-reflection",
		memoryConfig,
	)
	reflectionSummary := fmt.Sprintf("✓ 记忆反思独立超时: %s", memoryConfig.ReflectTimeout)
	scope.Log().InfoWithConsoleSummary(
		logs.SYSTEM,
		reflectionSummary,
		reflectionSummary,
	)
	reflector.Scope = scope
	reflections := memory.NewReflectionManager(reflector)
	reflections.Scope = scope

	skillStore := skills.NewStore(db, instanceID)

	// Register tools
	registry := make(map[string]tools.Tool)
	balanceTool := tools.BalanceQueryTool(config.Get, func(endpointID string) (balancequery.ProviderCredential, error) {
		credential, err := routerManager.EndpointCredential(endpointID)
		return balancequery.ProviderCredential{APIKey: credential.APIKey, BaseURL: credential.BaseURL}, err
	})
	registry[balanceTool.Name()] = balanceTool
	sendMsgTool := tools.SendMsgTool()
	registry[sendMsgTool.Name()] = sendMsgTool
	qqTool := tools.QQTool()
	registry[qqTool.Name()] = qqTool
	richImageTool := tools.SendRichImageTool(config.Get)
	registry[richImageTool.Name()] = richImageTool
	staySilentTool := tools.StaySilentTool()
	registry[staySilentTool.Name()] = staySilentTool
	timeSleepTool := tools.TimeSleepTool(config.Get)
	registry[timeSleepTool.Name()] = timeSleepTool
	if securityController != nil {
		banUserTool := tools.NewBanUserTool(securityController)
		registry[banUserTool.Name()] = banUserTool
	}

	subAgentTool := tools.SubAgentTool(subagentProvider)
	registry[subAgentTool.Name()] = subAgentTool
	var dynamicSandbox *sandbox.DynamicBackend
	if sandboxManager != nil {
		dynamicSandbox = sandbox.NewDynamicBackend(func() sandbox.Config {
			cfg := sandboxManager.Get()
			cfg.SessionNamespace = strings.TrimRight(cfg.SessionNamespace, "/") + "/" + instanceID
			return cfg
		}, func(cfg sandbox.Config) sandbox.Backend {
			return codeinterpreter.New(cfg, codeinterpreter.WithLogger(logger))
		})
		commandTool := tools.ExecuteCommandTool(dynamicSandbox)
		registry[commandTool.Name()] = commandTool
	}

	// Initialize ActionsCat client & tools
	actionsCatClient := actionscat.New(scope.Getenv)
	actionsCatTool := tools.ActionsCatTool(actionsCatClient, scope)
	registry[actionsCatTool.Name()] = actionsCatTool

	// Initialize sticker subsystem
	stickerVision := &sticker.LLMVisionCaller{Scope: scope,
		Provider:  visionProvider,
		ModelName: "model-router-vision",
	}
	var summarizer *sticker.Summarizer
	var stealer *sticker.Stealer
	stickerStore, err2 := sticker.NewSQLStore(db, instanceID, filepath.Join(dir, "sticker"))
	if err2 != nil {
		return nil, err2
	} else {
		summarizer = sticker.NewSummarizer(stickerStore, stickerVision, scope)
		stealer = sticker.NewStealer(stickerStore, summarizer)
		stickerTool := tools.SendStickerTool(stickerStore, prefix)
		registry[stickerTool.Name()] = stickerTool
		stealStickerTool := tools.StealStickerTool(stealer)
		registry[stealStickerTool.Name()] = stealStickerTool
		scope.Log().InfoWithConsoleSummary(logs.SYSTEM, "✓ 表情包摘取子系统已初始化", "✓ 表情包摘取子系统已初始化")
		if enabled {
			summarizer.EnqueueUnsummarized()
		}
	}

	var skillBackend sandbox.Backend
	if dynamicSandbox != nil {
		skillBackend = dynamicSandbox
	}
	for _, tool := range tools.SkillTools(skillStore, skillBackend, sessionManager) {
		registry[tool.Name()] = tool
	}

	executorMap := make(map[string]llm.ToolExecutor)
	for name, tool := range registry {
		executorMap[name] = tool
	}

	billingCfg := billing.LoadConfig(scope.Getenv)
	dispatcher := core.NewDefaultDispatcher()

	dialoguePrompt, err := dialogue.LoadPromptSQL(db, instanceID)
	if err != nil {
		scope.Log().Warn(logs.SYSTEM, fmt.Sprintf("加载人设预设对话失败: %v", err))
	}

	maxIterations := positiveIntFromEnv(scope, "AGENT_MAX_ITERATIONS", llm.DefaultMaxIterations)
	version := buildinfo.DisplayVersion()
	engine := &llm.Engine{Scope: scope,
		MaxIterations:  maxIterations,
		ToolRegistry:   executorMap,
		Provider:       foregroundProvider,
		VisionProvider: visionProvider,
		TokenUsage:     tokenUsage,
		StorageUsage:   storageusage.New(db, instanceID, filepath.Join(dir, "sticker"), filepath.Join(dir, "logs"), filepath.Join(dir, "session-checkpoints")),
		ModelRouter:    routerManager,
		ModelName:      "model-router",
		SessionManager: sessionManager,
		Dispatcher:     dispatcher,
		StartedAt:      time.Now(),
		Version:        version,
		MCPManager:     mcpManager,
		Skills:         skillStore,
		SandboxBackend: dynamicSandbox,
		Security:       securityController,
		InstanceID:     instanceID,
		// Billing components
		BillingClient: billingClient,
		BillingConfig: billingCfg,
		// Memory components
		MemoryReader:      reader,
		MemoryWriter:      writer,
		MemoryGateway:     gateway,
		MemoryCatalog:     catalog,
		MemoryReflections: reflections,
		GroupManager:      groupManager,
		GroupCompactor:    groupCompactor,
		GroupSummaryStore: groupSummaryStore,
		// Persona dialogue prompt
		DialoguePrompt: dialoguePrompt,
	}
	if strings.EqualFold(scope.Getenv("ENABLE_EMBEDDING"), "true") {
		embeddings, err := memory.NewEmbeddings(scope, store, groupManager, routerManager, reflector)
		if err != nil {
			return nil, err
		}
		engine.MemoryEmbeddings = embeddings
		embeddings.BindWriter(writer)

	}
	compactSummary := fmt.Sprintf(
		"✓ 群聊 running compact 已启用 (buffer: %d, min interval: %s)",
		groupCompactBufferSize,
		groupCompactMinInterval,
	)
	scope.Log().InfoWithConsoleSummary(logs.SYSTEM, compactSummary, compactSummary)

	scope.Log().InfoWithConsoleSummary(logs.SYSTEM, "✓ 智能体引擎初始化完成", "✓ 智能体引擎初始化完成")
	// Register memory tool (must be after engine assignment)
	engine.ToolHistory = toolhistory.NewStore(db, instanceID)
	engine.ChatHistory = chathistory.NewRecorder(chathistory.NewStore(db, instanceID), scope.Getenv,
		func(message string) { scope.Log().Warn(logs.SYSTEM, message) })
	historyTool := tools.NewChatHistoryTool(engine)
	engine.ToolRegistry[historyTool.Name()] = historyTool
	toolHistoryTool := tools.NewToolHistoryTool(engine)
	engine.ToolRegistry[toolHistoryTool.Name()] = toolHistoryTool
	memTool := tools.NewMemoryTool(engine)
	engine.ToolRegistry[memTool.Name()] = memTool
	overrideConfigTool := tools.OverrideFrostAgentConfigTool(engine, settingsWriteMu)
	engine.ToolRegistry[overrideConfigTool.Name()] = overrideConfigTool
	// Register the context-image inspection tool after the engine so it can use
	// the routed vision/dialogue providers and the shared security controller.
	inspectImageTool := tools.InspectMessageImageTool(engine)
	engine.ToolRegistry[inspectImageTool.Name()] = inspectImageTool
	mux := http.NewServeMux()
	if skillStore != nil {
		mux.Handle("/api/skills", skillservice.New(skillStore, mcpGetenv))
		mux.Handle("/api/skills/", skillservice.New(skillStore, mcpGetenv))
	}
	// ConnectRPC 服务注册
	botPath, botHandler := pbconnect.NewBotStatusServiceHandler(botstatus.New(engine, version, wsListenAddr))
	mux.Handle(botPath, botHandler)

	settingsService := settings.NewScoped(config, global, sandboxManager)
	backend := string(db.Backend)
	settingsService.SetEmbeddingStorage(backend, func(ctx context.Context) error {
		if _, err := routerManager.Snapshot().Resolve(modelrouter.WorkloadEmbedding, modelrouter.Scope{}); err != nil {
			return fmt.Errorf("请先配置可用的 Embedding 模型路由: %w", err)
		}
		_, err := db.EnsureEmbeddingStorage(ctx)
		return err
	})
	mux.HandleFunc("/api/persistence", func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		var checkpointStatus sessionstate.Status
		if sessionManager.Checkpoints != nil {
			checkpointStatus = sessionManager.Checkpoints.Status()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"logs": logger.PersistenceStatus(), "sessions": checkpointStatus})
	})
	settingsPath, settingsHandler := pbconnect.NewSettingsServiceHandler(settingsService)
	mux.Handle(settingsPath, settingsHandler)

	routerPath, routerHandler := pbconnect.NewModelRouterServiceHandler(routersvc.New(engine.ModelRouter))
	mux.Handle(routerPath, routerHandler)

	logsPath, logsHandler := pbconnect.NewLogServiceHandler(logsvc.New(logger))
	mux.Handle(logsPath, logsHandler)
	mux.HandleFunc(logs.LogImagePathPrefix, logger.ImageHandler)

	mcpPath, mcpHandler := pbconnect.NewMCPServiceHandler(mcpsvc.NewScoped(mcpManager, mcpGetenv))
	mux.Handle(mcpPath, mcpHandler)

	memoryService := memsvc.New(store, groupManager, engine.MemoryReflections)
	memoryService.SetEmbeddings(engine.MemoryEmbeddings)
	memoryPath, memoryHandler := pbconnect.NewMemoryServiceHandler(memoryService)
	mux.Handle(memoryPath, memoryHandler)

	dialogueSvc := dialogue.NewSQL(db, instanceID, engine, dialogue.WithLogger(logger))
	dialogueServicePath, dialogueHandler := pbconnect.NewDialogueServiceHandler(
		dialogueSvc,
	)
	mux.Handle(dialogueServicePath, dialogueHandler)

	if stickerStore != nil {
		stickerSvc := stickersvc.New(stickerStore, summarizer)
		stickerPath, stickerHandler := pbconnect.NewStickerServiceHandler(stickerSvc)
		mux.Handle(stickerPath, stickerHandler)
		mux.HandleFunc("/api/sticker/", stickerSvc.ImageHandler())
	}

	msgSvc := messages.New(dispatcher, instanceID, scope.Getenv)
	mux.Handle("/api/v1/messages/send", msgSvc)

	actionsCatSvc := actionscatsvc.NewScoped(actionsCatClient, instanceID, mcpGetenv)
	mux.Handle("/api/actionscat/", actionsCatSvc)
	mux.Handle("/api/actionscat", actionsCatSvc)
	mux.Handle("/api/v1/actionscat/", actionsCatSvc)
	mux.Handle("/api/v1/actionscat", actionsCatSvc)

	ob := onebot.NewAdapter(engine)
	ob.ConfigureConnection(onebotConnection)
	ob.SetIncomingDispatcher(actionsCatClient)
	ob.SetStealer(stealer)
	dispatcher.RegisterAdapter(ob)
	engine.Awareness = &llm.AwarenessService{Engine: engine, Resolve: func(target llm.AwarenessTarget) (llm.AwarenessSender, bool) {
		if (target.AdapterID != "" && target.AdapterID != "onebot") || !memory.IsQQPlatform(target.Platform) {
			return nil, false
		}
		return ob.SendAwareness, ob.AwarenessReady()
	}}
	// Read-only self-description. Registered after the adapters so it can report
	// live platform connections; every value is read at call time.
	frostInfoTool := tools.FrostAgentInfoTool(tools.FrostAgentInfoSource{
		BotName: func() string { return engine.Getenv("BOT_NAME") },
		Aliases: func() []string { return strings.Split(engine.Getenv("BOT_ALIASES"), ",") },
		Version: func() string { return engine.Version },
		StartedAt: func() time.Time {
			return engine.StartedAt
		},
		Platforms: func() []string {
			var platforms []string
			if ob.ConnectionCount() > 0 {
				platforms = append(platforms, "onebot")
			}
			return platforms
		},
		DialogueModel: func(ctx context.Context) (string, error) {
			return llm.DialogueModelForRun(ctx, engine.ModelRouter)
		},
		Features: func() map[string]bool {
			sandboxEnabled := dynamicSandbox != nil && sandboxManager != nil && sandboxManager.Get().Enabled
			mcpConnected := false
			if mcpManager != nil {
				for _, server := range mcpManager.ListServers() {
					if server.Status() == mcp.StatusConnected {
						mcpConnected = true
						break
					}
				}
			}
			return map[string]bool{
				tools.FeatureMemory:          engine.MemoryReader != nil,
				tools.FeatureVectorRetrieval: engine.MemoryEmbeddings != nil,
				tools.FeatureSandbox:         sandboxEnabled,
				tools.FeatureMCP:             mcpConnected,
				tools.FeatureActionsCat:      actionsCatClient.IsEnabled() && actionsCatClient.IsConfigured(),
				tools.FeatureSticker:         stickerStore != nil,
			}
		},
	})
	engine.ToolRegistry[frostInfoTool.Name()] = frostInfoTool
	// Keep OneBot available for reverse connections and browser mock sessions.
	// The adapter rejects real inbound connections in forward mode.
	mux.HandleFunc("/ws/onebot", ob.Handler())
	if !enabled {
		engine.StartedAt = time.Time{}
	}
	success = true
	return &Runtime{
		Scope: scope, Engine: engine, Handler: mux, Onebot: ob,
		onebotClient: !onebotConnection.Disabled && onebotConnection.Mode == wsclient.Forward,
	}, nil
}

// StartBackground starts activation-owned workers only after the runtime is
// published. Building disabled or rollback candidates never initiates a dial.
func (r *Runtime) StartBackground() {
	if r == nil || r.Scope == nil || r.Scope.Context().Err() != nil {
		return
	}
	r.backgroundOnce.Do(func() {
		if r.Engine.SessionManager.Checkpoints != nil {
			r.Scope.Go(func() { r.Engine.SessionManager.Checkpoints.Run(r.Scope.Context()) })
		}
		if r.Engine.ChatHistory != nil {
			r.Scope.Go(func() { r.Engine.ChatHistory.Run(r.Scope.Context()) })
		}
		r.StartEmbeddings()
		if r.Engine.Awareness != nil {
			r.Scope.Go(func() { r.Engine.Awareness.Run(r.Scope.Context()) })
		}
		if r.onebotClient {
			r.Scope.Go(func() { r.Onebot.RunClient(r.Scope.Context()) })
		}
	})
}
func (r *Runtime) StartEmbeddings() {
	if r != nil && r.Engine != nil && r.Engine.MemoryEmbeddings != nil && r.Scope.Context().Err() == nil {
		r.Engine.MemoryEmbeddings.Start()
	}
}

func (r *Runtime) Stop() {
	r.Scope.Cancel()
	if r.Engine != nil {
		r.Engine.StorageUsage.Close()
	}
	r.Onebot.CloseConnections()
	r.Engine.GroupCompactor.StopTimers()
	r.Scope.Wait()
}

func resolveSecurityGatewayTimeout(scope *runtimescope.Scope, fallback time.Duration) time.Duration {
	if fallback <= 0 {
		fallback = security.DefaultClassifierTimeout
	}
	keys := []string{"SECURITY_GATEWAY_TIMEOUT", "SECURITY_CLASSIFIER_TIMEOUT"}
	for _, key := range keys {
		raw := ""
		if scope != nil {
			raw = strings.TrimSpace(scope.Getenv(key))
		} else {
			raw = strings.TrimSpace(os.Getenv(key))
		}
		if raw != "" {
			val, err := time.ParseDuration(raw)
			if err != nil || val <= 0 {
				if scope != nil {
					scope.Log().Warn(
						logs.SYSTEM,
						fmt.Sprintf("%s=%q 不是有效的正数时长，使用默认值 %s", key, raw, fallback),
					)
				}
				return fallback
			}
			return val
		}
	}
	return fallback
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
