package settings

import (
	"connectrpc.com/connect"
	"context"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"path/filepath"
	"testing"
)

func TestTelegramDraftValidationRestartAndSecretMetadata(t *testing.T) {
	config, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	service := NewScoped(config, nil, nil)
	invalid, err := service.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{Updates: []*v1.EnvVar{{Key: "TELEGRAM_ENABLED", Value: "true"}, {Key: "BOT_NAME", Value: "must-not-save"}}}))
	if err != nil || invalid.Msg.Success || config.Get("BOT_NAME") != "" {
		t.Fatalf("invalid draft persisted: %v %v", invalid, err)
	}
	valid, err := service.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{Updates: []*v1.EnvVar{{Key: "TELEGRAM_ENABLED", Value: "true"}, {Key: "TELEGRAM_BOT_TOKEN", Value: "900000:synthetic-test-token"}}}))
	if err != nil || !valid.Msg.Success {
		t.Fatalf("valid Telegram draft rejected: %v %v", valid, err)
	}
	deleted, err := service.DeleteEnvVar(context.Background(), connect.NewRequest(&v1.DeleteEnvVarRequest{Key: "TELEGRAM_BOT_TOKEN"}))
	if err != nil || deleted.Msg.Success || config.Get("TELEGRAM_BOT_TOKEN") == "" {
		t.Fatal("active token could be cleared without disabling")
	}
	for _, key := range []string{"TELEGRAM_ENABLED", "TELEGRAM_BOT_TOKEN", "TELEGRAM_PROXY_URL"} {
		if !instanceconfig.InstanceRestartKeys[key] || !knownEnvVars[key].RequiresRestart {
			t.Fatalf("missing restart marker for %s", key)
		}
	}
	if !knownEnvVars["TELEGRAM_BOT_TOKEN"].IsSecret || !knownEnvVars["TELEGRAM_PROXY_URL"].IsSecret {
		t.Fatal("credentials not marked secret")
	}
}
