package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"FrostAgent/internal/actionscat"
	"FrostAgent/internal/admincmd"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/runtimescope"
)

const (
	defaultRunWaitDuration = 25 * time.Second
)

// AgentActionDTO represents the action metadata returned to LLM agent tools.
type AgentActionDTO struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Enabled         bool   `json:"enabled"`
	Runnable        bool   `json:"runnable"`
	ActiveVersionID string `json:"active_version_id,omitempty"`
	ActiveBuildID   string `json:"active_build_id,omitempty"`
	MaxConcurrency  int    `json:"max_concurrency"`
}

func toAgentActionDTO(a *actionscat.Action) AgentActionDTO {
	if a == nil {
		return AgentActionDTO{}
	}
	return AgentActionDTO{
		ID:              a.ID,
		Name:            a.Name,
		Description:     a.Description,
		Enabled:         a.Enabled,
		Runnable:        a.IsRunnable(),
		ActiveVersionID: a.ActiveVersionID,
		ActiveBuildID:   a.ActiveBuildID,
		MaxConcurrency:  a.MaxConcurrency,
	}
}

// AgentRunDTO represents the redacted execution run payload returned to LLM agent tools.
// Internal execution state, secrets, and planned environment variables (e.g. planned_env)
// are intentionally omitted to prevent leaking sensitive credentials into the model context.
type AgentRunDTO struct {
	ID           string `json:"id"`
	ActionID     string `json:"action_id"`
	Status       string `json:"status"`
	TriggerType  string `json:"trigger_type,omitempty"`
	ExitCode     *int   `json:"exit_code,omitempty"`
	DurationMs   int64  `json:"duration_ms"`
	Stdout       string `json:"stdout,omitempty"`
	Stderr       string `json:"stderr,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	CompletedAt  string `json:"completed_at,omitempty"`
}

func toAgentRunDTO(run *actionscat.Run, includeLogs bool) AgentRunDTO {
	if run == nil {
		return AgentRunDTO{}
	}
	dto := AgentRunDTO{
		ID:           run.ID,
		ActionID:     run.ActionID,
		Status:       run.Status,
		TriggerType:  run.TriggerType,
		ExitCode:     run.ExitCode,
		DurationMs:   run.DurationMs,
		ErrorMessage: run.ErrorMessage,
	}
	if !run.CreatedAt.IsZero() {
		dto.CreatedAt = run.CreatedAt.UTC().Format(time.RFC3339)
	}
	if run.CompletedAt != nil && !run.CompletedAt.IsZero() {
		dto.CompletedAt = run.CompletedAt.UTC().Format(time.RFC3339)
	}
	if includeLogs {
		dto.Stdout = run.Stdout
		dto.Stderr = run.Stderr
	}
	return dto
}

// ActionsCatListActionsTool creates a Tool that lists registered ActionsCat actions.
func ActionsCatListActionsTool(client *actionscat.Client) Tool {
	return Tool{
		name: "actionscat_list_actions",
		description: "列出 ActionsCat 自动化平台中已注册的 Actions。返回各 Action 的 ID、名称、描述、" +
			"启用状态及是否可运行（runnable）。注意：只有 runnable=true（已关联并激活构建 active_build_id）" +
			"的 Action 才能被 actionscat_run_action 成功运行；未构建/未激活版本的 Action 仅为元数据，执行会失败。",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"enabled_only": map[string]any{
					"type":        "boolean",
					"description": "是否仅返回已启用的 Action（默认 false，返回全部）",
				},
				"runnable_only": map[string]any{
					"type":        "boolean",
					"description": "是否仅返回已激活构建、可直接执行的 Action（要求 enabled=true 且具备 active_build_id，默认 false）",
				},
			},
		},
		executeContext: func(ctx context.Context, args string) (string, error) {
			if client == nil || !client.IsConfigured() {
				return "ActionsCat 尚未配置。请在实例设置中配置 ACTIONSCAT_ENDPOINT 和 ACTIONSCAT_MANAGEMENT_TOKEN。", nil
			}

			var input struct {
				EnabledOnly  bool `json:"enabled_only"`
				RunnableOnly bool `json:"runnable_only"`
			}
			if strings.TrimSpace(args) != "" {
				_ = json.Unmarshal([]byte(args), &input)
			}

			actions, err := client.ListActions(ctx)
			if err != nil {
				return fmt.Sprintf("获取 ActionsCat actions 列表失败: %v", err), nil
			}

			var filtered []AgentActionDTO
			for _, a := range actions {
				if input.EnabledOnly && !a.Enabled {
					continue
				}
				if input.RunnableOnly && !a.IsRunnable() {
					continue
				}
				filtered = append(filtered, toAgentActionDTO(&a))
			}

			resp := struct {
				Total   int              `json:"total"`
				Actions []AgentActionDTO `json:"actions"`
			}{
				Total:   len(filtered),
				Actions: filtered,
			}

			data, err := json.MarshalIndent(resp, "", "  ")
			if err != nil {
				return "", fmt.Errorf("serialize actions response: %w", err)
			}
			return string(data), nil
		},
	}
}

// ActionsCatRunActionTool creates a Tool that triggers an action in ActionsCat.
func ActionsCatRunActionTool(client *actionscat.Client) Tool {
	return Tool{
		name: "actionscat_run_action",
		description: "在 ActionsCat 中触发执行指定的 Action。支持传入自定义环境变量及元数据。" +
			"默认等待执行完成并返回输出结果（最长等待 25 秒），若任务仍在运行则返回当前状态及 run_id 供后续查询。",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action_id": map[string]any{
					"type":        "string",
					"description": "要触发执行的 Action ID",
				},
				"extra_env": map[string]any{
					"type":                 "object",
					"description":          "传递给 Action 运行容器的额外环境变量（键值对）",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"trigger_metadata": map[string]any{
					"type":                 "object",
					"description":          "传递给 Action 的触发上下文元数据（如会话 ID、用户意图等）",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"wait": map[string]any{
					"type":        "boolean",
					"description": "是否等待任务执行结束并直接返回结果，默认为 true",
				},
			},
			"required": []string{"action_id"},
		},
		executeContext: func(ctx context.Context, args string) (string, error) {
			if runContext, ok := llm.RunContextFromContext(ctx); ok && runContext.Mock {
				return "模拟会话模式下禁用 ActionsCat 执行", nil
			}
			if client == nil || !client.IsConfigured() {
				return "ActionsCat 尚未配置。请在实例设置中配置 ACTIONSCAT_ENDPOINT 和 ACTIONSCAT_MANAGEMENT_TOKEN。", nil
			}

			var input struct {
				ActionID        string            `json:"action_id"`
				ExtraEnv        map[string]string `json:"extra_env"`
				TriggerMetadata map[string]string `json:"trigger_metadata"`
				Wait            *bool             `json:"wait"`
			}
			if err := json.Unmarshal([]byte(args), &input); err != nil {
				return fmt.Sprintf("参数解析错误: %v", err), nil
			}
			if strings.TrimSpace(input.ActionID) == "" {
				return "缺少必填参数 'action_id'", nil
			}

			wait := true
			if input.Wait != nil {
				wait = *input.Wait
			}

			for k := range input.ExtraEnv {
				if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(k)), "ACTIONSCAT_") {
					return fmt.Sprintf("环境变量 %q 包含受保护的前缀 'ACTIONSCAT_'，不允许自定义覆盖", k), nil
				}
			}

			req := actionscat.ManualRunReq{
				ExtraEnv:        input.ExtraEnv,
				TriggerMetadata: input.TriggerMetadata,
			}

			var run *actionscat.Run
			var err error
			if wait {
				run, err = client.TriggerRunAndWait(ctx, input.ActionID, req, defaultRunWaitDuration)
			} else {
				run, err = client.TriggerRun(ctx, input.ActionID, req)
			}

			if err != nil {
				errMsg := err.Error()
				if strings.Contains(strings.ToLower(errMsg), "no active build") || strings.Contains(strings.ToLower(errMsg), "no_active_build") {
					return fmt.Sprintf("触发 ActionsCat Action %s 失败: 该动作尚未激活构建版本 (active_build_id 为空)，无法运行。请先在 ActionsCat 中构建并激活版本后再执行。", input.ActionID), nil
				}
				return fmt.Sprintf("触发 ActionsCat Action %s 失败: %v", input.ActionID, err), nil
			}

			dto := toAgentRunDTO(run, true)
			data, err := json.MarshalIndent(dto, "", "  ")
			if err != nil {
				return "", fmt.Errorf("serialize run response: %w", err)
			}
			return string(data), nil
		},
	}
}

// ActionsCatGetRunTool creates a Tool that inspects a run's status and logs.
func ActionsCatGetRunTool(client *actionscat.Client) Tool {
	return Tool{
		name: "actionscat_get_run",
		description: "获取 ActionsCat 某次运行任务 (Run) 的状态、耗时、退出码及详细日志 (stdout/stderr)。" +
			"可用于轮询未完成的任务或分析执行失败原因。",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action_id": map[string]any{
					"type":        "string",
					"description": "所属 Action ID",
				},
				"run_id": map[string]any{
					"type":        "string",
					"description": "运行任务的 Run ID",
				},
				"include_logs": map[string]any{
					"type":        "boolean",
					"description": "是否包含日志内容（默认 true）",
				},
			},
			"required": []string{"action_id", "run_id"},
		},
		executeContext: func(ctx context.Context, args string) (string, error) {
			if client == nil || !client.IsConfigured() {
				return "ActionsCat 尚未配置。请在实例设置中配置 ACTIONSCAT_ENDPOINT 和 ACTIONSCAT_MANAGEMENT_TOKEN。", nil
			}

			var input struct {
				ActionID    string `json:"action_id"`
				RunID       string `json:"run_id"`
				IncludeLogs *bool  `json:"include_logs"`
			}
			if err := json.Unmarshal([]byte(args), &input); err != nil {
				return fmt.Sprintf("参数解析错误: %v", err), nil
			}
			if strings.TrimSpace(input.ActionID) == "" || strings.TrimSpace(input.RunID) == "" {
				return "缺少必填参数 'action_id' 或 'run_id'", nil
			}

			includeLogs := true
			if input.IncludeLogs != nil {
				includeLogs = *input.IncludeLogs
			}

			run, err := client.GetRun(ctx, input.ActionID, input.RunID)
			if err != nil {
				return fmt.Sprintf("查询 Run 状态失败: %v", err), nil
			}

			dto := toAgentRunDTO(run, includeLogs)
			data, err := json.MarshalIndent(dto, "", "  ")
			if err != nil {
				return "", fmt.Errorf("serialize run response: %w", err)
			}
			return string(data), nil
		},
	}
}

// ActionsCatCreateActionTool creates a Tool that registers a new Action in ActionsCat.
// This is a management-plane control operation and is strictly restricted to configured administrators (ADMIN_QQ_IDS).
func ActionsCatCreateActionTool(client *actionscat.Client, scopes ...*runtimescope.Scope) Tool {
	return Tool{
		name: "actionscat_create_action",
		description: "在 ActionsCat 中注册 Action 动作元数据（仅限配置了 ADMIN_QQ_IDS 的管理员使用）。" +
			"可指定动作名称、描述及最大并发数。注意：新建的 Action 为元数据壳，后续需在 ActionsCat 中构建并激活版本后方可由 actionscat_run_action 实际执行。",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Action 任务名称（必填，如 '天气预报抓取'、'每日数据备份'）",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Action 任务功能说明或描述（可选）",
				},
				"max_concurrency": map[string]any{
					"type":        "integer",
					"description": "Action 允许的最大并发运行数（可选，默认为 1）",
				},
			},
			"required": []string{"name"},
		},
		executeContext: func(ctx context.Context, args string) (string, error) {
			runContext, ok := llm.RunContextFromContext(ctx)
			if !ok {
				return "无法获取调用者会话上下文，拒绝执行管理操作", nil
			}
			if runContext.Mock {
				return "模拟会话模式下禁用 ActionsCat 创建任务", nil
			}
			if !admincmd.IsAdmin(runContext.ActorUserID, scopes...) {
				return "权限不足: actionscat_create_action 仅允许管理员 (ADMIN_QQ_IDS) 执行", nil
			}
			if client == nil || !client.IsConfigured() {
				return "ActionsCat 尚未配置。请在实例设置中配置 ACTIONSCAT_ENDPOINT 和 ACTIONSCAT_MANAGEMENT_TOKEN。", nil
			}

			var input struct {
				Name           string `json:"name"`
				Description    string `json:"description"`
				MaxConcurrency int    `json:"max_concurrency"`
			}
			if err := json.Unmarshal([]byte(args), &input); err != nil {
				return fmt.Sprintf("参数解析错误: %v", err), nil
			}
			if strings.TrimSpace(input.Name) == "" {
				return "缺少必填参数 'name'", nil
			}

			action, err := client.CreateAction(ctx, actionscat.CreateActionReq{
				Name:           input.Name,
				Description:    input.Description,
				MaxConcurrency: input.MaxConcurrency,
			})
			if err != nil {
				return fmt.Sprintf("创建 ActionsCat Action 失败: %v", err), nil
			}

			result := struct {
				actionscat.Action
				Runnable bool   `json:"runnable"`
				Notice   string `json:"notice"`
			}{
				Action:   *action,
				Runnable: action.IsRunnable(),
				Notice:   "Action 元数据注册成功。当前尚未关联激活构建 (active_build_id 为空)，需在 ActionsCat 中构建并激活版本后方可运行。",
			}

			data, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return "", fmt.Errorf("serialize created action response: %w", err)
			}
			return string(data), nil
		},
	}
}

const maxTotalFilesBytes = 10 * 1024 * 1024 // 10 MiB

type createVersionInput struct {
	ActionID            string                      `json:"action_id"`
	Files               map[string]string           `json:"files"`
	Encodings           map[string]string           `json:"encodings,omitempty"`
	BuildSpec           *actionscat.BuildSpec       `json:"build_spec,omitempty"`
	RuntimeSpec         *actionscat.RuntimeSpec     `json:"runtime_spec,omitempty"`
	StateInjections     []actionscat.StateInjection `json:"state_injections,omitempty"`
	RuntimeCapabilities []string                    `json:"runtime_capabilities,omitempty"`
}

func prepareCreateVersionReq(input createVersionInput) (actionscat.CreateVersionReq, error) {
	if len(input.Files) == 0 {
		return actionscat.CreateVersionReq{}, fmt.Errorf("files 文件映射不能为空")
	}
	var totalBytes int
	for f, content := range input.Files {
		totalBytes += len(f) + len(content)
	}
	if totalBytes > maxTotalFilesBytes {
		return actionscat.CreateVersionReq{}, fmt.Errorf("文件总大小超过 10 MiB 上限 (当前: %d 字节)", totalBytes)
	}

	req := actionscat.CreateVersionReq{
		Files:               input.Files,
		Encodings:           input.Encodings,
		StateInjections:     input.StateInjections,
		RuntimeCapabilities: input.RuntimeCapabilities,
	}
	if input.BuildSpec != nil {
		req.BuildSpec = *input.BuildSpec
	}
	if strings.TrimSpace(req.BuildSpec.Command) == "" {
		req.BuildSpec.Command = "go build -o /sandbox/out/entrypoint ."
	}
	if strings.TrimSpace(req.BuildSpec.Language) == "" {
		req.BuildSpec.Language = "go"
	}

	if input.RuntimeSpec != nil {
		req.RuntimeSpec = *input.RuntimeSpec
	}
	// Entrypoint sanitization: default to "entrypoint", and strip any leading "/sandbox/" or "/"
	entrypoint := strings.TrimSpace(req.RuntimeSpec.Entrypoint)
	if entrypoint == "" {
		entrypoint = "entrypoint"
	}
	entrypoint = strings.TrimPrefix(entrypoint, "/sandbox/")
	entrypoint = strings.TrimPrefix(entrypoint, "/")
	req.RuntimeSpec.Entrypoint = entrypoint

	mode := strings.TrimSpace(strings.ToLower(req.RuntimeSpec.Network.Mode))
	if mode == "" {
		mode = "none"
	}
	switch mode {
	case "none", "public", "allowlist", "isolated":
		req.RuntimeSpec.Network.Mode = mode
	default:
		return actionscat.CreateVersionReq{}, fmt.Errorf("无效的网络模式 %q，仅支持 'none', 'public', 'allowlist', 'isolated'", mode)
	}

	if mode == "allowlist" {
		if len(req.RuntimeSpec.Network.Allow) == 0 {
			return actionscat.CreateVersionReq{}, fmt.Errorf("当 network.mode 为 'allowlist' 时，allow 规则列表不能为空")
		}
		for i, rule := range req.RuntimeSpec.Network.Allow {
			host := strings.TrimSpace(rule.Host)
			if host == "" {
				return actionscat.CreateVersionReq{}, fmt.Errorf("network.allow[%d] host 不能为空", i)
			}
			if rule.Port < 1 || rule.Port > 65535 {
				return actionscat.CreateVersionReq{}, fmt.Errorf("network.allow[%d] port %d 无效，必须在 1-65535 之间", i, rule.Port)
			}
		}
	}

	if req.RuntimeSpec.TimeoutSeconds <= 0 {
		req.RuntimeSpec.TimeoutSeconds = 30
	}
	return req, nil
}

func truncateLog(log string, maxLen int) string {
	if len(log) <= maxLen {
		return log
	}
	return log[:maxLen] + fmt.Sprintf("\n... [日志过长已截断，总长度 %d 字节] ...", len(log))
}

// AgentBuildDTO represents the build metadata and logs returned to LLM agent tools.
type AgentBuildDTO struct {
	ID             string `json:"id"`
	ActionID       string `json:"action_id"`
	VersionID      string `json:"version_id"`
	BuildNumber    int    `json:"build_number"`
	Status         string `json:"status"`
	BuilderProfile string `json:"builder_profile,omitempty"`
	ExitCode       *int   `json:"exit_code,omitempty"`
	ArtifactDigest string `json:"artifact_digest,omitempty"`
	ArtifactSize   int64  `json:"artifact_size,omitempty"`
	Stdout         string `json:"stdout,omitempty"`
	Stderr         string `json:"stderr,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	CompletedAt    string `json:"completed_at,omitempty"`
	Notice         string `json:"notice,omitempty"`
}

