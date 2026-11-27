package modelrouter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestDirectPublicationPreservesDraftAndTurnSnapshot(t *testing.T) {
	db := catalogTestDB(t, storage.SQLite)
	catalog := seedCatalog(t, db, "https://synthetic.invalid/v1")
	route := NewSQL(db, "instance-a")
	if _, err := route.ApplyDirect(Mutation{Operation: "route-set", Workload: WorkloadDialogue, Binding: Binding{Mode: BindingModel, ModelID: "model-a"}}); err != nil {
		t.Fatal(err)
	}
	before := route.Snapshot()
	draft := catalog.Active()
	draft.Endpoints[0].BaseURL = "https://pending.invalid/v1"
	if err := catalog.SaveDraftForOwner(draft, "synthetic-editor"); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SetDraftEndpointSecretForOwner("shared", "synthetic-pending", "synthetic-editor"); err != nil {
		t.Fatal(err)
	}
	active, err := catalog.ApplyDirect(Mutation{Operation: "secret-set", EndpointID: "shared", Secret: "synthetic-current"})
	if err != nil || active.Revision <= draft.Revision || !active.Endpoints[0].APIKeyConfigured {
		t.Fatalf("direct publish: %+v %v", active, err)
	}
	if _, err := catalog.PublishForOwner("synthetic-editor"); err == nil {
		t.Fatal("stale Web draft overwrote direct publication")
	}
	if !catalog.HasDraftSecretChangesForOwner("synthetic-editor") {
		t.Fatal("direct publication discarded Web secret draft")
	}
	after := route.Snapshot()
	if before.credentials["shared"].value != "synthetic-old-key" || after.credentials["shared"].value != "synthetic-current" {
		t.Fatal("turn snapshot credential isolation failed")
	}
	if _, err := catalog.ApplyDirect(Mutation{Operation: "endpoint-remove", EndpointID: "shared"}); err == nil {
		t.Fatal("referenced endpoint was removed")
	}
	routeDraft := route.Active()
	if err := route.SaveDraft(routeDraft); err != nil {
		t.Fatal(err)
	}
	if _, err := route.ApplyDirect(Mutation{Operation: "route-set", Workload: WorkloadDialogue, Binding: Binding{Mode: BindingModel, ModelID: "model-b"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := route.PublishExpectedRoutes(routeDraft); err == nil {
		t.Fatal("stale route draft overwrote active route")
	}
	target, err := route.Snapshot().Resolve(WorkloadDialogue, Scope{})
	if err != nil || target.ModelID != "model-b" {
		t.Fatal(target, err)
	}
	oldTarget, err := before.Resolve(WorkloadDialogue, Scope{})
	if err != nil || oldTarget.ModelID != "model-a" {
		t.Fatal("old snapshot changed")
	}
	if _, _, err := catalog.TestActiveModel(context.Background(), "model-a", "unrelated"); err == nil {
		t.Fatal("mismatched model was tested")
	}
}

func TestImportPreviewUsesCascadeRemappingAndCredentialSources(t *testing.T) {
	db := catalogTestDB(t, storage.SQLite)
	catalog := seedCatalog(t, db, "https://synthetic.invalid/v1")
	incoming := catalog.Active()
	incoming.Endpoints[0].BaseURL = "https://conflicting.invalid/v1"
	plan, err := PreviewImportedConfiguration(context.Background(), db, incoming)
	if err != nil || len(plan.RemappedEndpoints) != 1 || len(plan.RemappedModels) != 2 {
		t.Fatalf("cascade remap missing: %+v %v", plan, err)
	}
	file := filepath.Join(t.TempDir(), "synthetic-key")
	if err := os.WriteFile(file, []byte("synthetic-file-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec("UPDATE provider_endpoints SET api_key_source='secret_file',api_key_ref=?", file); err != nil {
		t.Fatal(err)
	}
	incoming = catalog.Active()
	plan, err = PreviewImportedConfiguration(context.Background(), db, incoming)
	if err != nil || len(plan.MissingCredentials) != 0 || len(plan.CredentialStateUnavailable) != 0 {
		t.Fatalf("configured file key misreported: %+v %v", plan, err)
	}
}

func TestDirectTransactionFailureAndConcurrentMutations(t *testing.T) {
	db := catalogTestDB(t, storage.SQLite)
	catalog := seedCatalog(t, db, "https://synthetic.invalid/v1")
	before := catalog.Active()
	if _, err := db.SQL.Exec("CREATE TRIGGER reject_direct_secret BEFORE UPDATE ON provider_secrets BEGIN SELECT RAISE(ABORT,'synthetic refusal'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.ApplyDirect(Mutation{Operation: "secret-set", EndpointID: "shared", Secret: "synthetic-rejected"}); err == nil || errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("expected definite rollback: %v", err)
	}
	if after := catalog.Active(); after.Revision != before.Revision {
		t.Fatal("failed transaction advanced revision")
	}
	if value, err := catalog.secrets.Resolve(before.Endpoints[0]); err != nil || value != "synthetic-old-key" {
		t.Fatal("failed transaction changed secret")
	}
	if _, err := db.SQL.Exec("DROP TRIGGER reject_direct_secret"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("synthetic-%d", n)
			_, err := NewSQL(db, "").ApplyDirect(Mutation{Operation: "endpoint-add", Endpoint: Endpoint{ID: id, DisplayName: id, BaseURL: "https://synthetic.invalid/v1", Enabled: true}})
			errs <- err
		}(n)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := catalog.Active(); len(got.Endpoints) != 13 || got.Revision != before.Revision+12 {
		t.Fatalf("concurrent writes lost: endpoints=%d revision=%d", len(got.Endpoints), got.Revision)
	}
	if _, err := catalog.ApplyDirect(Mutation{Operation: "secret-unset", EndpointID: "shared"}); err != nil {
		t.Fatal(err)
	}
	if catalog.Active().Endpoints[0].APIKeyConfigured {
		t.Fatal("secret unset still configured")
	}
	if _, err := catalog.ApplyDirect(Mutation{Operation: "endpoint-remove", EndpointID: "synthetic-0"}); err != nil {
		t.Fatal(err)
	}
}
