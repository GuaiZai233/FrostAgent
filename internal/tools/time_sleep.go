package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/timesleep"
)

// TimeSleepTool waits inside the current execution and respects its cancellation.
// Configuration is captured per call, so updates apply to subsequent calls.
func TimeSleepTool(getenv func(string) string) Tool {
	return Tool{
		name:        timesleep.ToolName,
		description: "当你收到尚未说完、说了一半的消息，或正在等待工具输出结果、等候用户回应时，调用此工具，可以在保持此会话的情况下，挂起一段时间。",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"seconds": map[string]any{
					"type":             "number",
					"exclusiveMinimum": 0,
					"description":      "挂起秒数；达到配置的超时上限时返回 Timeout（默认 60 秒）",
				},
			},
			"required":             []string{"seconds"},
			"additionalProperties": false,
		},
		executeContext: func(ctx context.Context, args string) (string, error) {
			var params struct {
				Seconds *float64 `json:"seconds"`
			}
			if err := json.Unmarshal([]byte(args), &params); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			if params.Seconds == nil {
				return "", errors.New("seconds 参数不能为空")
			}
			duration, err := timesleep.Duration(*params.Seconds)
			if err != nil {
				return "", err
			}
			var raw string
			if getenv != nil {
				raw = getenv(timesleep.TimeoutKey)
			}
			limit, err := timesleep.Timeout(raw)
			if err != nil {
				return "", err
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			timedOut := duration >= limit
			if timedOut {
				duration = limit
			}
			timer := time.NewTimer(duration)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-timer.C:
				if err := ctx.Err(); err != nil {
					return "", err
				}
				if timedOut {
					return "", timesleep.ErrTimeout
				}
				return fmt.Sprintf("已挂起 %g 秒", *params.Seconds), nil
			}
		},
	}
}
