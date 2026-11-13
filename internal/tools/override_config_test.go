package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/botloop"
	"github.com/GuaiZai233/FrostAgent/internal/envinject"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

const overrideRequest = "请修改配置"

func overrideFixture(t *testing.T, sql bool) (*llm.Engine, llm.RunContext, func() *instanceconfig.Store) {
	t.Helper()
	var store *instanceconfig.Store
	var reopen func() *instanceconfig.Store
	if sql {
		t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
		t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "override.db"))
		db, err := storage.Open(context.Background(), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := db.CreateInstance(context.Background(), storage.InstanceRecord{ID: "fixture-instance", Name: "Fixture", CreatedAt: time.Now()}, 2); err != nil {
			t.Fatal(err)
		}
		reopen = func() *instanceconfig.Store {
			s, err := instanceconfig.OpenDatabase(db, "fixture-instance", false)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}
	} else {
		path := filepath.Join(t.TempDir(), ".env")
		reopen = func() *instanceconfig.Store {
			s, err := instanceconfig.Open(path, false)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}
	}
	store = reopen()
	for key, value := range map[string]string{"ADMIN_QQ_IDS": "fixture-admin", "SYSTEM_PROMPT": "你是一个乐于助人的助手。", "BOT_LOOP_USER_IDS": "old-bot"} {
		if err := store.Update(key, value, false); err != nil {
			t.Fatal(err)
		}
	}
	scope := runtimescope.New(store, nil, nil)
	t.Cleanup(scope.Cancel)
	engine := &llm.Engine{Scope: scope, InstanceID: "fixture-instance"}
	run := llm.RunContext{SessionID: "fixture-session", InstanceID: engine.InstanceID, ActorPlatform: "onebot", ActorUserID: "fixture-admin", MemorySource: memory.GroupMessage{Content: overrideRequest}}
	return engine, run, reopen
}

func overrideArgs(action, value string) string {
	args, _ := json.Marshal(map[string]string{"action": action, "value": value, "request": overrideRequest})
	return string(args)
}

type overrideTestWriteLock struct {
	acquire func(context.Context) error
	release func()
}

func (l overrideTestWriteLock) LockContext(ctx context.Context) error { return l.acquire(ctx) }
func (l overrideTestWriteLock) Unlock()                               { l.release() }

func TestOverrideConfigAuthorization(t *testing.T) {
	engine, valid, _ := overrideFixture(t, false)
	lockCalls := 0
	tool := OverrideFrostAgentConfigTool(engine, overrideTestWriteLock{
		acquire: func(context.Context) error { lockCalls++; return nil },
		release: func() {},
	})
	if tool.IsProactiveAllowed() {
		t.Fatal("mutation tool allowed in proactive turns")
	}
	for _, tc := range []struct {
		name string
		edit func(*llm.RunContext)
	}{
		{"non-admin", func(r *llm.RunContext) { r.ActorUserID = "fixture-other" }},
		{"missing-platform", func(r *llm.RunContext) { r.ActorPlatform = "" }},
		{"other-platform-collision", func(r *llm.RunContext) { r.ActorPlatform = "telegram" }},
		{"mock", func(r *llm.RunContext) { r.Mock = true }},
		{"proactive", func(r *llm.RunContext) { r.Proactive = true }},
		{"awareness", func(r *llm.RunContext) { r.Awareness = true }},
		{"other-instance", func(r *llm.RunContext) { r.InstanceID = "other-instance" }},
		{"missing-instance", func(r *llm.RunContext) { r.InstanceID = "" }},
		{"missing-session", func(r *llm.RunContext) { r.SessionID = "" }},
		{"no-current-request", func(r *llm.RunContext) { r.MemorySource.Content = "你好" }},
		{"image-request", func(r *llm.RunContext) { r.MemorySource = memory.GroupMessage{ImageDescription: overrideRequest} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := valid
			tc.edit(&run)
			before := engine.Scope.Config.Snapshot()
			_, err := tool.ExecuteContext(llm.WithRunContext(context.Background(), run), overrideArgs("actionscat_enabled", "false"))
			if err == nil {
				t.Fatal("unauthorized mutation accepted")
			}
			if fmt.Sprint(before) != fmt.Sprint(engine.Scope.Config.Snapshot()) {
				t.Fatal("unauthorized call changed storage")
			}
		})
	}
	if _, err := tool.Execute(overrideArgs("actionscat_enabled", "false")); err == nil {
		t.Fatal("missing context accepted")
	}
	ctx, cancel := context.WithCancel(llm.WithRunContext(context.Background(), valid))
	cancel()
	if _, err := tool.ExecuteContext(ctx, overrideArgs("actionscat_enabled", "false")); err == nil {
		t.Fatal("canceled request accepted")
	}
	if lockCalls != 0 {
		t.Fatalf("unauthorized calls attempted shared lock %d times", lockCalls)
	}
}

