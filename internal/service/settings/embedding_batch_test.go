package settings

import (
	"context"
	"errors"
	"testing"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/storage"

	"connectrpc.com/connect"
)

func TestEmbeddingBatchValidatesBeforeWriting(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	config, err := instanceconfig.OpenDatabase(db, "synthetic-settings-instance", false)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewScoped(config, nil, nil)
	checks := 0
	blocked := true
	svc.SetEmbeddingStorage("postgres", func(context.Context) error {
		checks++
		if blocked {
			return errors.New("synthetic missing vector extension")
		}
		return nil
	})
	before := config.Get("BOT_NAME")
	for _, value := range []string{"invalid", "true"} {
		response, err := svc.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{Updates: []*v1.EnvVar{
			{Key: "BOT_NAME", Value: "synthetic changed name"}, {Key: "ENABLE_EMBEDDING", Value: value},
		}}))
		if err != nil || response.Msg.Success || response.Msg.Error == "" {
			t.Fatalf("invalid batch accepted: %v %v", response, err)
		}
		if config.Get("BOT_NAME") != before || config.Get("ENABLE_EMBEDDING") == "true" {
			t.Fatal("failed batch wrote settings")
		}
	}
	if checks != 1 {
		t.Fatalf("invalid input must not check storage: %d", checks)
	}
	blocked = false
	response, err := svc.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{Updates: []*v1.EnvVar{{Key: "ENABLE_EMBEDDING", Value: "true"}}}))
	if err != nil || !response.Msg.Success || config.Get("ENABLE_EMBEDDING") != "true" {
		t.Fatalf("enable batch failed: %v %v", response, err)
	}
	blocked = true
	response, err = svc.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{Updates: []*v1.EnvVar{{Key: "ENABLE_EMBEDDING", Value: "false"}}}))
	if err != nil || !response.Msg.Success {
		t.Fatalf("disable must not require vector extension: %v %v", response, err)
	}
	response, err = svc.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{ClearKeys: []string{"ENABLE_EMBEDDING"}}))
	if err != nil || !response.Msg.Success || config.Get("ENABLE_EMBEDDING") == "true" || checks != 2 {
		t.Fatalf("clear unexpectedly checked storage: %v %v checks=%d", response, err, checks)
	}
	listed, err := svc.ListEnvVars(context.Background(), connect.NewRequest(&v1.ListEnvVarsRequest{}))
	if err != nil || listed.Msg.StorageBackend != "postgres" {
		t.Fatal("storage backend missing from settings response")
	}
}
