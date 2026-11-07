package settings

import (
	"connectrpc.com/connect"
	"context"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/replyformat"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"path/filepath"
	"testing"
	"time"
)

func TestMultiSegmentReplySetting(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "settings.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []string{"synthetic-first", "synthetic-second"} {
		if err := db.CreateInstance(context.Background(), storage.InstanceRecord{ID: id, Name: id, CreatedAt: time.Now()}, 2); err != nil {
			t.Fatal(err)
		}
		if err := instanceconfig.InitializeInstanceSettings(db, id); err != nil {
			t.Fatal(err)
		}
	}
	first, err := instanceconfig.OpenDatabase(db, "synthetic-first", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := instanceconfig.OpenDatabase(db, "synthetic-second", false)
	if err != nil {
		t.Fatal(err)
	}
	global, err := instanceconfig.OpenDatabase(db, "", true)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewScoped(first, global, nil)
	if first.Get(replyformat.EnvKey) != "false" {
		t.Fatal("new instances must default to disabled segmentation")
	}
	for _, value := range []string{"true", "false", ""} {
		result, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{Key: replyformat.EnvKey, Value: value}))
		if err != nil || result == nil || !result.Msg.Success {
			t.Fatalf("update %q: %v / %v", value, result, err)
		}
		reopened, err := instanceconfig.OpenDatabase(db, "synthetic-first", false)
		if err != nil {
			t.Fatal(err)
		}
		if reopened.Get(replyformat.EnvKey) != value || second.Get(replyformat.EnvKey) != "false" || global.Get(replyformat.EnvKey) != "" {
			t.Fatal("setting persistence/scope")
		}
	}
	if knownEnvVars[replyformat.EnvKey].RequiresRestart || instanceconfig.GlobalKeys[replyformat.EnvKey] {
		t.Fatal("setting must be live and instance scoped")
	}
	if err := validateUpdate(replyformat.EnvKey, "yes"); err == nil {
		t.Fatal("invalid boolean accepted")
	}
}
