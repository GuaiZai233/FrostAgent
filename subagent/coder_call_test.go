package subagent

import (
	"FrostAgent/internal/core"
	"context"
	"errors"
	"testing"
)

type failingProvider struct{}

func (failingProvider) Chat(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
	return nil, errors.New("test provider failure")
}

func TestCallCoder(t *testing.T) {
	// 准备数据
	content := "使用golang写一个 Hello World"

	// 调用函数
	result, err := CallCoder(context.Background(), failingProvider{}, core.RouteContext{}, content)

	if err == nil {
		t.Fatal("预期返回错误，但得到了 nil")
	}

	if result != "" {
		t.Errorf("预期空结果，但得到了 %q", result)
	}
}
