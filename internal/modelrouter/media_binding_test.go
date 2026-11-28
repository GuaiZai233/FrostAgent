package modelrouter

import (
	"context"
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

// Group overrides of the media workload are never stored either: older
// binaries reject unknown workloads in every override.
func TestGroupMediaOverrideIsNotStored(t *testing.T) {
	config := defaultConfiguration()
	config.GroupOverrides = []GroupOverride{{Platform: "onebot", GroupID: "g1", Bindings: map[Workload]Binding{
		WorkloadMedia:    {Mode: BindingModel, ModelID: "model-a"},
		WorkloadDialogue: {Mode: BindingModel, ModelID: "model-b"},
	}}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		GroupOverrides []struct {
			Bindings map[string]Binding `json:"bindings"`
		} `json:"group_overrides"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded.GroupOverrides[0].Bindings["media"]; ok || decoded.GroupOverrides[0].Bindings["dialogue"].ModelID != "model-b" {
		t.Fatalf("group bindings serialized wrongly: %s", data)
	}
	if _, ok := config.GroupOverrides[0].Bindings[WorkloadMedia]; !ok {
		t.Fatal("marshaling mutated the group override")
	}

	db := catalogTestDB(t, storage.SQLite)
	seedCatalog(t, db, "https://synthetic.invalid/v1")
	tx, err := db.SQL.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeConfigurationTx(context.Background(), db, tx, "instance-a", config); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var media, dialogue int
	row := db.SQL.QueryRow(db.Bind(`SELECT COALESCE(SUM(CASE WHEN workload = 'media' THEN 1 ELSE 0 END), 0), COALESCE(SUM(CASE WHEN workload = 'dialogue' AND group_id = 'g1' THEN 1 ELSE 0 END), 0) FROM model_bindings WHERE instance_id = ?`), "instance-a")
	if err := row.Scan(&media, &dialogue); err != nil {
		t.Fatal(err)
	}
	if media != 0 || dialogue != 1 {
		t.Fatalf("stored media rows=%d group dialogue rows=%d", media, dialogue)
	}
}
