package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"FrostAgent/internal/actionscat"
)

const (
	defaultRunWaitDuration = 25 * time.Second
)

// ActionsCatListActionsTool creates a Tool that lists registered ActionsCat actions.
func ActionsCatListActionsTool(client *actionscat.Client) Tool {
	return Tool{
		name: "actionscat_list_actions",
		description: "列出 ActionsCat 自动化平台中已注册的 Actions。返回各 Action 的 ID、名称、描述、" +
			"启用状态及激活版本，供 Agent 发现并决策调用对应任务。",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"enabled_only": map[string]any{
					"type":        "boolean",
					"description": "是否仅返回已启用的 Action（默认 false，返回全部）",
				},
			},
		},
		executeContext: func(ctx context.Context, args string) (string, error) {
			if client == nil || !client.IsConfigured() {
				return "ActionsCat 尚未配置。请在实例设置中配置 ACTIONSCAT_ENDPOINT 和 ACTIONSCAT_MANAGEMENT_TOKEN。", nil
			}

			var input struct {
				EnabledOnly bool `json:"enabled_only"`
			}
			if strings.TrimSpace(args) != "" {
				_ = json.Unmarshal([]byte(args), &input)
			}

			actions, err := client.ListActions(ctx)
			if err != nil {
				return fmt.Sprintf("获取 ActionsCat actions 列表失败: %v", err), nil
			}

			var filtered []actionscat.Action
			for _, a := range actions {
				if input.EnabledOnly && !a.Enabled {
					continue
				}
				filtered = append(filtered, a)
			}

			resp := struct {
				Total   int                 `json:"total"`
				Actions []actionscat.Action `json:"actions"`
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
				return fmt.Sprintf("触发 ActionsCat Action %s 失败: %v", input.ActionID, err), nil
			}

			data, err := json.MarshalIndent(run, "", "  ")
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

			if !includeLogs {
				run.Stdout = ""
				run.Stderr = ""
			}

			data, err := json.MarshalIndent(run, "", "  ")
			if err != nil {
				return "", fmt.Errorf("serialize run response: %w", err)
			}
			return string(data), nil
		},
	}
}