func toAgentBuildDTO(bld *actionscat.ArtifactBuild, includeLogs bool) AgentBuildDTO {
	if bld == nil {
		return AgentBuildDTO{}
	}
	dto := AgentBuildDTO{
		ID:             bld.ID,
		ActionID:       bld.ActionID,
		VersionID:      bld.VersionID,
		BuildNumber:    bld.BuildNumber,
		Status:         bld.Status,
		BuilderProfile: bld.BuilderProfile,
		ExitCode:       bld.ExitCode,
		ArtifactDigest: bld.ArtifactDigest,
		ArtifactSize:   bld.ArtifactSize,
	}
	if !bld.CreatedAt.IsZero() {
		dto.CreatedAt = bld.CreatedAt.UTC().Format(time.RFC3339)
	}
	if bld.CompletedAt != nil && !bld.CompletedAt.IsZero() {
		dto.CompletedAt = bld.CompletedAt.UTC().Format(time.RFC3339)
	}
	if includeLogs {
		dto.Stdout = bld.Stdout
		dto.Stderr = bld.Stderr
	}
	return dto
}

// ActionsCatCreateVersionTool creates a Tool that registers an immutable ActionVersion.
func ActionsCatCreateVersionTool(client *actionscat.Client, scopes ...*runtimescope.Scope) Tool {
	return Tool{
		name: "actionscat_create_version",
		description: "在 ActionsCat 中为指定的 Action 创建一个不可变代码版本 (ActionVersion)（仅限管理员 ADMIN_QQ_IDS）。" +
			"支持传入源代码文件映射 (files)、构建规约 (build_spec)、运行规约 (runtime_spec)、状态注入 (state_injections) 和运行能力 (runtime_capabilities)。" +
			"注意：版本一旦创建永久不可变；创建后需调用 actionscat_build_version 编译生成制品，随后激活方可运行。",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action_id": map[string]any{
					"type":        "string",
					"description": "所属 Action ID",
				},
				"files": map[string]any{
					"type":                 "object",
					"description":          "源代码文件路径与内容的映射表（如 {\"main.go\": \"package main...\"}）",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"encodings": map[string]any{
					"type":                 "object",
					"description":          "可选的文件编码映射（如 base64、utf-8）",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"build_spec": map[string]any{
					"type":        "object",
					"description": "构建环境配置（语言、编译指令等）",
					"properties": map[string]any{
						"language":              map[string]any{"type": "string", "description": "开发语言，默认 go"},
						"toolchain_requirement": map[string]any{"type": "string", "description": "工具链版本要求"},
						"command":               map[string]any{"type": "string", "description": "构建命令，默认 'go build -o /sandbox/out/entrypoint .'"},
						"network":               map[string]any{"type": "boolean", "description": "构建期间是否允许网络访问，默认 false"},
					},
				},
				"runtime_spec": map[string]any{
					"type":        "object",
					"description": "运行环境配置（入口路径、网络策略、超时及资源配额等）",
					"properties": map[string]any{
						"entrypoint":      map[string]any{"type": "string", "description": "相对 /sandbox 的可执行入口路径，默认 'entrypoint'（若指定绝对路径如 '/sandbox/entrypoint' 或 '/entrypoint' 将自动规范化为相对路径）"},
						"timeout_seconds": map[string]any{"type": "integer", "description": "执行超时时间（秒），默认 30"},
						"memory_limit_mb": map[string]any{"type": "integer", "description": "内存上限（MB）"},
						"cpu_limit":       map[string]any{"type": "number", "description": "CPU 配额（核数）"},
						"network": map[string]any{
							"type":        "object",
							"description": "网络访问策略配置",
							"properties": map[string]any{
								"mode": map[string]any{
									"type":        "string",
									"enum":        []string{"none", "public", "allowlist", "isolated"},
									"description": "网络模式：'none'（默认，完全无外网）、'public'（允许公网访问）、'allowlist'（出站白名单）、'isolated'（容器间互联隔离）",
								},
								"allow": map[string]any{
									"type":        "array",
									"description": "出站访问白名单规则（当 mode 为 'allowlist' 时必填且非空）",
									"items": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"host": map[string]any{"type": "string", "description": "允许访问的主机名或域名"},
											"port": map[string]any{"type": "integer", "description": "允许访问的端口号 (1-65535)"},
										},
										"required": []string{"host", "port"},
									},
								},
							},
						},
					},
				},
				"state_injections": map[string]any{
					"type":        "array",
					"description": "需要注入到运行容器状态文件的声明列表",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"state_path": map[string]any{"type": "string"},
							"env_var":    map[string]any{"type": "string"},
							"optional":   map[string]any{"type": "boolean"},
						},
						"required": []string{"state_path", "env_var"},
					},
				},
				"runtime_capabilities": map[string]any{
					"type":        "array",
					"description": "声明请求的运行时权限能力列表（如 [\"state.write\", \"frostagent.sendmsg\"]）",
					"items":       map[string]any{"type": "string"},
				},
			},
			"required": []string{"action_id", "files"},
		},
		executeContext: func(ctx context.Context, args string) (string, error) {
			runContext, ok := llm.RunContextFromContext(ctx)
			if !ok {
				return "无法获取调用者会话上下文，拒绝执行管理操作", nil
			}
			if runContext.Mock {
				return "模拟会话模式下禁用 ActionsCat 创建版本", nil
			}
			if !admincmd.IsAdmin(runContext.ActorUserID, scopes...) {
				return "权限不足: actionscat_create_version 仅允许管理员 (ADMIN_QQ_IDS) 执行", nil
			}
			if client == nil || !client.IsConfigured() {
				return "ActionsCat 尚未配置。请在实例设置中配置 ACTIONSCAT_ENDPOINT 和 ACTIONSCAT_MANAGEMENT_TOKEN。", nil
			}

			var input createVersionInput
			if err := json.Unmarshal([]byte(args), &input); err != nil {
				return fmt.Sprintf("参数解析错误: %v", err), nil
			}
			if strings.TrimSpace(input.ActionID) == "" {
				return "缺少必填参数 'action_id'", nil
			}

			req, err := prepareCreateVersionReq(input)
			if err != nil {
				return fmt.Sprintf("创建版本参数校验失败: %v", err), nil
			}

			ver, err := client.CreateVersion(ctx, input.ActionID, req)
			if err != nil {
				return fmt.Sprintf("创建 ActionsCat Action 版本失败: %v", err), nil
			}

			result := struct {
				actionscat.ActionVersion
				Notice string `json:"notice"`
			}{
				ActionVersion: *ver,
				Notice:        "Action 版本创建成功（版本不可变）。下一步请使用 actionscat_build_version 触发编译构建。",
			}

			data, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return "", fmt.Errorf("serialize created version response: %w", err)
			}
			return string(data), nil
		},
	}
}

