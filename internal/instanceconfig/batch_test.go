package instanceconfig

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/GuaiZai233/FrostAgent/internal/testutil/sqlfault"
)

func TestDatabaseCommitAcknowledgementLoss(t *testing.T) {
	for _, failReadback := range []bool{false, true} {
		name := "confirmed-cache"
		if failReadback {
			name = "isolated-cache"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			db, err := storage.Open(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := db.CreateInstance(context.Background(), storage.InstanceRecord{ID: "synthetic", Name: "Synthetic", CreatedAt: time.Now()}, 1); err != nil {
				t.Fatal(err)
			}
			instance, _ := OpenDatabase(db, "synthetic", false)
			global, _ := OpenDatabase(db, "", true)
			for key, value := range map[string]string{"MCP_CONTROL_TOKEN": "synthetic-old", "ADMIN_TOKEN": "synthetic-legacy"} {
				if err := global.Update(key, value, false); err != nil {
					t.Fatal(err)
				}
			}
			persisted := db.SQL
			fault := sqlfault.Install(t, db, filepath.Join(root, "frostagent.db"))
			fault.FailReadback = failReadback
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fault.AfterCommit = cancel
			err = ApplyDatabaseChanges(ctx, []DatabaseChange{
				{Store: instance, Key: "BOT_NAME", Value: "Synthetic new"},
				{Store: global, Key: "MCP_CONTROL_TOKEN", Value: "synthetic-new"},
			})
			if !errors.Is(err, ErrOutcomeUnknown) || fault.Commits.Load() != 1 {
				t.Fatalf("commit result = %v; commits = %d", err, fault.Commits.Load())
			}
			var actual string
			if err := persisted.QueryRow("SELECT value FROM settings WHERE scope = 'instance' AND instance_id = 'synthetic' AND key = 'BOT_NAME'").Scan(&actual); err != nil || actual != "Synthetic new" {
				t.Fatalf("fixture did not commit the real SQL write: %q %v", actual, err)
			}
			if !failReadback {
				if instance.Get("BOT_NAME") != "Synthetic new" || global.Get("MCP_CONTROL_TOKEN") != "synthetic-new" || instance.Error() != nil || global.Error() != nil {
					t.Fatal("independent readback did not refresh both scopes after request cancellation")
				}
				return
			}
			if !errors.Is(instance.Error(), ErrStateUnavailable) || !errors.Is(global.Error(), ErrStateUnavailable) || instance.Snapshot()["BOT_NAME"] != "" {
				t.Fatal("partial or stale readback was not isolated")
			}
			if global.Get("MCP_CONTROL_TOKEN") != "" || global.Get("ADMIN_TOKEN") != "" {
				t.Fatal("unconfirmed credentials still authorize")
			}
			for _, write := range []func() error{
				func() error {
					return ApplyDatabaseChanges(context.Background(), []DatabaseChange{{Store: instance, Key: "BOT_NAME", Value: "overwrite"}})
				},
				func() error { return instance.Update("BOT_NAME", "overwrite", false) },
				func() error {
					return instance.Transform(context.Background(), "BOT_NAME", func(string) (string, error) { t.Error("transform used stale state"); return "", nil })
				},
				func() error {
					return instance.ReplaceDatabaseSubset(map[string]bool{"BOT_NAME": true}, map[string]string{"BOT_NAME": "overwrite"})
				},
			} {
				if err := write(); !errors.Is(err, ErrStateUnavailable) {
					t.Fatalf("write was not blocked: %v", err)
				}
			}
		})
	}
}