func TestOverrideConfigRechecksAuthorizationAfterLock(t *testing.T) {
	for _, name := range []string{"admin-removed", "access-locked", "request-canceled", "runtime-stopped", "turn-interrupted"} {
		t.Run(name, func(t *testing.T) {
			engine, run, _ := overrideFixture(t, false)
			engine.Security = security.NewController(t.TempDir())
			session := new(llm.SessionContext)
			run.Turn = session.ReserveSequenceTurn("fixture-lane", false, false)
			ctx, cancel := context.WithTimeout(llm.WithRunContext(context.Background(), run), 5*time.Second)
			defer cancel()
			waiting, release := make(chan struct{}), make(chan struct{})
			unlocked := false
			tool := OverrideFrostAgentConfigTool(engine, overrideTestWriteLock{
				acquire: func(ctx context.Context) error {
					close(waiting)
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				},
				release: func() { unlocked = true },
			})
			result := make(chan error, 1)
			go func() {
				_, err := tool.ExecuteContext(ctx, overrideArgs("actionscat_enabled", "false"))
				result <- err
			}()
			select {
			case <-waiting:
			case <-ctx.Done():
				t.Fatal("authorized call did not attempt the lock")
			}
			switch name {
			case "admin-removed":
				if err := engine.Scope.Config.Update("ADMIN_QQ_IDS", "fixture-other-admin", false); err != nil {
					t.Fatal(err)
				}
			case "access-locked":
				principal, err := security.NewPrincipal(run.ActorPlatform, run.ActorUserID)
				if err != nil {
					t.Fatal(err)
				}
				if err := engine.Security.Lock(principal, "synthetic revocation"); err != nil {
					t.Fatal(err)
				}
			case "request-canceled":
				cancel()
			case "runtime-stopped":
				engine.Scope.Cancel()
			case "turn-interrupted":
				session.ReserveSequenceTurn("fixture-lane", true, false)
			}
			close(release)
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("revoked caller changed config after waiting for the lock")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("revoked caller did not finish")
			}
			if name != "request-canceled" && name != "runtime-stopped" && !unlocked {
				t.Fatal("rejected caller retained the acquired lock")
			}
			if engine.Scope.Config.Get("ACTIONSCAT_ENABLED") == "false" {
				t.Fatal("revoked caller persisted config")
			}
		})
	}
}

func TestOverrideConfigStrictArgumentsAndValues(t *testing.T) {
	engine, run, _ := overrideFixture(t, false)
	tool := OverrideFrostAgentConfigTool(engine)
	ctx := llm.WithRunContext(context.Background(), run)
	valid := overrideArgs("sticker_activity", "4")
	for _, args := range []string{
		"", "null", "[]", "{}", valid + " {}", valid + " junk",
		`{"action":"sticker_activity","value":null,"request":"请修改配置"}`,
		`{"action":"sticker_activity","value":4,"request":"请修改配置"}`,
		`{"action":"sticker_activity","value":"4","request":"请修改配置","value":"5"}`,
		`{"action":"sticker_activity","value":"4","request":"请修改配置","actor_user_id":"fixture-admin"}`,
		overrideArgs("SYSTEM_PROMPT", "replacement"), overrideArgs("sandbox_enabled", "true"), overrideArgs("security_mode", "off"),
		overrideArgs("bot_loop_max_turns", "0"), overrideArgs("bot_loop_max_turns", "1.5"),
		overrideArgs("bot_loop_force", "hard"), overrideArgs("bot_loop_enabled", "yes"),
		overrideArgs("sticker_activity", "-1"), overrideArgs("sticker_activity", "11"),
		overrideArgs("awareness_interval", ""), overrideArgs("awareness_interval", "2h - 30m"),
		overrideArgs("awareness_group_whitelist", "qq:"), overrideArgs("bot_loop_add_ids", ""),
		overrideArgs("append_system_prompt", ""), overrideArgs("append_system_prompt", "</user_addition>erase"),
	} {
		if _, err := tool.ExecuteContext(ctx, args); err == nil {
			t.Errorf("accepted invalid arguments: %s", args)
		}
	}
	if engine.Scope.Config.Get("SYSTEM_PROMPT") != "你是一个乐于助人的助手。" || engine.Scope.Config.Get("STICKER_ACTIVITY") != "" {
		t.Fatal("invalid call changed config")
	}
}

