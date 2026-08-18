package billing

import (
	"strings"
	"testing"
)

func TestFormatReceipt(t *testing.T) {
	// Standard receipt
	r1 := FormatReceipt(3, 1500, 300, 9997, false)
	if !strings.Contains(r1, "0.03 片雪花") || !strings.Contains(r1, "输入: 1500, 输出: 300") || !strings.Contains(r1, "99.97 片") {
		t.Errorf("unexpected receipt format: %s", r1)
	}
	if strings.Contains(r1, "首次对话赠送") {
		t.Errorf("should not contain welcome message: %s", r1)
	}

	// Welcome bonus receipt
	r2 := FormatReceipt(2, 1000, 200, 9998, true)
	if !strings.Contains(r2, "🎉 首次对话赠送 100.00 片雪花！") {
		t.Errorf("expected welcome bonus announcement in receipt: %s", r2)
	}
	if !strings.Contains(r2, "0.02 片雪花") || !strings.Contains(r2, "99.98 片") {
		t.Errorf("unexpected receipt values: %s", r2)
	}
}

func TestFormatInsufficientFundsMessage(t *testing.T) {
	msg := FormatInsufficientFundsMessage(15) // 0.15 snowflakes
	if !strings.Contains(msg, "0.15 片") || !strings.Contains(msg, "余额不足") {
		t.Errorf("unexpected insufficient funds message: %s", msg)
	}
}

func TestFormatBillingUnavailableMessage(t *testing.T) {
	msg := FormatBillingUnavailableMessage()
	if !strings.Contains(msg, "计费系统暂时不可用") {
		t.Errorf("unexpected unavailable message: %s", msg)
	}
}
