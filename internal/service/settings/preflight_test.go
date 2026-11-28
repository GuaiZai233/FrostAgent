package settings

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"path/filepath"
	"strings"
	"testing"
)

func TestScopedPreflightIsPureAtomicAndRedacted(t *testing.T) {
	dir := t.TempDir()
	instance, err := instanceconfig.Open(filepath.Join(dir, "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	global, err := instanceconfig.Open(filepath.Join(dir, "global.env"), true)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewScoped(instance, global, nil)
	if err = instance.Update("ONEBOT_WS_TOKEN", "synthetic-secret", false); err != nil {
		t.Fatal(err)
	}
	req := &v1.UpdateEnvVarsRequest{Scope: "instance", DryRun: true, Updates: []*v1.EnvVar{{Key: "BOT_NAME", Value: "candidate"}, {Key: "ENABLE_EMBEDDING", Value: "true"}}}
	writes := 0
	reads := 0
	svc.SetEmbeddingStorage("sqlite", func(context.Context) error { writes++; return nil }, func(context.Context) error { reads++; return nil })
	result, err := svc.UpdateEnvVars(context.Background(), connect.NewRequest(req))
	if err != nil || !result.Msg.Success {
		t.Fatalf("%v %v", result, err)
	}
	if instance.Get("BOT_NAME") != "" || writes != 0 || reads != 1 {
		t.Fatal("preflight mutated state")
	}
	req.DryRun = false
	req.Updates = append(req.Updates, &v1.EnvVar{Key: "UNKNOWN_SETTING", Value: "bad"})
	result, err = svc.UpdateEnvVars(context.Background(), connect.NewRequest(req))
	if err != nil || result.Msg.Success || len(result.Msg.Errors) != 1 || instance.Get("BOT_NAME") != "" {
		t.Fatal("batch failed to reject atomically")
	}
	read, err := svc.ListEnvVars(context.Background(), connect.NewRequest(&v1.ListEnvVarsRequest{Scope: "instance", RedactSecrets: true}))
	if err != nil {
		t.Fatal(err)
	}
	bytes, _ := json.Marshal(read.Msg)
	if strings.Contains(string(bytes), "synthetic-secret") {
		t.Fatal("ordinary read exposed secret")
	}
	for _, v := range read.Msg.EnvVars {
		if v.Key == "ONEBOT_WS_TOKEN" && !v.SecretConfigured {
			t.Fatal("secret state missing")
		}
		if instanceconfig.GlobalKeys[v.Key] {
			t.Fatal("global key escaped scope")
		}
	}
}

func TestListenerPreflightMatchesApply(t *testing.T) {
	global, err := instanceconfig.Open(filepath.Join(t.TempDir(), "global.env"), true)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewScoped(nil, global, nil)
	for _, dryRun := range []bool{true, false} {
		for _, address := range []string{"invalid-listener", "127.0.0.1:not-a-port"} {
			req := &v1.UpdateEnvVarsRequest{Scope: "global", DryRun: dryRun, Updates: []*v1.EnvVar{{Key: "LISTEN_ADDR", Value: address}}}
			result, err := svc.UpdateEnvVars(context.Background(), connect.NewRequest(req))
			if err != nil {
				t.Fatal(err)
			}
			if result.Msg.Success || result.Msg.State == "validated" || !strings.Contains(result.Msg.Error, "management") {
				t.Fatalf("dryRun=%v %q accepted: %+v", dryRun, address, result.Msg)
			}
			if global.Get("LISTEN_ADDR") != "" {
				t.Fatal("rejected listener was written")
			}
		}
	}
	req := &v1.UpdateEnvVarsRequest{Scope: "global", DryRun: true, Updates: []*v1.EnvVar{{Key: "LISTEN_ADDR", Value: "127.0.0.1:18080"}}}
	result, err := svc.UpdateEnvVars(context.Background(), connect.NewRequest(req))
	if err != nil || !result.Msg.Success || result.Msg.State != "validated" {
		t.Fatalf("valid listener rejected: %+v %v", result.Msg, err)
	}
}