// ActionsCatBuildVersionTool creates a Tool that compiles an ActionVersion in a builder sandbox.
func ActionsCatBuildVersionTool(client *actionscat.Client, scopes ...*runtimescope.Scope) Tool {
	return Tool{
		name: "actionscat_build_version",
		description: "在 ActionsCat 中为指定的 ActionVersion 触发沙箱编译构建（仅限管理员 ADMIN_QQ_IDS）。" +
			"构建为同步过程，耗时较长。若构建成功 (status=succeeded)，后续需使用 actionscat_activate_build 激活。" +
			"若构建失败，将返回详细的编译错误日志（stderr/stdout）。由于版本不可变，严禁在当前版本重复重试构建或尝试激活失败构建；" +
			"必须修复代码后通过 actionscat_create_version 创建新版本重新编译。",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action_id": map[string]any{
					"type":        "string",
					"description": "所属 Action ID",
				},
				"version_id": map[string]any{
					"type":        "string",
					"description": "要构建的 ActionVersion ID",
				},
			},
			"required": []string{"action_id", "version_id"},
		},
		executeContext: func(ctx context.Context, args string) (string, error) {
			runContext, ok := llm.RunContextFromContext(ctx)
			if !ok {
				return "无法获取调用者会话上下文，拒绝执行管理操作", nil
			}
			if runContext.Mock {
				return "模拟会话模式下禁用 ActionsCat 构建版本", nil
			}
			if !admincmd.IsAdmin(runContext.ActorUserID, scopes...) {
				return "权限不足: actionscat_build_version 仅允许管理员 (ADMIN_QQ_IDS) 执行", nil
			}
			if client == nil || !client.IsConfigured() {
				return "ActionsCat 尚未配置。请在实例设置中配置 ACTIONSCAT_ENDPOINT 和 ACTIONSCAT_MANAGEMENT_TOKEN。", nil
			}

			var input struct {
				ActionID  string `json:"action_id"`
				VersionID string `json:"version_id"`
			}
			if err := json.Unmarshal([]byte(args), &input); err != nil {
				return fmt.Sprintf("参数解析错误: %v", err), nil
			}
			if strings.TrimSpace(input.ActionID) == "" || strings.TrimSpace(input.VersionID) == "" {
				return "缺少必填参数 'action_id' 或 'version_id'", nil
			}

			bld, err := client.BuildVersion(ctx, input.ActionID, input.VersionID)
			if err != nil {
				if errors.Is(err, actionscat.ErrBuildUnknownResult) {
					return fmt.Sprintf("构建请求超时或网络传输中断，构建结果未知 (后端可能仍在编译中)。\n"+
						"【恢复指引】：严禁立即重复触发构建！请先调用 actionscat_list_builds(action_id=%q, version_id=%q) "+
						"查询该版本是否已在后台生成构建以及其实时状态。若状态为 succeeded，可直接获取 build_id 进行激活；若 building 则稍后重试查询。",
						input.ActionID, input.VersionID), nil
				}
				return fmt.Sprintf("构建版本失败: %v", err), nil
			}

			if bld.Status != "succeeded" {
				var exitCodeStr string
				if bld.ExitCode != nil {
					exitCodeStr = fmt.Sprintf("%d", *bld.ExitCode)
				} else {
					exitCodeStr = "none"
				}
				stderr := truncateLog(bld.Stderr, 4000)
				stdout := truncateLog(bld.Stdout, 4000)
				return fmt.Sprintf("版本构建未成功 (status: %s, exit_code: %s)。版本具有不可变性，请勿在当前版本重复构建或尝试激活；请修复代码后通过 actionscat_create_version 创建新版本重新构建。\n\n=== 构建错误日志 (Stderr) ===\n%s\n\n=== 构建输出日志 (Stdout) ===\n%s",
					bld.Status, exitCodeStr, stderr, stdout), nil
			}

			dto := toAgentBuildDTO(bld, true)
			dto.Notice = "版本构建成功 (status: succeeded)！制品已打包生成。注意：当前 Action 尚未激活此构建，请调用 actionscat_activate_build 将该构建激活为有效运行版本。"

			data, err := json.MarshalIndent(dto, "", "  ")
			if err != nil {
				return "", fmt.Errorf("serialize build response: %w", err)
			}
			return string(data), nil
		},
	}
}

