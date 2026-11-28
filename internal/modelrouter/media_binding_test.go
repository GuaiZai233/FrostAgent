package modelrouter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

// The media workload postdates the v1 storage contract and older binaries
// reject unknown workload rows, so its default Disabled row is never stored.
func TestMediaBindingStoredOnlyWhenConfigured(t *testing.T) {
	db := catalogTestDB(t, storage.SQLite)
	seedCatalog(t, db, "https://synthetic.invalid/v1")
	routes := NewSQL(db, "instance-a")
	countMediaRows := func() int {
		t.Helper()
		var count int
		if err := db.SQL.QueryRow(db.Bind(`SELECT COUNT(*) FROM model_bindings WHERE instance_id = ? AND workload = ?`), "instance-a", string(WorkloadMedia)).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	config := cloneConfiguration(routes.Active())
	config.GlobalBindings[WorkloadDialogue] = Binding{Mode: BindingModel, ModelID: "model-a"}
	if err := routes.SaveDraft(config); err != nil {
		t.Fatal(err)
	}
	published, err := routes.PublishExpectedRoutes(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := published.GlobalBindings[WorkloadMedia]; got.Mode != BindingDisabled {
		t.Fatalf("default media binding = %+v", got)
	}
	if n := countMediaRows(); n != 0 {
		t.Fatalf("default media binding stored %d rows", n)
	}

	config = cloneConfiguration(published)
	config.GlobalBindings[WorkloadMedia] = Binding{Mode: BindingModel, ModelID: "model-b"}
	if err := routes.SaveDraft(config); err != nil {
		t.Fatal(err)
	}
	if _, err := routes.PublishExpectedRoutes(config); err != nil {
		t.Fatal(err)
	}
	if n := countMediaRows(); n != 1 {
		t.Fatalf("configured media binding stored %d rows", n)
	}
	reloaded := NewSQL(db, "instance-a").Active()
	if got := reloaded.GlobalBindings[WorkloadMedia]; got.Mode != BindingModel || got.ModelID != "model-b" {
		t.Fatalf("configured media binding lost on reload: %+v", got)
	}
}

func TestConfigurationJSONOmitsDefaultMediaBinding(t *testing.T) {
	config := defaultConfiguration()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"media"`) {
		t.Fatalf("default media binding serialized: %s", data)
	}
	if _, ok := config.GlobalBindings[WorkloadMedia]; !ok {
		t.Fatal("marshaling mutated the configuration")
	}
	var decoded Configuration
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	normalizeConfiguration(&decoded)
	if got := decoded.GlobalBindings[WorkloadMedia]; got.Mode != BindingDisabled {
		t.Fatalf("default media binding not restored on read: %+v", got)
	}
	config.GlobalBindings[WorkloadMedia] = Binding{Mode: BindingModel, ModelID: "model-a"}
	if data, _ = json.Marshal(config); !strings.Contains(string(data), `"media":{"mode":"model","model_id":"model-a"}`) {
		t.Fatalf("configured media binding dropped: %s", data)
	}
}