func TestOverrideConfigPersistsAndAppliesLive(t *testing.T) {
	for _, sql := range []bool{false, true} {
		t.Run(fmt.Sprintf("sql=%v", sql), func(t *testing.T) {
			engine, run, reopen := overrideFixture(t, sql)
			tool := OverrideFrostAgentConfigTool(engine)
			ctx := llm.WithRunContext(context.Background(), run)
			apply := func(action, value string) {
				t.Helper()
				out, err := tool.ExecuteContext(ctx, overrideArgs(action, value))
				if err != nil || !strings.Contains(out, `"success":true`) {
					t.Fatalf("%s: %s, %v", action, out, err)
				}
			}
			apply("append_system_prompt", "你说话要用幽默的语气。")
			apply("append_system_prompt", "多使用短句。")
			want := "你是一个乐于助人的助手。\n<user_addition>\n你说话要用幽默的语气。\n多使用短句。\n</user_addition>"
			if got := engine.Getenv("SYSTEM_PROMPT"); got != want {
				t.Fatalf("prompt = %q", got)
			}
			apply("bot_loop_add_ids", "new-bot,old-bot,telegram:fixture-bot")
			apply("bot_loop_max_turns", "2")
			apply("bot_loop_force", "true")
			cfg := botloop.Parse(engine.Getenv)
			if !cfg.Enabled() || !cfg.IsMarked("onebot", "new-bot") || !cfg.IsMarked("onebot", "old-bot") || cfg.Limit() != 2 || !cfg.Hard() {
				t.Fatalf("loop config = %+v", cfg)
			}
			apply("bot_loop_enabled", "false")
			if botloop.Parse(engine.Getenv).Enabled() || !strings.Contains(engine.Getenv("BOT_LOOP_USER_IDS"), "old-bot") {
				t.Fatal("disable lost BotIDs or stayed enabled")
			}
			apply("bot_loop_enabled", "true")
			if !botloop.Parse(engine.Getenv).IsMarked("onebot", "old-bot") {
				t.Fatal("enable did not restore marked Bot")
			}
			apply("bot_loop_force", "false")
			if botloop.Parse(engine.Getenv).Hard() {
				t.Fatal("force=false did not select soft mode")
			}
			apply("awareness_enabled", "true")
			apply("awareness_interval", "10m - 1h")
			apply("awareness_private_whitelist", "fixture-user")
			apply("awareness_private_whitelist_enabled", "true")
			apply("awareness_group_whitelist", "fixture-group")
			apply("awareness_group_whitelist_enabled", "true")
			for _, kind := range []string{"group", "private"} {
				id := "fixture-group"
				if kind == "private" {
					id = "fixture-user"
				}
				if !proactive.AwarenessAllowed(engine.Getenv, "onebot", kind, id) || proactive.AwarenessAllowed(engine.Getenv, "onebot", kind, "other") {
					t.Fatal("awareness whitelist not applied")
				}
			}
			apply("awareness_group_whitelist", "")
			if proactive.AwarenessAllowed(engine.Getenv, "onebot", "group", "fixture-group") {
				t.Fatal("empty enabled whitelist admitted group")
			}
			apply("actionscat_enabled", "false")
			apply("sticker_activity", "0")
			persisted := reopen()
			for key, expected := range engine.Scope.Config.Snapshot() {
				if got := persisted.Get(key); got != expected {
					t.Errorf("persisted %s = %q, want %q", key, got, expected)
				}
			}
		})
	}
}

