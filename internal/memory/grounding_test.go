package memory

import (
	"testing"
)

func TestCountNegationMarkers(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"我不是管理员", 1},
		{"我是管理员", 0},
		{"我不喜欢舞萌", 1},
		{"我喜欢舞萌", 0},
		{"没有收到通知", 1},
		{"收到通知", 0},
		{"不仅喜欢舞萌，而且喜欢打机", 0}, // conjunction "不仅" is not negation
		{"不但没有迟到，反而早到了", 1},   // "不但" is stripped, "没有" is counted
		{"I am not an admin", 1},
		{"I am an admin", 0},
		{"Assistant says noted and confirmed", 0}, // "noted" must not match "not"
		{"I don't like music games", 1},
	}

	for _, tt := range tests {
		got := CountNegationMarkers(tt.input)
		if got != tt.expected {
			t.Errorf("CountNegationMarkers(%q) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestFindEnclosingClause(t *testing.T) {
	msg := "我今天来打机，李四喜欢玩舞萌；王五在看书。"

	c1 := FindEnclosingClause(msg, "我今天来打机")
	if c1 != "我今天来打机" {
		t.Errorf("expected %q, got %q", "我今天来打机", c1)
	}

	c2 := FindEnclosingClause(msg, "李四喜欢玩舞萌")
	if c2 != "李四喜欢玩舞萌" {
		t.Errorf("expected %q, got %q", "李四喜欢玩舞萌", c2)
	}

	c3 := FindEnclosingClause(msg, "玩舞萌")
	if c3 != "李四喜欢玩舞萌" {
		t.Errorf("expected %q, got %q", "李四喜欢玩舞萌", c3)
	}
}

func TestHasPolarityInversion(t *testing.T) {
	tests := []struct {
		evidence  string
		content   string
		srcMsg    string
		inverted  bool
	}{
		{"我不是管理员", "我是管理员", "我不是管理员", true},
		{"管理员", "我是管理员", "我不是管理员", true},
		{"管理员", "用户是管理员", "我不是管理员", true},
		{"我不喜欢舞萌", "我喜欢舞萌", "我不喜欢舞萌", true},
		{"舞萌", "用户喜欢舞萌", "我不喜欢舞萌", true},
		{"我不是管理员", "用户不是管理员", "我不是管理员", false},
		{"我不喜欢舞萌", "用户不喜欢舞萌", "我不喜欢舞萌", false},
		{"我喜欢舞萌", "我不喜欢舞萌", "我喜欢舞萌", true},
		{"我喜欢舞萌", "用户喜欢舞萌", "我喜欢舞萌", false},
	}

	for _, tt := range tests {
		got := HasPolarityInversion(tt.evidence, tt.content, tt.srcMsg)
		if got != tt.inverted {
			t.Errorf("HasPolarityInversion(%q, %q, %q) = %v, want %v", tt.evidence, tt.content, tt.srcMsg, got, tt.inverted)
		}
	}
}

func TestIsFirstPersonStatement(t *testing.T) {
	tests := []struct {
		evidence   string
		content    string
		fullMsg    string
		senderName string
		want       bool
	}{
		// Finding 2 regression: unrelated "我" in message, but evidence/content describes 李四
		{"李四喜欢玩舞萌", "李四喜欢玩舞萌", "张三: 我今天来打机，李四喜欢玩舞萌", "张三", false},
		{"喜欢玩舞萌", "李四喜欢舞萌", "张三: 我今天来打机，李四喜欢玩舞萌", "张三", false},
		// Valid quoted self-claim preserved
		{"我今天来打机", "用户今天来打机", "张三: 我今天来打机，李四喜欢玩舞萌", "张三", true},
		{"我平时喜欢喝咖啡", "用户平时喜欢喝咖啡", "张三: 我平时喜欢喝咖啡", "张三", true},
		{"Alice drinks matcha latte", "Alice drinks matcha latte", "Alice: Alice drinks matcha latte every morning", "Alice", true},
	}

	for _, tt := range tests {
		got := IsFirstPersonStatement(tt.evidence, tt.content, tt.fullMsg, tt.senderName)
		if got != tt.want {
			t.Errorf("IsFirstPersonStatement(ev=%q, cont=%q, msg=%q, sender=%q) = %v, want %v",
				tt.evidence, tt.content, tt.fullMsg, tt.senderName, got, tt.want)
		}
	}
}
