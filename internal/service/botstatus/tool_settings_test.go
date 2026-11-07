package botstatus

import (
	"context"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
)

type settingsTestTool string

func (t settingsTestTool) Name() string                 { return string(t) }
func (settingsTestTool) Description() string            { return "Synthetic tool" }
func (settingsTestTool) Parameters() map[string]any     { return nil }
func (settingsTestTool) Execute(string) (string, error) { return "", nil }

func TestSetToolEnabledConcurrentUpdates(t *testing.T) {
	config, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	scope := runtimescope.New(config, nil, nil)
	defer scope.Cancel()
	names := []string{"fixture-a", "fixture-b", "fixture-c"}
	engine := &llm.Engine{Scope: scope, ToolRegistry: map[string]llm.ToolExecutor{}}
	for _, name := range names {
		engine.ToolRegistry[name] = settingsTestTool(name)
	}
	service := New(engine, "test", "")
	results := make(chan error, len(names))
	for _, name := range names {
		go func() {
			_, err := service.SetToolEnabled(context.Background(), connect.NewRequest(&v1.SetToolEnabledRequest{Name: name, Enabled: false}))
			results <- err
		}()
	}
	for range names {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range names {
		if engine.ToolEnabled(name) {
			t.Errorf("concurrent update lost switch for %s", name)
		}
	}
}
