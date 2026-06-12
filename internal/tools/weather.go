package tools

import (
	"encoding/json"
	"fmt"
)

func GetWeatherTool() Tool {
	return Tool{
		name:        "get_weather",
		description: "获取指定城市的天气信息",
		//json schema
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{
					"type":        "string",
					"description": "要查询天气的城市名称",
				},
			},
			"required": []string{"city"},
		},

		//工具执行逻辑
		execute: func(args string) (string, error) {
			var params struct {
				City string `json:"city"`
			}
			if err := json.Unmarshal([]byte(args), &params); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}

			if params.City == "" {
				return "", fmt.Errorf("参数 city 不能为空")
			}

			//这里我们模拟返回天气信息，实际应用中可以调用第三方天气API
			return fmt.Sprintf("%s 的天气是晴朗，温度 25°C", params.City), nil
		},
	}
}
