package subagent

import (
	"FrostAgent/internal/llm"
	"testing"
)

func TestCallCoder(t *testing.T) {
	// 准备数据
	content := "使用golang写一个 Hello World"

	// 调用函数
	result := CallCoder(&llm.Client{}, "", "", "", content)

	// 断言：因为我们用了假的 key，预期应该返回错误信息
	if result == "" {
		t.Error("预期返回错误信息，但得到了空字符串")
	}

	t.Logf("测试结果: %s", result)
}
