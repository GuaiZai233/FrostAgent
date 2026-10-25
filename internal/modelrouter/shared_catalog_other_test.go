//go:build !windows

package modelrouter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestUnusedWindowsProviderDoesNotBlockOtherInstances(t *testing.T) {
	db := catalogTestDB(t, storage.SQLite)
	catalog := seedCatalog(t, db, "https://shared.invalid/v1")
	cfg := catalog.Draft()
	cfg.Endpoints = append(cfg.Endpoints, Endpoint{ID: "windows", DisplayName: "Windows", BaseURL: "https://windows.invalid/v1", APIKeySource: APIKeyStorageWindowsCredentialManager, Enabled: true})
	cfg.Models = append(cfg.Models, Model{ID: "windows-model", DisplayName: "Windows Model", EndpointID: "windows", UpstreamModel: "synthetic", Enabled: true})
	if err := catalog.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Publish(); err != nil {
		t.Fatal(err)
	}
	a := NewSQL(db, "instance-a")
	if err := a.LoadError(); err != nil {
		t.Fatalf("unused Windows provider prevented instance load: %v", err)
	}
	bindInstance(t, a, "model-a")
	if _, err := a.ActiveChecked(); err != nil {
		t.Fatal(err)
	}
	snapshot := a.Snapshot()
	if err := snapshot.LoadError(); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.Resolve(WorkloadDialogue, Scope{}); err != nil {
		t.Fatal(err)
	}
	bindInstance(t, a, "windows-model")
	_, err := a.Provider(WorkloadDialogue, false, time.Second).Chat(context.Background(), core.ChatRequest{})
	if err == nil || !strings.Contains(err.Error(), "Windows Credential Manager") {
		t.Fatalf("selected Windows provider should report local availability: %v", err)
	}
}
