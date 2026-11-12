package instance

import (
	"context"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/actionscat"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/astrbot"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/wsclient"
	"github.com/GuaiZai233/FrostAgent/internal/balancequery"
	"github.com/GuaiZai233/FrostAgent/internal/billing"
	"github.com/GuaiZai233/FrostAgent/internal/buildinfo"
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
	mcpsvc "github.com/GuaiZai233/FrostAgent/internal/service/mcp"
	memsvc "github.com/GuaiZai233/FrostAgent/internal/service/memory"
	"github.com/GuaiZai233/FrostAgent/internal/service/messages"
	routersvc "github.com/GuaiZai233/FrostAgent/internal/service/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/service/settings"
	skillservice "github.com/GuaiZai233/FrostAgent/internal/service/skills"
	stickersvc "github.com/GuaiZai233/FrostAgent/internal/service/sticker"
	"github.com/GuaiZai233/FrostAgent/internal/skills"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/GuaiZai233/FrostAgent/internal/tokenusage"
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
	Astrbot        *astrbot.Adapter
	backgroundOnce sync.Once
	onebotClient   bool
	astrbotClient  bool
}

func buildRuntime(dir, configDir, prefix, wsListenAddr string, config, global *instanceconfig.Store, logger *logs.Store, templateDialogue string, billingClient *billing.Client, mcpManager *mcp.Manager, mcpGetenv func(string) string, sandboxManager *sandbox.ConfigManager, instanceID string, enabled bool, securityController *security.Controller, db *storage.DB) (*Runtime, error) {
	if config.AccessError() != nil {
		return nil, config.AccessError()
	}
	if enabled && config.Error() != nil {
		return nil, config.Error()
	}
	onebotConnection, onebotError := wsclient.Load(config.Get, "ONEBOT")
	astrbotConnection, astrbotError := wsclient.Load(config.Get, "ASTRBOT")
	if enabled {
		if onebotError != nil {
			return nil, fmt.Errorf("OneBot connection: %w", onebotError)
		}
		if astrbotError != nil {
			return nil, fmt.Errorf("AstrBot connection: %w", astrbotError)
		}
	}
	if err := safeTree(dir); err != nil {
		return nil, err
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
	var routerManager *modelrouter.Manager
	if db != nil {
		routerManager = modelrouter.NewSQL(db, instanceID, scope)
	} else {
		routerManager = modelrouter.New(filepath.Join(configDir, "model_router.json"), scope)
	}
	if err := routerManager.LoadError(); err != nil {
		if enabled {
			return nil, err
		}
		scope.Log().Error(logs.SYSTEM, fmt.Sprintf("模型路由配置不可用，已进入未配置状态: %v", err))
	}
	tokenUsage, usageErr := tokenusage.New(db, instanceID, filepath.Join(dir, "token_usage.json"))
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
	var store *memory.Store
	var groupManager *memory.GroupManager
	if db != nil {
		store = memory.NewSQLStore(db, instanceID)
		groupManager = memory.NewSQLGroupManager(db, instanceID, scope)
	} else {
		store = memory.NewStore(filepath.Join(dir, "brain.json"))
		groupManager = memory.NewGroupManager(dir, scope)
		if err := memory.MigrateLegacyGroupMemories(dir, store, groupManager, scope); err != nil {
			return nil, fmt.Errorf("migrate legacy group memories: %w", err)
		}
	}
	reader := memory.NewReader(store, 20)
	writer := memory.NewWriter(store)
	writer.Scope = scope
	writer.SetGroupManager(groupManager)
	writer.SetLLM(memoryProvider, "model-router-memory-extract")
	var groupSummaryStore *groupsummary.Store
	var err error
	if db != nil {
		groupSummaryStore, err = groupsummary.NewSQLStore(db, instanceID)
	} else {
		groupSummaryStore, err = groupsummary.NewStore(filepath.Join(dir, "group_summaries.json"))
	}
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
	// Gateway: owner + visibility filtering
	gateway := memory.NewGateway()
	// Reflection: background, owner-isolated topic catalog generation
	var catalog *memory.CatalogStore
	if db != nil {
		catalog = memory.NewSQLCatalogStore(db, instanceID, memory.ScopePrivate, "", "")
	} else {
		catalog = memory.NewCatalogStore(filepath.Join(dir, "memory_catalog.json"))
	}
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

	var skillStore *skills.Store
	if db != nil {
		skillStore = skills.NewStore(db, instanceID)
	}

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
	staySilentTool := tools.StaySilentTool()
	registry[staySilentTool.Name()] = staySilentTool
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
	var stickerStore *sticker.Store
	var err2 error
	if db != nil {
		stickerStore, err2 = sticker.NewSQLStore(db, instanceID, filepath.Join(dir, "sticker"))
	} else {
		stickerStore, err2 = sticker.NewStore(filepath.Join(dir, "sticker"))
	}
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

	dialoguePath := filepath.Join(configDir, "dialogue.yml")
	if db == nil {
		if _, err := os.Stat(dialoguePath); os.IsNotExist(err) {
			var dialogueContent []byte
			if templateDialogue != "" {
				dialogueContent, _ = os.ReadFile(templateDialogue)
			}
			if len(dialogueContent) == 0 {
				dialogueContent = []byte("[]\n")
			}
			_ = instanceconfig.WriteAtomic(dialoguePath, dialogueContent, 0600)
		}
	}
	var dialoguePrompt string
	if db != nil {
		dialoguePrompt, err = dialogue.LoadPromptSQL(db, instanceID)
	} else {
		dialoguePrompt, err = llm.LoadDialoguePrompt(dialoguePath)
	}
	if err != nil && !os.IsNotExist(err) {
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
	memTool := tools.NewMemoryTool(engine)
	engine.ToolRegistry[memTool.Name()] = memTool
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
	backend := "sqlite"
	if db != nil {
		backend = string(db.Backend)
	}
	settingsService.SetEmbeddingStorage(backend, func(ctx context.Context) error {
		if _, err := routerManager.Snapshot().Resolve(modelrouter.WorkloadEmbedding, modelrouter.Scope{}); err != nil {
			return fmt.Errorf("请先配置可用的 Embedding 模型路由: %w", err)
		}
		if db != nil {
			_, err := db.EnsureEmbeddingStorage(ctx)
			return err
		}
		return nil
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

	var dialogueSvc *dialogue.Service
	if db != nil {
		dialogueSvc = dialogue.NewSQL(db, instanceID, engine, dialogue.WithLogger(logger))
	} else {
		dialogueSvc = dialogue.New(dialoguePath, engine, dialogue.WithLogger(logger))
	}
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
	ab := astrbot.NewAdapter(engine)
	ob.ConfigureConnection(onebotConnection)
	ab.ConfigureConnection(astrbotConnection)
	ob.SetIncomingDispatcher(actionsCatClient)
	ab.SetIncomingDispatcher(actionsCatClient)
	ob.SetStealer(stealer)
	ab.SetStealer(stealer)
	dispatcher.RegisterAdapter(ob)
	dispatcher.RegisterAdapter(ab)
	engine.Awareness = &llm.AwarenessService{Engine: engine, Resolve: func(target llm.AwarenessTarget) (llm.AwarenessSender, bool) {
		switch target.AdapterID {
		case "onebot":
			return ob.SendAwareness, ob.AwarenessReady()
		case "astrbot":
			return ab.SendAwareness, ab.AwarenessReady()
		}
		if !memory.IsQQPlatform(target.Platform) {
			return ab.SendAwareness, ab.AwarenessReady()
		}
		// Until an inbound route is known, two usable adapters are ambiguous.
		onebotReady, astrbotReady := ob.AwarenessReady(), ab.AwarenessReady()
		if onebotReady == astrbotReady {
			return nil, false
		}
		if onebotReady {
			return ob.SendAwareness, true
		}
		return ab.SendAwareness, true
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
			if ab.ConnectionCount() > 0 {
				platforms = append(platforms, "astrbot")
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
	// Keep both endpoints for reverse connections and browser mock sessions.
	// Each adapter rejects real inbound connections in forward mode.
	mux.HandleFunc("/ws/onebot", ob.Handler())
	mux.HandleFunc("/ws/astrbot", ab.Handler())
	if !enabled {
		engine.StartedAt = time.Time{}
		logger.Clear()
	}
	success = true
	return &Runtime{
		Scope: scope, Engine: engine, Handler: mux, Onebot: ob, Astrbot: ab,
		onebotClient:  !onebotConnection.Disabled && onebotConnection.Mode == wsclient.Forward,
		astrbotClient: !astrbotConnection.Disabled && astrbotConnection.Mode == wsclient.Forward,
	}, nil
}

// StartBackground starts activation-owned workers only after the runtime is
// published. Building disabled or rollback candidates never initiates a dial.
func (r *Runtime) StartBackground() {
	if r == nil || r.Scope == nil || r.Scope.Context().Err() != nil {
		return
	}
	r.backgroundOnce.Do(func() {
		r.StartEmbeddings()
		if r.Engine.Awareness != nil {
			r.Scope.Go(func() { r.Engine.Awareness.Run(r.Scope.Context()) })
		}
		if r.onebotClient {
			r.Scope.Go(func() { r.Onebot.RunClient(r.Scope.Context()) })
		}
		if r.astrbotClient {
			r.Scope.Go(func() { r.Astrbot.RunClient(r.Scope.Context()) })
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
	r.Onebot.CloseConnections()
	r.Astrbot.CloseConnections()
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
