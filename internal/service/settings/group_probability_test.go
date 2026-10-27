package settings

import (
	"context"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
)

func TestGroupProbabilitySettingCatalogAndSingleUpdate(t *testing.T) {
	store, err := instanceconfig.Open(filepath.Join(t.TempDir(), ".env"), false)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewScoped(store, nil, nil)
	list, err := svc.ListEnvVars(context.Background(), connect.NewRequest(&v1.ListEnvVarsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, setting := range list.Msg.EnvVars {
		if setting.Key == proactive.EnvGroupProbabilities {
			found = true
			if setting.IsSecret {
				t.Fatal("probability overrides are not secret")
			}
		}
	}
	if !found || !ExportableValueKeys()[proactive.EnvGroupProbabilities] || instanceconfig.InstanceRestartKeys[proactive.EnvGroupProbabilities] {
		t.Fatal("group probabilities must be listed, backed up and hot-applied")
	}
	for _, value := range []string{`{"synthetic-group":0.25}`, `{"synthetic-group":"bad"}`, `{"synthetic-group":-1}`, `{"synthetic-group":2}`, `{"synthetic-group":0}`} {
		response, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{Key: proactive.EnvGroupProbabilities, Value: value}))
		if err != nil || !response.Msg.Success {
			t.Fatalf("save %q: %v %v", value, response, err)
		}
	}
	response, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{Key: proactive.EnvGroupProbabilities, Value: "broken"}))
	if err != nil || response.Msg.Success {
		t.Fatalf("accepted malformed container: %v %v", response, err)
	}
	if store.Get(proactive.EnvGroupProbabilities) != `{"synthetic-group":0}` {
		t.Fatal("failed update changed saved configuration")
	}
}
