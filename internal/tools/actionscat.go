package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/actionscat"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
)

// ActionsCatTool exposes the ActionsCat lifecycle through on-demand discovery.
// Subtools retain their own authorization, mock-session and client checks.
func ActionsCatTool(client *actionscat.Client, scopes ...*runtimescope.Scope) Tool {
	subtools := []Tool{
		ActionsCatListActionsTool(client), ActionsCatRunActionTool(client), ActionsCatGetRunTool(client),
		ActionsCatCreateActionTool(client, scopes...), ActionsCatCreateVersionTool(client, scopes...),
		ActionsCatBuildVersionTool(client, scopes...), ActionsCatGetBuildTool(client), ActionsCatListBuildsTool(client),
		ActionsCatActivateBuildTool(client, scopes...), ActionsCatDeployActionTool(client, scopes...),
		ActionsCatCreateScheduleTool(client, scopes...), ActionsCatListSchedulesTool(client),
		ActionsCatDeleteScheduleTool(client, scopes...), ActionsCatCreateMatcherTool(client, scopes...),
		ActionsCatListMatchersTool(client), ActionsCatDeleteMatcherTool(client, scopes...),
	}
	byAction := make(map[string]Tool, len(subtools))
	actions := []string{"discover"}
	for _, tool := range subtools {
		action := strings.TrimPrefix(tool.Name(), "actionscat_")
		byAction[action] = tool
		actions = append(actions, action)
	}
	return Tool{
		name:        "actionscat",
		description: "ActionsCat automation: actions, source versions, builds, deployment, runs, schedules and event matchers. First call action=discover to list capabilities, then discover with capability to inspect its description and JSON parameters. Execute using action=<capability> and arguments=<parameters>. Management operations require ADMIN_QQ_IDS; mock sessions cannot run or change tasks. Never retry an unknown build/deployment result; inspect list_builds first.",
		parameter: map[string]any{"type": "object", "properties": map[string]any{
			"action":     map[string]any{"type": "string", "enum": actions},
			"capability": map[string]any{"type": "string", "description": "Capability to inspect when action=discover; omit to list capabilities."},
			"arguments":  map[string]any{"type": "object", "description": "Parameters returned by discover for the selected action."},
		}, "required": []string{"action"}, "additionalProperties": false},
		enabled:       client.IsEnabled,
		configured:    client.IsConfigured,
		disabledError: actionscat.ErrDisabled,
		executeContext: func(ctx context.Context, raw string) (string, error) {
			var input struct {
				Action     string          `json:"action"`
				Capability string          `json:"capability,omitempty"`
				Arguments  json.RawMessage `json:"arguments,omitempty"`
			}
			data := bytes.TrimSpace([]byte(raw))
			if len(data) == 0 || data[0] != '{' {
				return "", fmt.Errorf("ActionsCat 参数必须为 JSON 对象")
			}
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				return "", fmt.Errorf("ActionsCat 参数无效: %w", err)
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				return "", fmt.Errorf("ActionsCat 参数包含多余 JSON")
			}
			scope := runtimescope.First(scopes)
			if scope == nil {
				scope = runtimescope.FromContext(ctx)
			}
			settings := &llm.Engine{Scope: scope}
			if input.Action == "discover" {
				if len(input.Arguments) != 0 {
					return "", fmt.Errorf("ActionsCat discover 不接受 arguments；请使用 capability 查询参数")
				}
				if input.Capability == "" {
					capabilities := make([]string, 0, len(subtools))
					for _, action := range actions[1:] {
						if settings.ToolEnabled(byAction[action].Name()) {
							capabilities = append(capabilities, action)
						}
					}
					payload, err := json.Marshal(map[string]any{"capabilities": capabilities, "available": client.IsEnabled() && client.IsConfigured()})
					return string(payload), err
				}
			} else if input.Capability != "" {
				return "", fmt.Errorf("ActionsCat capability 仅用于 action=discover；执行参数应放入 arguments")
			}
			action := input.Action
			if action == "discover" {
				action = input.Capability
			}
			tool, ok := byAction[action]
			if !ok {
				return "", fmt.Errorf("unknown ActionsCat capability %q; use action=discover", action)
			}
			// Honor persisted per-tool switches from before the routed interface.
			if !settings.ToolEnabled(tool.Name()) {
				return "", fmt.Errorf("ActionsCat capability %q 已被管理员禁用", action)
			}
			if input.Action == "discover" {
				payload, err := json.Marshal(map[string]any{
					"capability": action, "description": tool.Description(), "parameters": tool.Parameters(),
					"available": tool.IsEnabled(),
				})
				return string(payload), err
			}
			arguments := bytes.TrimSpace(input.Arguments)
			if len(arguments) == 0 {
				arguments = []byte("{}")
			}
			if arguments[0] != '{' {
				return "", fmt.Errorf("ActionsCat arguments 必须为 JSON 对象")
			}
			return tool.ExecuteContext(ctx, string(arguments))
		},
	}
}
