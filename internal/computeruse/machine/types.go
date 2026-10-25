package machine

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
)

var ErrInvalid = errors.New("invalid machine operation")
var ErrBusy = errors.New("machine resource busy")
var ErrPaused = errors.New("machine task paused")
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

type Project struct {
	ID          string            `json:"id"`
	Path        string            `json:"path"`
	Remote      string            `json:"remote"`
	Baseline    string            `json:"baseline"`
	Shell       string            `json:"shell"`
	Environment map[string]string `json:"environment,omitempty"`
	Archived    bool              `json:"archived"`
}

type Workspace struct {
	TaskID      string   `json:"task_id"`
	ProjectID   string   `json:"project_id"`
	Path        string   `json:"path"`
	Branch      string   `json:"branch"`
	Shell       string   `json:"shell"`
	Environment []string `json:"-"`
	Generation  uint64   `json:"generation"`
	Paused      bool     `json:"paused"`
}

type Params struct {
	Project   *Project `json:"project,omitempty"`
	ProjectID string   `json:"project_id,omitempty"`
	Command   string   `json:"command,omitempty"`
	Path      string   `json:"path,omitempty"`
	Text      string   `json:"text,omitempty"`
	Input     []Input  `json:"input,omitempty"`
	Archived  bool     `json:"archived,omitempty"`
}

type Input struct {
	Type   string `json:"type"`
	X      int    `json:"x,omitempty"`
	Y      int    `json:"y,omitempty"`
	Button string `json:"button,omitempty"`
	Key    uint16 `json:"key,omitempty"`
	Text   string `json:"text,omitempty"`
	Delta  int    `json:"delta,omitempty"`
}

type Desktop interface {
	Screenshot(context.Context) ([]byte, error)
	Input(context.Context, []Input) error
}

func params(raw json.RawMessage) (Params, error) {
	var p Params
	if json.Unmarshal(raw, &p) != nil {
		return p, ErrInvalid
	}
	return p, nil
}

func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && (len(rel) < 3 || rel[:3] != ".."+string(filepath.Separator))
}
