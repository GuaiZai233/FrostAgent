package modelrouter

import (
	"fmt"
	"os"
	"strings"
)

func resolveEndpointAPIKey(endpoint Endpoint) (string, error) {
	switch endpoint.APIKeyStorage {
	case "", APIKeyStorageManual:
		return endpoint.APIKey, nil
	case APIKeyStorageEnv:
		apiKey := strings.TrimSpace(os.Getenv(APIKeyEnvironment))
		if apiKey == "" {
			return "", fmt.Errorf("环境变量 %s 为空", APIKeyEnvironment)
		}
		return apiKey, nil
	case APIKeyStorageSecretFile:
		path := strings.TrimSpace(endpoint.SecretFile)
		if path == "" {
			return "", fmt.Errorf("Secret 文件路径为空")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		apiKey := strings.TrimSpace(string(data))
		if apiKey == "" {
			return "", fmt.Errorf("Secret 文件为空")
		}
		return apiKey, nil
	default:
		return "", fmt.Errorf("未知存储格式 %q", endpoint.APIKeyStorage)
	}
}
