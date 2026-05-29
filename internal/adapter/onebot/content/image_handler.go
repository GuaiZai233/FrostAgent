package content

import (
	"FrostAgent/internal/llm"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
)

func IsContainImage(segments []MessageSegment) bool {
	for _, seg := range segments {
		if seg.Type == "image" {
			return true
		}
	}
	return false
}

func ProcessImage(segments []MessageSegment, client *llm.Client, baseURL, apiKey, modelName string) string {
	var userTexts []string
	var imageBase64List []string

	//dispatch text and image
	for _, seg := range segments {
		if seg.Type == "text" {
			userTexts = append(userTexts, seg.Data["text"].(string))
		} else if seg.Type == "image" {
			url := seg.Data["url"].(string)
			//convert img to base64
			if b64, err := downloadAndToBase64(url); err == nil {
				imageBase64List = append(imageBase64List, b64)
			}
		}
	}

	combinedText := strings.Join(userTexts, "")
	//eg: call Qwen-VL
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
		return llm.CallVisionModel(client, baseURL, apiKey, modelName, string(jsonBytes))
	}
	return combinedText
}

func downloadAndToBase64(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	imgBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(imgBytes), nil
}