func TestAppendSystemPromptBoundaries(t *testing.T) {
	for _, original := range []string{"base", "base\n", ""} {
		first, err := appendSystemPrompt(original, "first")
		if err != nil || !strings.HasPrefix(first, original) || !strings.HasSuffix(first, "<user_addition>\nfirst\n</user_addition>") {
			t.Fatalf("original was changed: %q, %v", first, err)
		}
		second, err := appendSystemPrompt(first, "second")
		if err != nil || !strings.HasPrefix(second, original) || !strings.HasSuffix(second, "<user_addition>\nfirst\nsecond\n</user_addition>") {
			t.Fatalf("later addition did not reuse valid block: %q, %v", second, err)
		}
	}
	original := "base\n<user_addition>\nold\n</user_addition>\nsuffix"
	got, _ := appendSystemPrompt(original, "new")
	if got != "base\n<user_addition>\nold\nnew\n</user_addition>\nsuffix" {
		t.Fatalf("valid block/suffix changed: %q", got)
	}
	original = "base\n<user_addition>old</user_addition>\n<user_addition>last</user_addition>\nsuffix"
	got, _ = appendSystemPrompt(original, "new")
	if got != "base\n<user_addition>old</user_addition>\n<user_addition>last\nnew\n</user_addition>\nsuffix" {
		t.Fatalf("last valid block/suffix changed: %q", got)
	}
}

func TestAppendSystemPromptMalformedBoundaries(t *testing.T) {
	for _, original := range []string{
		"base\n<user_addition>old", "base\n</user_addition>old</user_addition>",
		"base\n<user_addition attr='bad'>old</user_addition>", "base\n</user_addition><user_addition>",
		"base\n<user_addition>old</user_addition", "base\n<user_addition>outer<user_addition>inner</user_addition></user_addition>",
		"base\n<user_addition>outer\n<user_addition>inner</user_addition>",
		"base\n<user_addition>\nouter\n<user_addition>\ninner\n</user_addition>",
		"base\n<user_addition>outer\n<user_addition>inner</user_addition></user_addition>",
		"base\n</user_addition>\n<user_addition>valid</user_addition>",
		"base\n<user_addition>valid</user_addition>\n<user_addition",
		"base <user_addition>inline</user_addition>",
	} {
		t.Run(original, func(t *testing.T) {
			first, err := appendSystemPrompt(original, "first")
			if err != nil || !strings.HasPrefix(first, original) || !strings.HasSuffix(first, "<user_addition>\nfirst\n</user_addition>") {
				t.Fatalf("malformed original was changed: %q, %v", first, err)
			}
			second, err := appendSystemPrompt(first, "second")
			if err != nil || second != first+"\n<user_addition>\nsecond\n</user_addition>" {
				t.Fatalf("malformed base allowed block reuse: %q, %v", second, err)
			}
		})
	}
}

