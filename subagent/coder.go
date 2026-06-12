package subagent

import (
	"FrostAgent/internal/llm"
	"encoding/json"
	"log"
	"os"
)

const CoderPrompt = "你是编程助手。"

func CallCoder(client *llm.Client, baseURL, apiKey, _, contentBlocks string) string {
	// 这边先硬编码，到时候我回来改
	messages := []llm.ChatMessage{
		{Role: "system", Content: CoderPrompt},
		{Role: "user", Content: contentBlocks},
	}
	responseMsg, err := client.CallAPI("https://dashscope.aliyuncs.com/compatible-mode/v1", os.Getenv("CODER_API_KEY"), "qwen-coder-plus", messages, nil)
	if err != nil {
		log.Printf("%s", err.Error())
		return err.Error()
	}

	bytes, _ := json.Marshal(responseMsg.Content)
	return string(bytes)
}
