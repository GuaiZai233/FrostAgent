package balancequery

import (
	"encoding/json"
	"errors"
	"fmt"
)

// validateNewAPIPayload rejects a New API response that is not a usable quota payload. A request can
// succeed at the HTTP layer yet carry no data (for example a wrong or unauthorised token), so the
// payload must be a JSON object that is not marked unsuccessful and that contains a quota field,
// either at the top level or under "data".
func validateNewAPIPayload(body string) error {
	var root map[string]any
	if err := json.Unmarshal([]byte(body), &root); err != nil || root == nil {
		return errors.New("New API 返回的不是有效的 JSON 对象，请检查请求地址与 Key")
	}
	if success, ok := root["success"].(bool); ok && !success {
		message, _ := root["message"].(string)
		if message == "" {
			message = "接口返回失败"
		}
		return fmt.Errorf("New API 返回失败：%s", message)
	}
	if hasQuotaField(root) {
		return nil
	}
	if data, ok := root["data"].(map[string]any); ok && hasQuotaField(data) {
		return nil
	}
	return errors.New("New API 返回中没有额度字段（quota），请检查请求地址、Key 与令牌权限")
}

func hasQuotaField(fields map[string]any) bool {
	for name := range fields {
		if quotaFields[name] {
			return true
		}
	}
	return false
}