// ActionsCatGetBuildTool creates a Tool that inspects a build's status and logs.
func ActionsCatGetBuildTool(client *actionscat.Client) Tool {
	return Tool{
		name: "actionscat_get_build",
		description: "获取 ActionsCat 某次编译构建 (Build) 的状态、制品哈希、退出码及详细编译日志 (stdout/stderr)。" +
			"可用于检查超时未决的构建任务或分析编译错误原因。",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action_id": map[string]any{
					"type":        "string",
					"description": "所属 Action ID",
				},
				"build_id": map[string]any{
					"type":        "string",
					"description": "构建任务的 Build ID",
				},
				"include_logs": map[string]any{
					"type":        "boolean",
					"description": "是否包含编译输出日志（默认 true）",
				},
			},
			"required": []string{"action_id", "build_id"},
		},
		executeContext: func(ctx context.Context, args string) (string, error) {
			if client == nil || !client.IsConfigured() {
				return "ActionsCat 尚未配置。请在实例设置中配置 ACTIONSCAT_ENDPOINT 和 ACTIONSCAT_MANAGEMENT_TOKEN。", nil
			}

			var input struct {
				ActionID    string `json:"action_id"`
				BuildID     string `json:"build_id"`
				IncludeLogs *bool  `json:"include_logs"`
			}
			if err := json.Unmarshal([]byte(args), &input); err != nil {
				return fmt.Sprintf("参数解析错误: %v", err), nil
			}
			if strings.TrimSpace(input.ActionID) == "" || strings.TrimSpace(input.BuildID) == "" {
				return "缺少必填参数 'action_id' 或 'build_id'", nil
			}

			includeLogs := true
			if input.IncludeLogs != nil {
				includeLogs = *input.IncludeLogs
			}

			bld, err := client.GetBuild(ctx, input.ActionID, input.BuildID)
			if err != nil {
				return fmt.Sprintf("查询 Build 状态失败: %v", err), nil
			}

			dto := toAgentBuildDTO(bld, includeLogs)
			data, err := json.MarshalIndent(dto, "", "  ")
			if err != nil {
				return "", fmt.Errorf("serialize build response: %w", err)
			}
			return string(data), nil
		},
	}
}

