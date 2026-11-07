package dialogue

import (
	"context"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"os"
	"path/filepath"
	"sync"

	"connectrpc.com/connect"
	"gopkg.in/yaml.v3"

	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
)

// Option configures DialogueService.
type Option func(*Service)

// WithLogger sets the logger for DialogueService.
func WithLogger(logger *logs.Store) Option {
	return func(s *Service) {
		s.logger = logger
	}
}

// Service implements frostagent.v1.DialogueServiceHandler.
type Service struct {
	mu         sync.RWMutex
	filePath   string
	engine     *llm.Engine
	logger     *logs.Store
	db         *storage.DB
	instanceID string
}

func NewSQL(db *storage.DB, instanceID string, engine *llm.Engine, opts ...Option) *Service {
	s := &Service{db: db, instanceID: instanceID, engine: engine}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// New creates a new DialogueService.
func New(filePath string, engine *llm.Engine, opts ...Option) *Service {
	if filePath == "" {
		filePath = "eval/dialogue/dialogue.yml"
	}
	s := &Service{
		filePath: filePath,
		engine:   engine,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Service) log() *logs.Store {
	if s.logger != nil {
		return s.logger
	}
	if s.engine != nil && s.engine.Scope != nil && s.engine.Log() != nil {
		return s.engine.Log()
	}
	return logs.General
}

// ListDialogues returns the list of dialogue examples and the current formatted prompt preview.
func (s *Service) ListDialogues(
	ctx context.Context,
	req *connect.Request[v1.ListDialoguesRequest],
) (*connect.Response[v1.ListDialoguesResponse], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var examples []llm.DialogueExample
	var err error
	if s.db != nil {
		examples, err = LoadExamplesSQL(s.db, s.instanceID)
	} else {
		examples, err = llm.LoadDialogueExamples(s.filePath)
	}
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

// SaveDialogues atomically saves dialogue examples and updates the engine prompt.
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
			User:      item.GetUser(),
			Preferred: item.GetPreferred(),
		})
	}

	var saveErr error
	if s.db != nil {
		saveErr = SaveExamplesSQL(s.db, s.instanceID, examples)
	} else {
		data, err := yaml.Marshal(examples)
		if err != nil {
			return connect.NewResponse(&v1.SaveDialoguesResponse{Success: false, Error: fmt.Sprintf("marshal YAML: %v", err)}), nil
		}
		saveErr = s.atomicWriteFile(data)
	}
	if saveErr != nil {
		return connect.NewResponse(&v1.SaveDialoguesResponse{
			Success: false,
			Error:   fmt.Sprintf("保存示例对话失败: %v", saveErr),
		}), nil
	}

	prompt := llm.FormatDialoguePrompt(examples)
	if s.engine != nil {
		s.engine.SetDialoguePrompt(prompt)
	}
	s.log().Info(logs.SYSTEM, fmt.Sprintf("已更新示例对话配置 (%d 条)，同步生效至系统提示词", len(examples)))

	return connect.NewResponse(&v1.SaveDialoguesResponse{
		Success:       true,
		PromptPreview: prompt,
	}), nil
}

// GetRawDialogueFile exports the stored dialogue examples as YAML.
func (s *Service) GetRawDialogueFile(
	ctx context.Context,
	req *connect.Request[v1.GetRawDialogueFileRequest],
) (*connect.Response[v1.GetRawDialogueFileResponse], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db != nil {
		examples, err := LoadExamplesSQL(s.db, s.instanceID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("read dialogue examples: %w", err))
		}
		data, err := yaml.Marshal(examples)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("marshal dialogue YAML: %w", err))
		}
		return connect.NewResponse(&v1.GetRawDialogueFileResponse{Content: string(data)}), nil
	}

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

// UpdateRawDialogueFile imports YAML into the configured store after parsing it.
func (s *Service) UpdateRawDialogueFile(
	ctx context.Context,
	req *connect.Request[v1.UpdateRawDialogueFileRequest],
) (*connect.Response[v1.UpdateRawDialogueFileResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	content := req.Msg.GetContent()
	if s.db != nil {
		var document yaml.Node
		if err := yaml.Unmarshal([]byte(content), &document); err != nil {
			return connect.NewResponse(&v1.UpdateRawDialogueFileResponse{
				Error: fmt.Sprintf("YAML 语法解析错误: %v", err),
			}), nil
		}
		if len(document.Content) != 1 || document.Content[0].Kind != yaml.SequenceNode {
			return connect.NewResponse(&v1.UpdateRawDialogueFileResponse{
				Error: "YAML 必须是对话示例列表；如需清空，请显式填写 []",
			}), nil
		}
	}
	examples, err := llm.ParseDialogueYAML([]byte(content))
	if err != nil {
		return connect.NewResponse(&v1.UpdateRawDialogueFileResponse{
			Success: false,
			Error:   fmt.Sprintf("YAML 语法解析错误: %v", err),
		}), nil
	}

	var saveErr error
	if s.db != nil {
		saveErr = SaveExamplesSQL(s.db, s.instanceID, examples)
	} else {
		saveErr = s.atomicWriteFile([]byte(content))
	}
	if saveErr != nil {
		return connect.NewResponse(&v1.UpdateRawDialogueFileResponse{
			Success: false,
			Error:   fmt.Sprintf("保存示例对话失败: %v", saveErr),
		}), nil
	}

	prompt := llm.FormatDialoguePrompt(examples)
	if s.engine != nil {
		s.engine.SetDialoguePrompt(prompt)
	}
	s.log().Info(logs.SYSTEM, fmt.Sprintf("已导入示例对话 YAML (%d 条)，同步生效至系统提示词", len(examples)))

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

// Prompt provides the persona snapshot under the same lock as file edits.
func (s *Service) Prompt() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.engine != nil {
		return s.engine.PersonaDialogue()
	}
	if s.db != nil {
		p, _ := LoadPromptSQL(s.db, s.instanceID)
		return p
	}
	p, _ := llm.LoadDialoguePrompt(s.filePath)
	return p
}
