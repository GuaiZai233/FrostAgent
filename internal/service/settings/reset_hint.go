package settings

import (
	"FrostAgent/internal/instanceconfig"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// These are code fallbacks after deletion, not the template values written when
// a new instance is created. Dependent values also describe their source.
var resetHints = map[string]string{
	"LISTEN_ADDR":                          "127.0.0.1:8080",
	"WS_LISTEN_ADDR":                       "127.0.0.1:1234",
	"HTTP_ALLOWED_ORIGINS":                 "不额外放行跨域来源",
	"WS_ALLOWED_ORIGINS":                   "不额外放行跨域来源",
	"SECURITY_CONTROL_MODE":                "基础模式",
	"MAX_CONTEXT_MESSAGES":                 "50 条（代码默认值）",
	"ADMIN_QQ_IDS":                         "未配置管理员",
	"ADMIN_COMMAND_PREFIX":                 "/",
	"GROUP_COMPACT_BUFFER_SIZE":            "20 条",
	"GROUP_COMPACT_MIN_INTERVAL":           "30s",
	"GROUP_RAW_CONTEXT_MAX_CHARS":          "12000 字符（仅未压缩群消息）",
	"MEMORY_EXTRACT_BATCH_MIN":             "3 轮",
	"MEMORY_EXTRACT_BATCH_MAX":             "5 轮（不小于最小累计轮数）",
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
	"MCP_CONTROL_TOKEN":                    "回退使用兼容管理令牌；两者均为空时远程管理不可用",
	"ADMIN_TOKEN":                          "未配置兼容令牌；管理入口继续优先使用管理控制令牌",
	"MCP_ENFORCE_LOCAL_TOKEN":              "关闭本机强制令牌；远程管理仍需令牌",
	"AGENT_MAX_ITERATIONS":                 "35 轮",
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
		return fmt.Sprintf("每批数量的 10 倍与 200 取较大值（当前 %d 条）", max(batch*10, 200))
	}
	if instanceconfig.SharedKeys[key] {
		for _, candidate := range []string{"SECURITY_GATEWAY_TIMEOUT", "SECURITY_CLASSIFIER_TIMEOUT"} {
			raw, source := "", ""
			if s.config != nil && candidate != key {
				raw = strings.TrimSpace(s.config.Get(candidate))
				source = "当前实例"
			}
			if raw == "" && s.global != nil && (s.config != nil || candidate != key) {
				raw = strings.TrimSpace(s.global.Get(candidate))
				source = "继承全局配置"
			}
			if raw != "" {
				if value, err := time.ParseDuration(raw); err != nil || value <= 0 {
					return "15s（已有超时配置无效，使用代码默认值）"
				}
				name := "安全审查网关超时"
				if candidate == "SECURITY_CLASSIFIER_TIMEOUT" {
					name = "兼容安全审查超时"
				}
				return fmt.Sprintf("%s · %s：%s", source, name, raw)
			}
		}
		return "15s（未配置网关或兼容超时，使用代码默认值）"
	}
	return resetHints[key]
}