// ActionsCatListBuildsTool creates a Tool that lists artifact builds for an Action.
// It allows inspecting build history and recovering from indeterminate build timeouts
// without triggering duplicate builds.
func ActionsCatListBuildsTool(client *actionscat.Client) Tool {
	return Tool{
		name: "actionscat_list_builds",
		description: "列出 ActionsCat 指定 Action 下的历史编译构建列表（按创建时间倒序排列）。" +
			"当 actionscat_build_version 或 actionscat_deploy_action 因超时或网络中断返回未知结果时，" +
			"必须先调用本工具检查对应 version_id 是否已创建构建及其实时状态（succeeded/failed/building），" +
			"避免盲目重试导致并发重复编译。",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action_id": map[string]any{
					"type":        "string",
					"description": "所属 Action ID",
				},
				"version_id": map[string]any{
					"type":        "string",
					"description": "可选：仅列出指定 ActionVersion 的构建记录",
				},
			},
			"required": []string{"action_id"},
		},
	executeContext: func(ctx context.Context, args string) (string, error) {
		if client == nil || !client.IsConfigured() {
			return "ActionsCat 尚未配置。请在实例设置中配置 ACTIONSCAT_ENDPOINT 和 ACTIONSCAT_MANAGEMENT_TOKEN。", nil
		}

		var input struct {
			ActionID  string `json:"action_id"`
			VersionID string `json:"version_id"`
		}
		if err := json.Unmarshal([]byte(args), &input); err != nil {
			return fmt.Sprintf("参数解析错误: %v", err), nil
		}
		if strings.TrimSpace(input.ActionID) == "" {
			return "缺少必填参数 'action_id'", nil
		}

		builds, err := client.ListBuilds(ctx, input.ActionID)
		if err != nil {
			return fmt.Sprintf("查询构建列表失败: %v", err), nil
		}

		var filtered []AgentBuildDTO
		for i := range builds {
			bld := &builds[i]
			if input.VersionID != "" && bld.VersionID != input.VersionID {
				continue
			}
			filtered = append(filtered, toAgentBuildDTO(bld, false))
		}

		if len(filtered) == 0 {
			if input.VersionID != "" {
				return fmt.Sprintf("未查询到 Action %s 中版本 %s 的任何构建记录。", input.ActionID, input.VersionID), nil
			}
			return fmt.Sprintf("未查询到 Action %s 的任何构建记录。", input.ActionID), nil
		}

		data, err := json.MarshalIndent(filtered, "", "  ")
		if err != nil {
			return "", fmt.Errorf("serialize builds response: %w", err)
		}
		return string(data), nil
	},
	}
}

