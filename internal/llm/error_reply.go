package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"

	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/openai"
)

// llmErrorType intentionally uses only known types and numeric status codes.
// Provider bodies, endpoint URLs and arbitrary error text stay in the log store.
func llmErrorType(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "请求超时"
	}
	if errors.Is(err, context.Canceled) {
		return "请求已取消"
	}
	var httpErr *openai.HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode >= 100 && httpErr.StatusCode <= 599 {
			return fmt.Sprintf("上游 HTTP %d", httpErr.StatusCode)
		}
		return "上游服务错误"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "请求超时"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return "网络请求失败"
	}
	return "模型调用错误"
}
