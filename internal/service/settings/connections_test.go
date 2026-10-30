package settings

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestConnectionSettingsValidateFinalDraftBeforeWriting(t *testing.T) {
	for _, adapter := range []string{"ONEBOT", "ASTRBOT"} {
		t.Run(adapter, func(t *testing.T) {
			db, err := storage.Open(context.Background(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			config, err := instanceconfig.OpenDatabase(db, "synthetic-connection-instance", false)
			if err != nil {
				t.Fatal(err)
			}
			svc := NewScoped(config, nil, nil)
			mode, url, token := adapter+"_WS_MODE", adapter+"_WS_URL", adapter+"_WS_TOKEN"
			for _, value := range []string{"invalid", "forward"} {
				response, err := svc.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{
					Updates: []*v1.EnvVar{{Key: "BOT_NAME", Value: "must-not-save"}, {Key: mode, Value: value}},
				}))
				if err != nil || response.Msg.Success || response.Msg.Error == "" {
					t.Fatalf("invalid draft accepted: %v %v", response, err)
				}
				if config.Get("BOT_NAME") != "" || config.Get(mode) != "" {
					t.Fatal("invalid draft partially persisted")
				}
			}
			response, err := svc.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{
				Updates: []*v1.EnvVar{{Key: mode, Value: "forward"}, {Key: url, Value: "ws://127.0.0.1:1244/"}, {Key: token, Value: "synthetic-ws-token"}},
			}))
			if err != nil || !response.Msg.Success {
				t.Fatalf("complete draft rejected: %v %v", response, err)
			}
			reopened, err := instanceconfig.OpenDatabase(db, "synthetic-connection-instance", false)
			if err != nil || reopened.Get(mode) != "forward" || reopened.Get(token) != "synthetic-ws-token" {
				t.Fatalf("settings did not persist: %v", err)
			}
			for _, badURL := range []string{"http://example.invalid/", "ws://user:synthetic-url-secret@example.invalid/", "ws://example.invalid/#synthetic-url-secret"} {
				update, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{Key: url, Value: badURL}))
				if err != nil || update.Msg.Success || strings.Contains(update.Msg.Error, "synthetic-url-secret") {
					t.Fatalf("invalid URL accepted or leaked: %v %v", update, err)
				}
				if config.Get(url) != "ws://127.0.0.1:1244/" {
					t.Fatal("invalid URL replaced saved settings")
				}
			}
			deleted, err := svc.DeleteEnvVar(context.Background(), connect.NewRequest(&v1.DeleteEnvVarRequest{Key: url}))
			if err != nil || deleted.Msg.Success || config.Get(url) == "" {
				t.Fatalf("removed URL of active forward configuration: %v %v", deleted, err)
			}
			response, err = svc.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{
				Updates: []*v1.EnvVar{{Key: mode, Value: "reverse"}}, ClearKeys: []string{url, token},
			}))
			if err != nil || !response.Msg.Success || config.Get(mode) != "reverse" || config.Get(url) != "" || config.Get(token) != "" {
				t.Fatalf("reverse transition rejected: %v %v", response, err)
			}
			deleted, err = svc.DeleteEnvVar(context.Background(), connect.NewRequest(&v1.DeleteEnvVarRequest{Key: mode}))
			if err != nil || !deleted.Msg.Success || config.Get(mode) != "" {
				t.Fatalf("reset to default reverse failed: %v %v", deleted, err)
			}
		})
	}
}

func TestConnectionSettingMetadataAndIsolation(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	config, err := instanceconfig.OpenDatabase(db, "synthetic-connection-instance", false)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewScoped(config, nil, nil)
	listed, err := svc.ListEnvVars(context.Background(), connect.NewRequest(&v1.ListEnvVarsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]*v1.EnvVar)
	for _, entry := range listed.Msg.EnvVars {
		entries[entry.Key] = entry
	}
	for _, adapter := range []string{"ONEBOT", "ASTRBOT"} {
		for _, suffix := range []string{"_WS_MODE", "_WS_URL", "_WS_TOKEN"} {
			key := adapter + suffix
			entry := entries[key]
			if entry == nil || entry.IsSecret != (suffix == "_WS_TOKEN") || instanceconfig.GlobalKeys[key] {
				t.Fatalf("incorrect connection metadata for %s", key)
			}
			if suffix == "_WS_MODE" && entry.ResetHint != "reverse" {
				t.Fatal("default must preserve reverse mode")
			}
			if !instanceconfig.InstanceRestartKeys[key] {
				t.Fatalf("legacy lifecycle missing connection key %s", key)
			}
			if suffix == "_WS_TOKEN" && ExportableValueKeys()[key] {
				t.Fatal("connection credential included in non-secret settings export")
			}
		}
	}
}
