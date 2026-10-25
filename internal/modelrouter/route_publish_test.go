package modelrouter

import (
	"reflect"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestRoutePublicationGuardKeepsScopesIsolated(t *testing.T) {
	for _, backend := range []storage.Backend{storage.SQLite, storage.Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			db := catalogTestDB(t, backend)
			catalog := seedCatalog(t, db, "https://synthetic.invalid/v1")
			routes := NewSQL(db, "instance-a")
			otherInstance := NewSQL(db, "instance-b")
			initial := routes.Active()
			otherInitial := otherInstance.Active()
			configA := cloneConfiguration(initial)
			configA.GlobalBindings[WorkloadDialogue] = Binding{Mode: BindingModel, ModelID: "model-a"}
			configB := cloneConfiguration(initial)
			configB.GlobalBindings[WorkloadDialogue] = Binding{Mode: BindingModel, ModelID: "model-b"}
			if err := routes.SaveDraft(configA); err != nil {
				t.Fatal(err)
			}
			if err := routes.SaveDraft(configB); err != nil {
				t.Fatal(err)
			}
			if _, err := routes.PublishExpectedRoutes(configA); err == nil {
				t.Fatal("replacement route draft was published")
			}
			if !reflect.DeepEqual(routes.Active(), initial) || !reflect.DeepEqual(routes.Draft(), configB) {
				t.Fatal("conflict changed active routes or the replacement draft")
			}
			catalogUpdate := catalog.Active()
			catalogUpdate.Endpoints[0].DisplayName = "Updated provider"
			if err := catalog.SaveDraft(catalogUpdate); err != nil {
				t.Fatal(err)
			}
			if _, err := catalog.Publish(); err != nil {
				t.Fatal(err)
			}
			published, err := routes.PublishExpectedRoutes(configB)
			if err != nil {
				t.Fatal(err)
			}
			if published.GlobalBindings[WorkloadDialogue].ModelID != "model-b" || published.Endpoints[0].DisplayName != "Updated provider" {
				t.Fatal("route publication lost its own binding or reverted the shared catalog")
			}
			if _, err := routes.PublishExpectedRoutes(configB); err == nil {
				t.Fatal("already published revision was accepted again")
			}
			otherActive := otherInstance.Active()
			if otherActive.Revision != otherInitial.Revision || !reflect.DeepEqual(otherActive.GlobalBindings, otherInitial.GlobalBindings) {
				t.Fatal("route publication changed another instance")
			}
			// A discard also replaces the draft without incrementing active revision.
			pending := cloneConfiguration(published)
			pending.GlobalBindings[WorkloadDialogue] = Binding{Mode: BindingDisabled}
			if err := routes.SaveDraft(pending); err != nil {
				t.Fatal(err)
			}
			routes.DiscardDraft()
			if _, err := routes.PublishExpectedRoutes(pending); err == nil {
				t.Fatal("discarded edit was published")
			}
		})
	}
}
