package instance

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

func TestOverrideConfigRegisteredAndInstanceIsolated(t *testing.T) {
	m := testManager(t)
	a, b := create(t, m, "override-a"), create(t, m, "override-b")
	if err := m.Enable(a.ID, true); err != nil {
		t.Fatal(err)
	}
	rt := m.instances[a.ID].runtime
	if err := rt.Scope.Config.Update("ADMIN_QQ_IDS", "fixture-admin", false); err != nil {
		t.Fatal(err)
	}
	executor, ok := rt.Engine.ToolRegistry[tools.OverrideFrostAgentConfigToolName]
	if !ok {
		t.Fatal("override tool not registered")
	}
	tool := executor.(tools.Tool)
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{SessionID: "fixture-session", InstanceID: a.ID, ActorPlatform: "onebot", ActorUserID: "fixture-admin", MemorySource: memory.GroupMessage{Content: "不要用表情包了，也关闭 ActionsCat"}})
	if _, err := tool.ExecuteContext(ctx, `{"action":"sticker_activity","value":"0","request":"不要用表情包了"}`); err != nil {
		t.Fatal(err)
	}
	if rt.Engine.Getenv("STICKER_ACTIVITY") != "0" || m.instances[a.ID].runtime != rt || rt.Scope.Context().Err() != nil {
		t.Fatal("override did not hot apply on same runtime")
	}
	if err := rt.Scope.Config.Update("STICKER_ACTIVITY", "5", false); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.ExecuteContext(ctx, `{"action":"actionscat_enabled","value":"false","request":"关闭 ActionsCat"}`); err != nil {
		t.Fatal(err)
	}
	if rt.Engine.Getenv("ACTIONSCAT_ENABLED") != "false" || m.instances[b.ID].runtime.Engine.Getenv("ACTIONSCAT_ENABLED") == "false" {
		t.Fatal("instance override crossed scope")
	}
	if err := m.Enable(a.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.ExecuteContext(ctx, `{"action":"sticker_activity","value":"0","request":"不要用表情包了"}`); err == nil {
		t.Fatal("stopped runtime retained mutation authority")
	}
}

func TestOverrideConfigSerializesWithSQLDraftRollback(t *testing.T) {
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info := create(t, m, "override-rollback")
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	rt := m.instances[info.ID].runtime
	if err := rt.Scope.Config.Update("ADMIN_QQ_IDS", "fixture-admin", false); err != nil {
		t.Fatal(err)
	}
	if err := rt.Scope.Config.Update("SYSTEM_PROMPT", "original", false); err != nil {
		t.Fatal(err)
	}
	tool := rt.Engine.ToolRegistry[tools.OverrideFrostAgentConfigToolName].(tools.Tool)
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{SessionID: "fixture-session", InstanceID: info.ID, ActorPlatform: "onebot", ActorUserID: "fixture-admin", MemorySource: memory.GroupMessage{Content: "追加 new"}})
	const args = `{"action":"append_system_prompt","value":"new","request":"追加 new"}`
	calls := 0
	m.SetGlobalApplyHook(func() error {
		calls++
		if calls == 1 {
			// This hook runs after both stores committed, while rollback is still
			// possible. The tool must wait for the complete management transaction.
			waiting, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
			if _, err := tool.ExecuteContext(waiting, args); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("tool bypassed draft lock: %v", err)
			}
			return errors.New("synthetic apply failure")
		}
		return m.ApplyGlobalSettings()
	})
	response := applySettingsDraft(m, info.ID, `{"updates":[{"key":"SYSTEM_PROMPT","value":"draft"},{"key":"MCP_CONTROL_TOKEN","value":"synthetic-draft-token"}]}`)
	if response.Code == http.StatusOK || rt.Engine.Getenv("SYSTEM_PROMPT") != "original" {
		t.Fatalf("draft did not roll back: %d %s", response.Code, response.Body.String())
	}
	if _, err := tool.ExecuteContext(ctx, args); err != nil {
		t.Fatal(err)
	}
	if got := rt.Engine.Getenv("SYSTEM_PROMPT"); got != "original\n<user_addition>\nnew\n</user_addition>" {
		t.Fatalf("rollback lost addition: %q", got)
	}
}

func TestOverrideConfigLockWaitEndsOnRuntimeStop(t *testing.T) {
	m := testManager(t)
	info := create(t, m, "override-stop")
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	rt := m.instances[info.ID].runtime
	if err := rt.Scope.Config.Update("ADMIN_QQ_IDS", "fixture-admin", false); err != nil {
		t.Fatal(err)
	}
	tool := rt.Engine.ToolRegistry[tools.OverrideFrostAgentConfigToolName].(tools.Tool)
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{SessionID: "fixture-session", InstanceID: info.ID, ActorPlatform: "onebot", ActorUserID: "fixture-admin", MemorySource: memory.GroupMessage{Content: "关闭 ActionsCat"}})
	m.settingsWriteMu.Lock()
	defer m.settingsWriteMu.Unlock()
	result := make(chan error, 1)
	if !rt.Scope.Go(func() {
		_, err := tool.ExecuteContext(ctx, `{"action":"actionscat_enabled","value":"false","request":"关闭 ActionsCat"}`)
		result <- err
	}) {
		t.Fatal("runtime rejected work")
	}
	stopped := make(chan struct{})
	go func() { rt.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stopping runtime deadlocked on the settings lock")
	}
	if err := <-result; err == nil {
		t.Fatal("stopped runtime changed settings")
	}
	if strings.EqualFold(rt.Scope.Config.Get("ACTIONSCAT_ENABLED"), "false") {
		t.Fatal("blocked call persisted after stop")
	}
}
