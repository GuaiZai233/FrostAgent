package content

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

func ProcessImage(segments []MessageSegment, provider core.LLMProvider, baseURL, apiKey, modelName string) string {
	var userTexts []string
	var imageBase64List []string

	// dispatch text and image
	for _, seg := range segments {
		if seg.Type == "text" {
			if text, ok := seg.Data["text"].(string); ok {
				userTexts = append(userTexts, text)
			}
		} else if seg.Type == "image" {
			url, ok := seg.Data["url"].(string)
			if !ok || strings.TrimSpace(url) == "" {
				log.Printf("图片消息缺少 url 字段: %+v", seg.Data)
				continue
			}
			// convert img to base64
			if b64, err := downloadAndToBase64(url); err == nil {
				imageBase64List = append(imageBase64List, b64)
			} else {
				log.Printf("下载图片失败: %v", err)
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
			log.Printf("序列化消息失败: %v\n", err)
			return "无法读取图片"
		}
		return llm.CallVisionModel(provider, baseURL, apiKey, modelName, string(jsonBytes))
	}
	return combinedText
}

func downloadAndToBase64(url string) (string, error) {
	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("关闭图片响应体失败: %v\n", err)
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
