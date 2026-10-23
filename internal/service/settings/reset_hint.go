package settings

import (
	"FrostAgent/internal/instanceconfig"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Input placeholders describe code defaults, including dependent defaults.
var resetHints = map[string]string{
	"ENABLE_POKE":                          "关闭",
	"POKE_CUSTOM_TEXT":                     "的...",
	"POKE_COOLDOWN_SECONDS":                "30",
	"LISTEN_ADDR":                          "127.0.0.1:8080",
	"WS_LISTEN_ADDR":                       instanceconfig.DefaultWSListenAddr,
	"HTTP_ALLOWED_ORIGINS":                 "不额外放行跨域来源",
	"WS_ALLOWED_ORIGINS":                   "不额外放行跨域来源",
	"SECURITY_CONTROL_MODE":                "基础模式",
	"MAX_CONTEXT_MESSAGES":                 "50",
	"ADMIN_QQ_IDS":                         "未配置管理员",
	"ADMIN_COMMAND_PREFIX":                 "/",
	"GROUP_COMPACT_BUFFER_SIZE":            "20",
	"GROUP_COMPACT_MIN_INTERVAL":           "30s",
	"GROUP_RAW_CONTEXT_MAX_CHARS":          "12000",
	"MEMORY_EXTRACT_BATCH_MIN":             "3",
	"MEMORY_EXTRACT_BATCH_MAX":             "5（不小于下限）",
	"MEMORY_REFLECTION_TIMEOUT":            "10m",
	"ONEBOT_ACTION_TIMEOUT":                "10s",
	"BILLING_ENABLED":                      "关闭",
	"ALCYONE_BASE_URL":                     "http://127.0.0.1:8081",
	"ALCYONE_SERVICE_TOKEN":                "未配置 Token",
	"ALCYONE_TIMEOUT":                      "5s",
	"BILLING_MAX_OUTPUT_TOKENS":            "2048",
	"BILLING_SAFETY_MULTIPLIER":            "1.2",
	"BILLING_PROMPT_PRICE_PER_MILLION":     "使用模型价格表；未登记模型为 2000 单位/百万 Token",
	"BILLING_COMPLETION_PRICE_PER_MILLION": "使用模型价格表；未登记模型为 8000 单位/百万 Token",
	"SANDBOX_ENABLED":                      "关闭",
	"SANDBOX_BASE_URL":                     "http://127.0.0.1:3874",
	"SANDBOX_AUTH_TOKEN":                   "未配置 Token",
	"SANDBOX_SESSION_NAMESPACE":            "frostagent",
	"MCP_CONTROL_TOKEN":                    "复用兼容管理令牌",
	"ADMIN_TOKEN":                          "未配置兼容令牌",
	"MCP_ENFORCE_LOCAL_TOKEN":              "关闭本机强制令牌；远程管理仍需令牌",
	"AGENT_MAX_ITERATIONS":                 "35",
	"ACTIONSCAT_ENABLED":                   "开启",
	"ACTIONSCAT_ENDPOINT":                  "未配置服务地址",
	"ACTIONSCAT_MANAGEMENT_TOKEN":          "未配置管理 Token",
	"ACTIONSCAT_DISPATCH_TOKEN":            "复用 ActionsCat 管理 Token",
}

func (s *Service) resetHint(key string) string {
	if key == "GROUP_COMPACT_MAX_BUFFER_SIZE" {
		batch := 20
		if store := s.store("GROUP_COMPACT_BUFFER_SIZE"); store != nil {
			if value, err := strconv.Atoi(store.Get("GROUP_COMPACT_BUFFER_SIZE")); err == nil && value > 0 {
				batch = value
			}
		}
		return fmt.Sprintf("%d（按每批 %d 条）", max(batch*10, 200), batch)
	}
	if instanceconfig.SharedKeys[key] {
		for _, candidate := range []string{"SECURITY_GATEWAY_TIMEOUT", "SECURITY_CLASSIFIER_TIMEOUT"} {
			raw, source := "", ""
			if s.config != nil && candidate != key {
				raw = strings.TrimSpace(s.config.Get(candidate))
				source = "实例配置"
			}
			if raw == "" && s.global != nil && (s.config != nil || candidate != key) {
				raw = strings.TrimSpace(s.global.Get(candidate))
				source = "全局继承"
			}
			if raw != "" {
				if value, err := time.ParseDuration(raw); err != nil || value <= 0 {
					return "15s"
				}
				name := "网关超时"
				if candidate == "SECURITY_CLASSIFIER_TIMEOUT" {
					name = "兼容超时"
				}
				return fmt.Sprintf("%s（%s · %s）", raw, source, name)
			}
		}
		return "15s"
	}
	return resetHints[key]
}
