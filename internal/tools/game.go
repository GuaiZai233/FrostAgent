package tools

import (
	"encoding/json"
	"fmt"
)

func GetGameVersionTool() Tool {
	return Tool{
		name:        "get_game_version",
		description: "获取游戏的版本信息",
		//json schema
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"game": map[string]any{
					"type":        "string",
					"description": "要查询的游戏名称",
				},
			},
			"required": []string{"game"},
		},

		//工具执行逻辑
		execute: func(args string) (string, error) {
			var params struct {
				Game string `json:"game"`
			}
			if err := json.Unmarshal([]byte(args), &params); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}

			if params.Game == "" {
				return "", fmt.Errorf("参数 game 不能为空")
			}

			return fmt.Sprintf("%s 的最新版本是 V1.14.51", params.Game), nil
		},
	}
}
