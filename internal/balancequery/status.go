package balancequery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// QuotaStatus is the quota conversion configuration published by a New API site at /api/status.
type QuotaStatus struct {
	QuotaPerUnit     float64
	USDExchangeRate  float64
	QuotaDisplayType string
}

// OriginOf returns the scheme and host of the endpoint a template targets.
func OriginOf(curl string) (string, error) {
	spec, err := parseCurl(curl)
	if err != nil {
		return "", err
	}
	return spec.url.Scheme + "://" + spec.url.Host, nil
}

// FetchQuotaStatus reads the New API status configuration from origin/api/status. Redirects are not
// followed, so the request cannot be bounced to a different host.
func FetchQuotaStatus(ctx context.Context, origin string) (QuotaStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/api/status", nil)
	if err != nil {
		return QuotaStatus{}, errors.New("远端状态地址无效")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return QuotaStatus{}, errors.New("请求远端 /api/status 失败或超时")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return QuotaStatus{}, errors.New("远端 /api/status 返回非成功状态")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes))
	if err != nil {
		return QuotaStatus{}, errors.New("读取远端 /api/status 失败")
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			QuotaPerUnit     float64 `json:"quota_per_unit"`
			USDExchangeRate  float64 `json:"usd_exchange_rate"`
			QuotaDisplayType string  `json:"quota_display_type"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &payload) != nil || !payload.Success {
		return QuotaStatus{}, errors.New("远端 /api/status 不是有效的 New API 状态")
	}
	if payload.Data.QuotaPerUnit <= 0 || math.IsNaN(payload.Data.QuotaPerUnit) || math.IsInf(payload.Data.QuotaPerUnit, 0) {
		return QuotaStatus{}, errors.New("远端 /api/status 未返回有效的 quota_per_unit")
	}
	return QuotaStatus{
		QuotaPerUnit:     payload.Data.QuotaPerUnit,
		USDExchangeRate:  payload.Data.USDExchangeRate,
		QuotaDisplayType: payload.Data.QuotaDisplayType,
	}, nil
}

// SameOrigin reports whether a template's request target has the same scheme, host and port as baseURL.
// Paths are ignored. A template that does not parse, or a base URL without a host, never matches.
func SameOrigin(curl, baseURL string) bool {
	origin, err := OriginOf(curl)
	if err != nil {
		return false
	}
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return false
	}
	return strings.EqualFold(origin, base.Scheme+"://"+base.Host)
}
