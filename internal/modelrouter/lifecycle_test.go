package modelrouter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func lifecycleConfiguration() Configuration {
	cfg := defaultConfiguration()
	cfg.Endpoints = []Endpoint{{ID: "endpoint_test", DisplayName: "Test endpoint", BaseURL: "https://example.invalid/v1", APIKeySource: APIKeyStorageManual, Enabled: true}}
	cfg.Models = []Model{{ID: "model_test", DisplayName: "Test model", EndpointID: "endpoint_test", UpstreamModel: "model-1", Enabled: true}}
	cfg.GlobalBindings[WorkloadDialogue] = Binding{Mode: BindingModel, ModelID: "model_test"}
	return cfg
}

func publishedLifecycleManager(t *testing.T) *Manager {
	t.Helper()
	m := New(filepath.Join(t.TempDir(), "model_router.json"))
	if err := m.SaveDraft(lifecycleConfiguration()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetDraftEndpointSecret("endpoint_test", "test-published-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish(); err != nil {
		t.Fatal(err)
	}
	return m
}

func assertLifecycleSecret(t *testing.T, m *Manager, want string) {
	t.Helper()
	key, err := m.secrets.Resolve(m.Active().Endpoints[0])
	if err != nil || key != want {
		t.Fatalf("published test secret mismatch (error: %v)", err)
	}
}

func TestPublishPersistsConfigurationAndSecretWithoutSharingSnapshots(t *testing.T) {
	m := publishedLifecycleManager(t)
	before := m.Snapshot()
	cfg := m.Draft()
	cfg.Models[0].UpstreamModel = "model-2"
	if err := m.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetDraftEndpointSecret("endpoint_test", "test-next-key"); err != nil {
		t.Fatal(err)
	}
	published, err := m.Publish()
	if err != nil {
		t.Fatal(err)
	}
	if published.Revision != 2 || m.HasDraftSecretChanges() || !reflect.DeepEqual(m.Active(), m.Draft()) {
		t.Fatal("publish did not commit revision and discard staged secret changes")
	}
	published.Models[0].UpstreamModel = "caller-mutation"
	reloaded := New(m.path)
	if err := reloaded.LoadError(); err != nil {
		t.Fatal(err)
	}
	target, err := reloaded.Resolve(WorkloadDialogue, Scope{})
	if err != nil || target.UpstreamModel != "model-2" || reloaded.Active().Revision != 2 {
		t.Fatalf("published configuration was not persisted: %+v, %v", target, err)
	}
	assertLifecycleSecret(t, reloaded, "test-next-key")
	oldTarget, err := before.Resolve(WorkloadDialogue, Scope{})
	if err != nil || oldTarget.UpstreamModel != "model-1" {
		t.Fatalf("existing snapshot changed after publish: %+v, %v", oldTarget, err)
	}
}

func TestPublishConfigurationFailureRollsBackSecretsAndAllowsRetry(t *testing.T) {
	m := publishedLifecycleManager(t)
	active := m.Active()
	path := m.path
	configBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	secretBytes, err := os.ReadFile(m.secrets.manualPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := m.Draft()
	cfg.Models[0].UpstreamModel = "model-2"
	if err := m.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetDraftEndpointSecret("endpoint_test", "test-next-key"); err != nil {
		t.Fatal(err)
	}
	// Secret Apply writes the real secret store before configuration persistence
	// fails. A file used as the destination parent fails on both Windows and Linux.
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	m.path = filepath.Join(blocked, "model_router.json")
	if _, err := m.Publish(); err == nil {
		t.Fatal("configuration write unexpectedly succeeded")
	}
	if !reflect.DeepEqual(m.Active(), active) || !m.HasDraftSecretChanges() || m.Draft().Models[0].UpstreamModel != "model-2" {
		t.Fatal("failed publish changed active state or lost the retryable draft")
	}
	assertLifecycleSecret(t, m, "test-published-key")
	for file, want := range map[string][]byte{path: configBytes, m.secrets.manualPath: secretBytes} {
		got, err := os.ReadFile(file)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("failed publish changed persisted state at %s: %v", filepath.Base(file), err)
		}
	}
	m.path = path
	if _, err := m.Publish(); err != nil {
		t.Fatalf("retry failed after restoring persistence: %v", err)
	}
	assertLifecycleSecret(t, New(path), "test-next-key")
}

func TestDiscardDraftRestoresPublishedConfigurationAndSecrets(t *testing.T) {
	m := publishedLifecycleManager(t)
	active := m.Active()
	cfg := m.Draft()
	cfg.Models[0].UpstreamModel = "discarded-model"
	if err := m.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClearDraftEndpointSecret("endpoint_test"); err != nil {
		t.Fatal(err)
	}
	discarded := m.DiscardDraft()
	if !reflect.DeepEqual(discarded, active) || m.HasDraftSecretChanges() {
		t.Fatal("discard did not restore the published configuration and secret status")
	}
	assertLifecycleSecret(t, m, "test-published-key")
	if _, err := m.Publish(); err != nil {
		t.Fatal(err)
	}
	assertLifecycleSecret(t, New(m.path), "test-published-key")
}

func TestInvalidDraftDoesNotChangePublishedOrDraftState(t *testing.T) {
	for name, invalidate := range map[string]func(*Configuration){
		"endpoint URL":     func(c *Configuration) { c.Endpoints[0].BaseURL = "file:///invalid" },
		"missing endpoint": func(c *Configuration) { c.Models[0].EndpointID = "missing" },
		"missing model": func(c *Configuration) {
			c.GlobalBindings[WorkloadDialogue] = Binding{Mode: BindingModel, ModelID: "missing"}
		},
		"disabled reflection": func(c *Configuration) { c.GlobalBindings[WorkloadReflection] = Binding{Mode: BindingDisabled} },
	} {
		t.Run(name, func(t *testing.T) {
			m := publishedLifecycleManager(t)
			before := m.Active()
			invalid := m.Draft()
			invalidate(&invalid)
			if err := m.SaveDraft(invalid); err == nil {
				t.Fatal("invalid configuration was accepted")
			}
			if !reflect.DeepEqual(m.Active(), before) || !reflect.DeepEqual(m.Draft(), before) {
				t.Fatal("invalid input changed configuration state")
			}
		})
	}
}

func TestLegacyInlineSecretMigratesAndIsRemovedFromConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model_router.json")
	cfg := lifecycleConfiguration()
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(encoded, &legacy); err != nil {
		t.Fatal(err)
	}
	endpoint := legacy["endpoints"].([]any)[0].(map[string]any)
	delete(endpoint, "api_key_source")
	endpoint["api_key_storage"] = "manual"
	endpoint["api_key"] = "test-legacy-key"
	encoded, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	m := New(path)
	if err := m.LoadError(); err != nil {
		t.Fatal(err)
	}
	assertLifecycleSecret(t, m, "test-legacy-key")
	clean, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(clean, []byte(`"api_key"`)) || bytes.Contains(clean, []byte(`"api_key_storage"`)) || bytes.Contains(clean, []byte("test-legacy-key")) {
		t.Fatal("legacy secret material remained in configuration")
	}
	reloaded := New(path)
	if err := reloaded.LoadError(); err != nil {
		t.Fatal(err)
	}
	assertLifecycleSecret(t, reloaded, "test-legacy-key")
}

func TestConcurrentSnapshotsRemainConsistentDuringPublish(t *testing.T) {
	m := publishedLifecycleManager(t)
	start := make(chan struct{})
	errs := make(chan error, 4)
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			<-start
			for range 100 {
				snapshot := m.Snapshot()
				target, err := snapshot.Resolve(WorkloadDialogue, Scope{})
				if err != nil || target.UpstreamModel != fmt.Sprintf("model-%d", snapshot.config.Revision) {
					errs <- fmt.Errorf("inconsistent snapshot at revision %d: %v", snapshot.config.Revision, err)
					return
				}
				active := m.Active()
				active.Models[0].UpstreamModel = "caller-mutation"
				draft := m.Draft()
				draft.Models[0].UpstreamModel = "caller-mutation"
			}
		})
	}
	close(start)
	for revision := 2; revision <= 12; revision++ {
		cfg := m.Draft()
		cfg.Models[0].UpstreamModel = fmt.Sprintf("model-%d", revision)
		if err := m.SaveDraft(cfg); err != nil {
			t.Error(err)
			break
		}
		if _, err := m.Publish(); err != nil {
			t.Error(err)
			break
		}
	}
	readers.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
