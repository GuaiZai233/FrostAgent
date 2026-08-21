package llm

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultDialogueInstruction 是注入示例对话时的引导提示词。
const DefaultDialogueInstruction = "以下是示例对话，请仿照句子格式、语气等回应接下来的用户输入。"

// DialogueExample 表示 YAML 中的单条示例对话数据结构。
type DialogueExample struct {
	ID        any    `yaml:"id" json:"id"`
	Scene     string `yaml:"scene" json:"scene"`
	Relation  string `yaml:"relation" json:"relation"`
	User      string `yaml:"user" json:"user"`
	Preferred string `yaml:"preferred" json:"preferred"`
}

// ParseDialogueYAML 解析 YAML 格式的示例对话数据。
func ParseDialogueYAML(data []byte) ([]DialogueExample, error) {
	var examples []DialogueExample
	if err := yaml.Unmarshal(data, &examples); err != nil {
		return nil, err
	}
	return examples, nil
}

// LoadDialogueExamples 从指定的 YAML 文件中读取并解析示例对话列表。
func LoadDialogueExamples(path string) ([]DialogueExample, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseDialogueYAML(data)
}

// FormatDialoguePrompt 将示例对话列表格式化为带引导提示词的系统提示词片段。
func FormatDialoguePrompt(examples []DialogueExample) string {
	if len(examples) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(DefaultDialogueInstruction)

	for _, ex := range examples {
		user := strings.TrimSpace(ex.User)
		preferred := strings.TrimSpace(ex.Preferred)
		if user == "" || preferred == "" {
			continue
		}
		sb.WriteString("\n\n")
		fmt.Fprintf(&sb, "User: %s\nAssistant: %s", user, preferred)
	}

	return sb.String()
}

// LoadDialoguePrompt 从文件路径读取示例对话并返回格式化后的系统提示词片段。
func LoadDialoguePrompt(path string) (string, error) {
	examples, err := LoadDialogueExamples(path)
	if err != nil {
		return "", err
	}
	return FormatDialoguePrompt(examples), nil
}
