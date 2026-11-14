package admincmd

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
)

func TestLogCommandPermissionsIsolationAndPrivateDelivery(t *testing.T) {
	config, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Update(AdminQQIDsEnv, "synthetic-admin", false); err != nil {
		t.Fatal(err)
	}
	store := logs.New("test", "test", 20)
	exec := NewExecutor(&llm.Engine{Scope: runtimescope.New(config, nil, store)})
	id := store.Error(logs.LLM_RESPONSE, "sensitive https://endpoint.invalid/secret")
	cmd, candidate, err := ParseCandidate("/log "+strings.ToUpper(id), "/")
	if !candidate || err != nil || cmd.Args[0] != id {
		t.Fatalf("parse: %+v %v", cmd, err)
	}
	for _, text := range []string{"/log", " /LOG \t"} {
		parsed, candidate, err := ParseCandidate(text, "/")
		if !candidate || err != nil || parsed.Type != CmdLog || len(parsed.Args) != 0 {
			t.Fatalf("bare log parse: %+v %v", parsed, err)
		}
	}
	for _, bad := range []string{"/log invalid", "/log " + id + " extra"} {
		if _, candidate, err := ParseCandidate(bad, "/"); !candidate || err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	for _, tc := range []struct {
		name                           string
		group, fail, nonadmin, missing bool
	}{
		{name: "group", group: true}, {name: "private"}, {name: "failed-private", group: true, fail: true},
		{name: "nonadmin", group: true, nonadmin: true}, {name: "missing", group: true, missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var replies, private []string
			ctx := CommandContext{CallerUserID: "synthetic-admin", IsGroup: tc.group,
				Reply: func(_ context.Context, text string, _ bool) error { replies = append(replies, text); return nil },
				ReplyPrivate: func(_ context.Context, text string) error {
					private = append(private, text)
					if tc.fail {
						return errors.New("synthetic delivery failure")
					}
					return nil
				},
			}
			if tc.nonadmin {
				ctx.CallerUserID = "synthetic-other"
			}
			if tc.missing {
				other := logs.New("other", "other", 2)
				cmd.Args = []string{other.Error(logs.SYSTEM, "foreign")}
			} else {
				cmd.Args = []string{id}
			}
			err := exec.Execute(context.Background(), ctx, cmd)
			if tc.nonadmin {
				if err == nil || len(private)+len(replies) != 0 {
					t.Fatalf("unauthorized lookup: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(replies) != 1 {
				t.Fatalf("replies: %v", replies)
			}
			if tc.group && strings.Contains(replies[0], "endpoint.invalid") {
				t.Fatal("group leaked details")
			}
			if tc.missing {
				if len(private) != 0 || !strings.Contains(replies[0], "未找到") {
					t.Fatal("foreign lookup succeeded")
				}
				return
			}
			if tc.group {
				if len(private) != 1 || !strings.Contains(private[0], id) || !strings.Contains(private[0], "endpoint.invalid") {
					t.Fatal("lost private diagnostics")
				}
			} else if !strings.Contains(replies[0], id) {
				t.Fatal("lost direct diagnostics")
			}
			if tc.fail && !strings.Contains(replies[0], "失败") {
				t.Fatal("reported false success")
			}
		})
	}
}
