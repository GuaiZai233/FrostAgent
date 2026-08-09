package tools

import (
	"context"
	"fmt"
)

type Tool struct {
	name           string
	description    string
	parameter      any //用于大模型生成json schema
	execute        func(args string) (string, error)
	executeContext func(ctx context.Context, args string) (string, error)
}

func (t Tool) Name() string {
	return t.name // 返回字段里的值
}

func (t Tool) Description() string {
	return t.description
}

func (t Tool) Parameters() map[string]any {
	if params, ok := t.parameter.(map[string]any); ok {
		return params
	}
	return nil
}

func (t Tool) Execute(args string) (string, error) {
	return t.ExecuteContext(context.Background(), args)
}

// ExecuteContext supplies request-local values to tools that need them.
func (t Tool) ExecuteContext(ctx context.Context, args string) (string, error) {
	if t.executeContext != nil {
		return t.executeContext(ctx, args)
	}
	if t.execute == nil {
		return "", fmt.Errorf("tool %s has no executor", t.name)
	}
	return t.execute(args)
}
