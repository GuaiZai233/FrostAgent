package dialogue

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"connectrpc.com/connect"
	"gopkg.in/yaml.v3"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
)

// Service implements frostagent.v1.DialogueServiceHandler.
type Service struct {
	mu       sync.RWMutex
	filePath string
	engine   *llm.Engine
}

// New creates a new DialogueService.
func New(filePath string, engine *llm.Engine) *Service {
	if filePath == "" {
		filePath = "eval/dialogue/dialogue.yml"
	}
	return &Service{
		filePath: filePath,
		engine:   engine,
	}
}

// ListDialogues returns the list of dialogue examples and the current formatted prompt preview.
func (s *Service) ListDialogues(
	ctx context.Context,
	req *connect.Request[v1.ListDialoguesRequest],
) (*connect.Response[v1.ListDialoguesResponse], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	examples, err := llm.LoadDialogueExamples(s.filePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("read dialogue file: %w", err))
	}

	dialogues := make([]*v1.DialogueItem, 0, len(examples))
	for _, ex := range examples {
		idStr := ""
		if ex.ID != nil {
			idStr = fmt.Sprintf("%v", ex.ID)
		}
		dialogues = append(dialogues, &v1.DialogueItem{
			Id:        idStr,
			Scene:     ex.Scene,
			Relation:  ex.Relation,
			User:      ex.User,
			Preferred: ex.Preferred,
		})
	}

	preview := llm.FormatDialoguePrompt(examples)
	return connect.NewResponse(&v1.ListDialoguesResponse{
		Dialogues:     dialogues,
		PromptPreview: preview,
		FilePath:      s.filePath,
	}), nil
}

// SaveDialogues saves the dialogue examples list, atomically writes the YAML file, and updates the engine prompt.
func (s *Service) SaveDialogues(
	ctx context.Context,
	req *connect.Request[v1.SaveDialoguesRequest],
) (*connect.Response[v1.SaveDialoguesResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	items := req.Msg.GetDialogues()
	examples := make([]llm.DialogueExample, 0, len(items))
	for _, item := range items {
		examples = append(examples, llm.DialogueExample{
			ID:        item.GetId(),
			Scene:     item.GetScene(),
			Relation:  item.GetRelation(),
			User:      item.GetUser(),
			Preferred: item.GetPreferred(),
		})
	}

	data, err := yaml.Marshal(examples)
	if err != nil {
		return connect.NewResponse(&v1.SaveDialoguesResponse{
			Success: false,
			Error:   fmt.Sprintf("marshal YAML: %v", err),
		}), nil
	}

	if err := s.atomicWriteFile(data); err != nil {
		return connect.NewResponse(&v1.SaveDialoguesResponse{
			Success: false,
			Error:   fmt.Sprintf("write file: %v", err),
		}), nil
	}

	prompt := llm.FormatDialoguePrompt(examples)
	if s.engine != nil {
		s.engine.DialoguePrompt = prompt
	}
	logs.Info(logs.SYSTEM, fmt.Sprintf("已更新示例对话配置 (%d 条)，同步生效至系统提示词", len(examples)))

	return connect.NewResponse(&v1.SaveDialoguesResponse{
		Success:       true,
		PromptPreview: prompt,
	}), nil
}

// GetRawDialogueFile returns the raw YAML content of the dialogue file.
func (s *Service) GetRawDialogueFile(
	ctx context.Context,
	req *connect.Request[v1.GetRawDialogueFileRequest],
) (*connect.Response[v1.GetRawDialogueFileResponse], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return connect.NewResponse(&v1.GetRawDialogueFileResponse{
				Content:  "",
				FilePath: s.filePath,
			}), nil
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("read file: %w", err))
	}

	return connect.NewResponse(&v1.GetRawDialogueFileResponse{
		Content:  string(data),
		FilePath: s.filePath,
	}), nil
}

// UpdateRawDialogueFile updates the raw YAML content after syntax validation.
func (s *Service) UpdateRawDialogueFile(
	ctx context.Context,
	req *connect.Request[v1.UpdateRawDialogueFileRequest],
) (*connect.Response[v1.UpdateRawDialogueFileResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	content := req.Msg.GetContent()
	examples, err := llm.ParseDialogueYAML([]byte(content))
	if err != nil {
		return connect.NewResponse(&v1.UpdateRawDialogueFileResponse{
			Success: false,
			Error:   fmt.Sprintf("YAML 语法解析错误: %v", err),
		}), nil
	}

	if err := s.atomicWriteFile([]byte(content)); err != nil {
		return connect.NewResponse(&v1.UpdateRawDialogueFileResponse{
			Success: false,
			Error:   fmt.Sprintf("写入文件失败: %v", err),
		}), nil
	}

	prompt := llm.FormatDialoguePrompt(examples)
	if s.engine != nil {
		s.engine.DialoguePrompt = prompt
	}
	logs.Info(logs.SYSTEM, fmt.Sprintf("已更新原始示例对话文件 (%d 条)，同步生效至系统提示词", len(examples)))

	return connect.NewResponse(&v1.UpdateRawDialogueFileResponse{
		Success:       true,
		PromptPreview: prompt,
	}), nil
}

func (s *Service) atomicWriteFile(data []byte) error {
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	tmpPath := s.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}

	if err := os.Rename(tmpPath, s.filePath); err != nil {
		// Fallback for cross-device
		if err := copyFile(tmpPath, s.filePath); err != nil {
			return fmt.Errorf("rename file: %w", err)
		}
		os.Remove(tmpPath)
	}
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