func TestOverrideConfigConcurrentAppends(t *testing.T) {
	for _, sql := range []bool{false, true} {
		t.Run(fmt.Sprintf("sql=%v", sql), func(t *testing.T) {
			engine, run, reopen := overrideFixture(t, sql)
			tool := OverrideFrostAgentConfigTool(engine)
			ctx := llm.WithRunContext(context.Background(), run)
			var wg sync.WaitGroup
			for i := 0; i < 12; i++ {
				wg.Go(func() {
					if _, err := tool.ExecuteContext(ctx, overrideArgs("append_system_prompt", fmt.Sprintf("addition-%02d", i))); err != nil {
						t.Error(err)
					}
					if _, err := tool.ExecuteContext(ctx, overrideArgs("bot_loop_add_ids", fmt.Sprintf("bot-%02d", i))); err != nil {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			persisted := reopen()
			for i := 0; i < 12; i++ {
				if strings.Count(persisted.Get("SYSTEM_PROMPT"), fmt.Sprintf("addition-%02d", i)) != 1 || !strings.Contains(persisted.Get("BOT_LOOP_USER_IDS"), fmt.Sprintf("bot-%02d", i)) {
					t.Fatalf("concurrent addition %d lost", i)
				}
			}
		})
	}
}

func TestOverrideConfigLegacyBotIDSeparators(t *testing.T) {
	for _, sql := range []bool{false, true} {
		t.Run(fmt.Sprintf("sql=%v", sql), func(t *testing.T) {
			engine, run, reopen := overrideFixture(t, sql)
			store := engine.Scope.Config
			if sql {
				if err := store.Update("BOT_LOOP_USER_IDS", "old-a\nold-b;Telegram:fixture-bot", false); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := store.Replace("ADMIN_QQ_IDS=fixture-admin\nBOT_LOOP_USER_IDS=\"old-a\\nold-b;Telegram:fixture-bot\"\n"); err != nil {
					t.Fatal(err)
				}
			}
			_, err := OverrideFrostAgentConfigTool(engine).ExecuteContext(llm.WithRunContext(context.Background(), run), overrideArgs("bot_loop_add_ids", "new-bot"))
			if err != nil {
				t.Fatal(err)
			}
			got := reopen().Get("BOT_LOOP_USER_IDS")
			if got != "old-a,old-b,Telegram:fixture-bot,new-bot" {
				t.Fatalf("legacy IDs lost: %q", got)
			}
		})
	}
}

func TestOverrideConfigPreservesInjectedAppendBases(t *testing.T) {
	previous := envinject.Suspend()
	t.Cleanup(func() {
		envinject.Suspend()
		if err := envinject.Restore(previous); err != nil {
			t.Error(err)
		}
	})
	for _, key := range []string{"SYSTEM_PROMPT", "BOT_LOOP_USER_IDS"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	engine, run, reopen := overrideFixture(t, true)
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for key, value := range map[string]string{"SYSTEM_PROMPT": "injected original prompt", "BOT_LOOP_USER_IDS": "injected-bot"} {
		if err := envinject.Put(context.Background(), db, key, value); err != nil {
			t.Fatal(err)
		}
		if err := engine.Scope.Config.Update(key, "", false); err != nil {
			t.Fatal(err)
		}
	}
	if err := envinject.ApplyAtStartup(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if engine.Getenv("SYSTEM_PROMPT") != "injected original prompt" || engine.Getenv("BOT_LOOP_USER_IDS") != "injected-bot" {
		t.Fatal("injected fixture not active")
	}
	tool := OverrideFrostAgentConfigTool(engine)
	ctx := llm.WithRunContext(context.Background(), run)
	for action, value := range map[string]string{"append_system_prompt": "addition", "bot_loop_add_ids": "new-bot"} {
		if _, err := tool.ExecuteContext(ctx, overrideArgs(action, value)); err != nil {
			t.Fatal(err)
		}
	}
	got := reopen()
	if got.Get("SYSTEM_PROMPT") != "injected original prompt\n<user_addition>\naddition\n</user_addition>" || got.Get("BOT_LOOP_USER_IDS") != "injected-bot,new-bot" {
		t.Fatal("append discarded injected existing content")
	}
}

func TestOverrideConfigStorageFailureDoesNotReportSuccess(t *testing.T) {
	engine, run, _ := overrideFixture(t, false)
	parent := filepath.Join(t.TempDir(), "parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, ".env")
	store, err := instanceconfig.Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update("ADMIN_QQ_IDS", run.ActorUserID, false); err != nil {
		t.Fatal(err)
	}
	engine.Scope.Config = store
	// Break only this test's temporary backing directory after loading the cache.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(parent); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parent, nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = OverrideFrostAgentConfigTool(engine).ExecuteContext(llm.WithRunContext(context.Background(), run), overrideArgs("sticker_activity", "4"))
	if err == nil {
		t.Fatal("unavailable config accepted mutation")
	}
	if store.Get("STICKER_ACTIVITY") != "" {
		t.Fatal("failed persistence changed live cache")
	}
}
