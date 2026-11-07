package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/GuaiZai233/FrostAgent/internal/balancequery"
)

func BalanceQueryTool(get func(string) string, resolve balancequery.KeyResolver) Tool {
	return Tool{
		name:        balancequery.ToolName,
		description: "查询管理员配置的 API 账户额度。请求端点、参数和 API key 由 FrostAgent 后端管理，不接受模型传入。返回查询接口的原始响应正文。",
		parameter:   map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		configured:  func() bool { return balancequery.Configured(get, resolve) },
		executeContext: func(ctx context.Context, args string) (string, error) {
			var input map[string]json.RawMessage
			if json.Unmarshal([]byte(args), &input) != nil || input == nil || len(input) != 0 {
				return "", errors.New("balance_query 不接受参数")
			}
			cfg, err := balancequery.LoadResolved(get, resolve)
			if err != nil {
				return "", errors.New("额度查询配置无效，请联系管理员")
			}
			return balancequery.Execute(ctx, cfg)
		},
	}
}
