package billing

import "fmt"

// FormatReceipt creates the standard consumption receipt string.
func FormatReceipt(actualMinor int64, promptTokens, completionTokens int, balanceMinor int64, welcomeGranted bool) string {
	receipt := fmt.Sprintf(
		"❄️ 本次消耗: %s 片雪花 (输入: %d, 输出: %d) | 剩余余额: %s 片",
		FormatSnowflakes(actualMinor),
		promptTokens,
		completionTokens,
		FormatSnowflakes(balanceMinor),
	)
	if welcomeGranted {
		return fmt.Sprintf("🎉 首次对话赠送 100.00 片雪花！\n%s", receipt)
	}
	return receipt
}

// FormatInsufficientFundsMessage formats the polite error message when balance is insufficient.
func FormatInsufficientFundsMessage(balanceMinor int64) string {
	return fmt.Sprintf("⚠️ 您的雪花余额不足（当前余额：%s 片），无法发起对话。请联系管理员充值后再试~", FormatSnowflakes(balanceMinor))
}

// FormatBillingUnavailableMessage formats the error message when Alcyone service is unreachable.
func FormatBillingUnavailableMessage() string {
	return "⚠️ 计费系统暂时不可用，无法发起对话，请稍后重试。"
}