// ActionsCatActivateBuildTool creates a Tool that activates a build for an Action.
func ActionsCatActivateBuildTool(client *actionscat.Client, scopes ...*runtimescope.Scope) Tool {
	return Tool{
		name: "actionscat_activate_build",
		description: "将 ActionsCat 中指定的成功构建 (build_id) 激活为 Action 的当前运行版本（仅限管理员 ADMIN_QQ_IDS）。" +
			"激活后 Action 变为可运行状态 (runnable=true)，方可由 actionscat_run_action 实际执行。" +
			"注意：严格校验构建状态，只有 status 为 'succeeded' 的成功构建才允许被激活。",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action_id": map[string]any{
					"type":        "string",
					"description": "要激活构建的目标 Action ID",
				},
				"version_id": map[string]any{
					"type":        "string",
					"description": "对应的 ActionVersion ID",
				},
				"build_id": map[string]any{
					"type":        "string",
					"description": "要激活的 ArtifactBuild ID（必须为 status=succeeded 的成功构建）",
				},
			},
			"required": []string{"action_id", "version_id", "build_id"},
		},
		executeContext: func(ctx context.Context, args string) (string, error) {
			runContext, ok := llm.RunContextFromContext(ctx)
			if !ok {
				return "无法获取调用者会话上下文，拒绝执行管理操作", nil
			}
			if runContext.Mock {
				return "模拟会话模式下禁用 ActionsCat 激活构建", nil
			}
			if !admincmd.IsAdmin(runContext.ActorUserID, scopes...) {
				return "权限不足: actionscat_activate_build 仅允许管理员 (ADMIN_QQ_IDS) 执行", nil
			}
			if client == nil || !client.IsConfigured() {
				return "ActionsCat 尚未配置。请在实例设置中配置 ACTIONSCAT_ENDPOINT 和 ACTIONSCAT_MANAGEMENT_TOKEN。", nil
			}

			var input struct {
				ActionID  string `json:"action_id"`
				VersionID string `json:"version_id"`
				BuildID   string `json:"build_id"`
			}
			if err := json.Unmarshal([]byte(args), &input); err != nil {
				return fmt.Sprintf("参数解析错误: %v", err), nil
			}
			if strings.TrimSpace(input.ActionID) == "" || strings.TrimSpace(input.VersionID) == "" || strings.TrimSpace(input.BuildID) == "" {
				return "缺少必填参数 'action_id'、'version_id' 或 'build_id'", nil
			}

			// Verify build exists and succeeded before activating
			bld, err := client.GetBuild(ctx, input.ActionID, input.BuildID)
			if err == nil && bld != nil {
				if bld.Status != "succeeded" {
					return fmt.Sprintf("拒绝激活构建: 构建 %s 当前状态为 %q (非 succeeded)，仅允许激活编译成功的构建。若构建失败，请修复代码后通过 actionscat_create_version 创建新版本重新构建。", input.BuildID, bld.Status), nil
				}
				if bld.VersionID != "" && bld.VersionID != input.VersionID {
					return fmt.Sprintf("拒绝激活构建: 构建 %s 关联的版本为 %s，与请求激活的版本 %s 不一致", input.BuildID, bld.VersionID, input.VersionID), nil
				}
			}

			if err := client.ActivateBuild(ctx, input.ActionID, actionscat.SetActiveBuildReq{
				VersionID: input.VersionID,
				BuildID:   input.BuildID,
			}); err != nil {
				return fmt.Sprintf("激活构建失败: %v", err), nil
			}

			act, err := client.GetAction(ctx, input.ActionID)
			if err != nil {
				return fmt.Sprintf("构建已激活，但获取更新后的 Action 失败: %v", err), nil
			}

			resp := struct {
				Action AgentActionDTO `json:"action"`
				Notice string         `json:"notice"`
			}{
				Action: toAgentActionDTO(act),
				Notice: "构建版本激活成功！Action 现已处于可运行状态 (runnable=true)，可以通过 actionscat_run_action 触发执行。",
			}

			data, err := json.MarshalIndent(resp, "", "  ")
			if err != nil {
				return "", fmt.Errorf("serialize activate build response: %w", err)
			}
			return string(data), nil
		},
	}
}

