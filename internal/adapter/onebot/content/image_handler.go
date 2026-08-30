package content

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func IsContainImage(segments []MessageSegment) bool {
	for _, seg := range segments {
		if seg.Type == "image" {
			return true
		}
	}
	return false
}

func ProcessImage(ctx context.Context, segments []MessageSegment, provider core.LLMProvider, route core.RouteContext) string {
	var userTexts []string
	var imageBase64List []string

	// dispatch text and image
	for _, seg := range segments {
		if seg.Type == "text" {
			if text, ok := seg.Data["text"].(string); ok {
				userTexts = append(userTexts, text)
			}
		} else if seg.Type == "image" {
			if b64, ok := seg.Data["base64"].(string); ok && strings.TrimSpace(b64) != "" {
				imageBase64List = append(imageBase64List, strings.TrimSpace(b64))
				continue
			}
			source, _ := seg.Data["url"].(string)
			if strings.TrimSpace(source) == "" {
				if file, ok := seg.Data["file"].(string); ok &&
					(strings.HasPrefix(file, "base64://") || strings.HasPrefix(file, "data:")) {
					source = file
				}
			}
			if strings.TrimSpace(source) == "" {
				logs.Warn(logs.WEBSOCKET, fmt.Sprintf("图片消息缺少可读取的数据: %+v", seg.Data))
				continue
			}
			if b64, err := imageSourceToBase64(source); err == nil {
				imageBase64List = append(imageBase64List, b64)
			} else {
				logs.Error(logs.WEBSOCKET, fmt.Sprintf("下载图片失败: %v", err))
			}
		}
	}

	combinedText := strings.Join(userTexts, "")
	// eg: call Qwen-VL
	if len(imageBase64List) > 0 {
		contentBlocks := []ContentBlock{
			{Type: "text", Text: combinedText},
		}

		for _, b64 := range imageBase64List {
			contentBlocks = append(contentBlocks, ContentBlock{
				Type:     "image_url",
				ImageURL: map[string]string{"url": "data:image/jpeg;base64," + b64},
			})
		}
		jsonBytes, err := json.Marshal(contentBlocks)
		if err != nil {
			logs.Error(logs.WEBSOCKET, fmt.Sprintf("序列化消息失败: %v", err))
			return "无法读取图片"
		}
		return llm.CallVisionModel(ctx, provider, route, string(jsonBytes))
	}
	return combinedText
}

func downloadAndToBase64(url string) (string, error) {
	return imageSourceToBase64(url)
}

func imageSourceToBase64(source string) (string, error) {
	if strings.HasPrefix(source, "base64://") {
		encoded := strings.TrimPrefix(source, "base64://")
		if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
			return "", err
		}
		return encoded, nil
	}
	if strings.HasPrefix(source, "data:") {
		comma := strings.IndexByte(source, ',')
		if comma < 0 || !strings.Contains(strings.ToLower(source[:comma]), ";base64") {
			return "", fmt.Errorf("图片 data URI 不是 Base64")
		}
		encoded := source[comma+1:]
		if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
			return "", err
		}
		return encoded, nil
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Get(source)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logs.Warn(logs.WEBSOCKET, fmt.Sprintf("关闭图片响应体失败: %v", err))
		}
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("图片下载失败，状态码: %d", resp.StatusCode)
	}

	imgBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(imgBytes), nil
}
