package modelrouter

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
	router "github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"google.golang.org/protobuf/proto"
)

func TestPublishRejectsReplacedRouteDraftOverRPC(t *testing.T) {
	for _, backend := range []string{"file", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var manager *router.Manager
			if backend == "file" {
				manager = router.New(filepath.Join(t.TempDir(), "model_router.json"))
			} else {
				db, err := storage.OpenWithConfig(context.Background(), t.TempDir(), storage.Config{Backend: storage.SQLite})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { db.Close() })
				if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES ('synthetic-instance', 'Synthetic', 'now')`); err != nil {
					t.Fatal(err)
				}
				manager = router.NewSQL(db, "synthetic-instance")
			}
			_, handler := frostagentv1connect.NewModelRouterServiceHandler(New(manager))
			server := httptest.NewServer(handler)
			defer server.Close()
			clientA := frostagentv1connect.NewModelRouterServiceClient(server.Client(), server.URL)
			clientB := frostagentv1connect.NewModelRouterServiceClient(server.Client(), server.URL)
			ctx := context.Background()
			initial, err := clientA.GetState(ctx, connect.NewRequest(&v1.GetStateRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			configA := proto.Clone(initial.Msg.Active).(*v1.ModelRouterConfiguration)
			configA.GroupOverrides = []*v1.GroupModelOverride{{Platform: "qq", GroupId: "synthetic-group-a", Bindings: []*v1.ModelBinding{{Workload: v1.ModelWorkload_MODEL_WORKLOAD_DIALOGUE, Mode: v1.ModelBindingMode_MODEL_BINDING_MODE_DISABLED}}}}
			configB := proto.Clone(initial.Msg.Active).(*v1.ModelRouterConfiguration)
			configB.GroupOverrides = []*v1.GroupModelOverride{{Platform: "qq", GroupId: "synthetic-group-b", Bindings: []*v1.ModelBinding{{Workload: v1.ModelWorkload_MODEL_WORKLOAD_VISION, Mode: v1.ModelBindingMode_MODEL_BINDING_MODE_DISABLED}}}}
			for _, item := range []struct {
				client        frostagentv1connect.ModelRouterServiceClient
				configuration *v1.ModelRouterConfiguration
			}{{clientA, configA}, {clientB, configB}} {
				saved, err := item.client.SaveDraft(ctx, connect.NewRequest(&v1.SaveDraftRequest{Configuration: item.configuration}))
				if err != nil || !saved.Msg.Success {
					t.Fatalf("save: %v %v", saved, err)
				}
			}
			publishedA, err := clientA.Publish(ctx, connect.NewRequest(&v1.PublishRequest{ExpectedConfiguration: configA}))
			if err != nil {
				t.Fatal(err)
			}
			if publishedA.Msg.Success || publishedA.Msg.Error == "" {
				t.Fatalf("window A published window B's draft: %v", publishedA.Msg)
			}
			active, draft, err := manager.State()
			if err != nil || active.Revision != 0 || len(active.GroupOverrides) != 0 || draft.GroupOverrides[0].GroupID != "synthetic-group-b" {
				t.Fatalf("conflict changed active state or the replacement draft: active=%v draft=%v err=%v", active, draft, err)
			}
			withoutExpected, err := clientA.Publish(ctx, connect.NewRequest(&v1.PublishRequest{}))
			if err != nil || withoutExpected.Msg.Success || withoutExpected.Msg.Error == "" {
				t.Fatalf("legacy route publication was accepted: %v %v", withoutExpected, err)
			}
			publishedB, err := clientB.Publish(ctx, connect.NewRequest(&v1.PublishRequest{ExpectedConfiguration: configB}))
			if err != nil || !publishedB.Msg.Success || publishedB.Msg.Active.GroupOverrides[0].GroupId != "synthetic-group-b" {
				t.Fatalf("window B could not publish its own draft: %v %v", publishedB, err)
			}
			stale, err := clientB.Publish(ctx, connect.NewRequest(&v1.PublishRequest{ExpectedConfiguration: configB}))
			if err != nil || stale.Msg.Success {
				t.Fatalf("stale publication was accepted: %v %v", stale, err)
			}
		})
	}
}
