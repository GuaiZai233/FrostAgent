package admincmd

import (
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/runtimescope"
	"path/filepath"
	"testing"
)

func newTestScope(t *testing.T, env map[string]string) *runtimescope.Scope {
	t.Helper()
	dir := t.TempDir()
	instanceStore, err := instanceconfig.Open(filepath.Join(dir, "instance.env"), false)
	if err != nil {
		t.Fatalf("打开实例配置失败: %v", err)
	}
	globalStore, err := instanceconfig.Open(filepath.Join(dir, "global.env"), true)
	if err != nil {
		t.Fatalf("打开全局配置失败: %v", err)
	}
	for k, v := range env {
		if err := instanceStore.Update(k, v, false); err != nil {
			t.Fatalf("写入配置失败: %v", err)
		}
	}
	return runtimescope.New(instanceStore, globalStore, nil)
}

func TestIsAdmin(t *testing.T) {
	// 1. Scoped environment test
	scope := newTestScope(t, map[string]string{
		AdminQQIDsEnv: "10001, 10002; 10003  10004",
	})

	if !IsAdmin("10001", scope) {
		t.Errorf("expected 10001 to be admin in scope")
	}
	if !IsAdmin("10002", scope) {
		t.Errorf("expected 10002 to be admin in scope")
	}
	if !IsAdmin("10003", scope) {
		t.Errorf("expected 10003 to be admin in scope")
	}
	if !IsAdmin("10004", scope) {
		t.Errorf("expected 10004 to be admin in scope")
	}
	if IsAdmin("99999", scope) {
		t.Errorf("expected 99999 not to be admin in scope")
	}
	if IsAdmin("", scope) {
		t.Errorf("expected empty user ID not to be admin")
	}

	// 2. Empty admin list
	emptyScope := newTestScope(t, map[string]string{
		AdminQQIDsEnv: "",
	})
	if IsAdmin("10001", emptyScope) {
		t.Errorf("expected 10001 not to be admin when ADMIN_QQ_IDS is empty")
	}

	// 3. Fallback to process os.Getenv when scope is nil
	t.Setenv(AdminQQIDsEnv, "20001,20002")
	if !IsAdmin("20001", nil) {
		t.Errorf("expected 20001 to be admin via os.Getenv")
	}
	if IsAdmin("20003", nil) {
		t.Errorf("expected 20003 not to be admin via os.Getenv")
	}
}