// ActionsCatDeployActionTool creates a high-level composite tool that handles the full lifecycle:
// create_version -> build_version -> inspect status -> activate_build.
func ActionsCatDeployActionTool(client *actionscat.Client, scopes ...*runtimescope.Scope) Tool {
	return Tool{
		name: "actionscat_deploy_action",
		description: "一站式完成 ActionsCat Action 的完整部署生命周期（仅限管理员 ADMIN_QQ_IDS）：" +
			"创建新代码版本 (create_version) -> 触发编译构建 (build_version) -> 校验构建状态 -> 激活成功构建 (activate_build)。" +
			"若编译失败，将返回详细错误日志并中止激活；若构建超时，将返回未知状态提示以防止重复构建。" +
			"部署成功后 Action 自动进入可运行状态 (runnable=true)，可直接通过 actionscat_run_action 执行。",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action_id": map[string]any{
					"type":        "string",
					"description": "所属 Action ID",
				},
				"files": map[string]any{
					"type":                 "object",
					"description":          "源代码文件路径与内容的映射表（如 {\"main.go\": \"package main...\"}）",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"encodings": map[string]any{
					"type":                 "object",
					"description":          "可选的文件编码映射（如 base64、utf-8）",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"build_spec": map[string]any{
					"type":        "object",
					"description": "构建环境配置（语言、编译指令等）",
					"properties": map[string]any{
						"language":              map[string]any{"type": "string", "description": "开发语言，默认 go"},
						"toolchain_requirement": map[string]any{"type": "string", "description": "工具链版本要求"},
						"command":               map[string]any{"type": "string", "description": "构建命令，默认 'go build -o /sandbox/out/entrypoint .'"},
						"network":               map[string]any{"type": "boolean", "description": "构建期间是否允许网络访问，默认 false"},
					},
				},
				"runtime_spec": map[string]any{
					"type":        "object",
					"description": "运行环境配置（入口路径、网络策略、超时及资源配额等）",
					"properties": map[string]any{
						"entrypoint":      map[string]any{"type": "string", "description": "相对 /sandbox 的可执行入口路径，默认 'entrypoint'（若指定绝对路径如 '/sandbox/entrypoint' 或 '/entrypoint' 将自动规范化为相对路径）"},
						"timeout_seconds": map[string]any{"type": "integer", "description": "执行超时时间（秒），默认 30"},
						"memory_limit_mb": map[string]any{"type": "integer", "description": "内存上限（MB）"},
						"cpu_limit":       map[string]any{"type": "number", "description": "CPU 配额（核数）"},
						"network": map[string]any{
							"type":        "object",
							"description": "网络访问策略配置",
							"properties": map[string]any{
								"mode": map[string]any{
									"type":        "string",
									"enum":        []string{"none", "public", "allowlist", "isolated"},
									"description": "网络模式：'none'（默认，完全无外网）、'public'（允许公网访问）、'allowlist'（出站白名单）、'isolated'（容器间互联隔离）",
								},
								"allow": map[string]any{
									"type":        "array",
									"description": "出站访问白名单规则（当 mode 为 'allowlist' 时必填且非空）",
									"items": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"host": map[string]any{"type": "string", "description": "允许访问的主机名或域名"},
											"port": map[string]any{"type": "integer", "description": "允许访问的端口号 (1-65535)"},
										},
										"required": []string{"host", "port"},
									},
								},
							},
						},
					},
				},
				"state_injections": map[string]any{
					"type":        "array",
					"description": "需要注入到运行容器状态文件的声明列表",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"state_path": map[string]any{"type": "string"},
							"env_var":    map[string]any{"type": "string"},
							"optional":   map[string]any{"type": "boolean"},
						},
						"required": []string{"state_path", "env_var"},
					},
				},
				"runtime_capabilities": map[string]any{
					"type":        "array",
					"description": "声明请求的运行时权限能力列表（如 [\"state.write\", \"frostagent.sendmsg\"]）",
					"items":       map[string]any{"type": "string"},
				},
			},
			"required": []string{"action_id", "files"},
		},
		executeContext: func(ctx context.Context, args string) (string, error) {
			runContext, ok := llm.RunContextFromContext(ctx)
			if !ok {
				return "无法获取调用者会话上下文，拒绝执行管理操作", nil
			}
			if runContext.Mock {
				return "模拟会话模式下禁用 ActionsCat 部署操作", nil
			}
			if !admincmd.IsAdmin(runContext.ActorUserID, scopes...) {
				return "权限不足: actionscat_deploy_action 仅允许管理员 (ADMIN_QQ_IDS) 执行", nil
			}
			if client == nil || !client.IsConfigured() {
				return "ActionsCat 尚未配置。请在实例设置中配置 ACTIONSCAT_ENDPOINT 和 ACTIONSCAT_MANAGEMENT_TOKEN。", nil
			}

			var input createVersionInput
			if err := json.Unmarshal([]byte(args), &input); err != nil {
				return fmt.Sprintf("参数解析错误: %v", err), nil
			}
			if strings.TrimSpace(input.ActionID) == "" {
				return "缺少必填参数 'action_id'", nil
			}

			req, err := prepareCreateVersionReq(input)
			if err != nil {
				return fmt.Sprintf("部署参数校验失败: %v", err), nil
			}

			// Step 1: Create Version
			ver, err := client.CreateVersion(ctx, input.ActionID, req)
			if err != nil {
				return fmt.Sprintf("部署失败 (创建版本阶段): %v", err), nil
			}

			// Step 2: Build Version
			bld, err := client.BuildVersion(ctx, input.ActionID, ver.ID)
			if err != nil {
				if errors.Is(err, actionscat.ErrBuildUnknownResult) {
					return fmt.Sprintf("部署中断: 版本 %s 构建请求超时或网络传输中断，构建结果未知 (后端可能仍在编译中)。\n"+
						"【恢复指引】：严禁立即重新部署或重复提交构建！请先调用 actionscat_list_builds(action_id=%q, version_id=%q) "+
						"检查该版本是否已在后台生成构建。若构建成功 (succeeded)，可调用 actionscat_activate_build 完成激活；若失败则排查日志后新建版本。",
						ver.ID, input.ActionID, ver.ID), nil
				}
				return fmt.Sprintf("部署失败 (编译构建阶段): %v", err), nil
			}

			// Step 3: Check Build Status
			if bld.Status != "succeeded" {
				var exitCodeStr string
				if bld.ExitCode != nil {
					exitCodeStr = fmt.Sprintf("%d", *bld.ExitCode)
				} else {
					exitCodeStr = "none"
				}
				stderr := truncateLog(bld.Stderr, 4000)
				stdout := truncateLog(bld.Stdout, 4000)
				return fmt.Sprintf("部署失败: 版本 %s 构建未成功 (状态: %s, 退出码: %s)。版本具有不可变性，请修复代码后重新部署。\n\n=== 构建错误日志 (Stderr) ===\n%s\n\n=== 构建输出日志 (Stdout) ===\n%s",
					ver.ID, bld.Status, exitCodeStr, stderr, stdout), nil
			}

			// Step 4: Activate Build
			if err := client.ActivateBuild(ctx, input.ActionID, actionscat.SetActiveBuildReq{
				VersionID: ver.ID,
				BuildID:   bld.ID,
			}); err != nil {
				return fmt.Sprintf("部署失败 (激活构建阶段): %v", err), nil
			}

			// Step 5: Return updated runnable action
			act, err := client.GetAction(ctx, input.ActionID)
			if err != nil {
				return fmt.Sprintf("部署成功已激活，但获取最终 Action 状态失败: %v", err), nil
			}

			resp := struct {
				Action    AgentActionDTO `json:"action"`
				VersionID string         `json:"version_id"`
				BuildID   string         `json:"build_id"`
				Status    string         `json:"status"`
				Notice    string         `json:"notice"`
			}{
				Action:    toAgentActionDTO(act),
				VersionID: ver.ID,
				BuildID:   bld.ID,
				Status:    "deployed",
				Notice:    "Action 部署并激活成功！当前已具备有效构建，处于可运行状态 (runnable=true)，可直接通过 actionscat_run_action 执行。",
			}

			data, err := json.MarshalIndent(resp, "", "  ")
			if err != nil {
				return "", fmt.Errorf("serialize deploy action response: %w", err)
			}
			return string(data), nil
		},
	}
}
