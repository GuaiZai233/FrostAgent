package botstatus

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/balancequery"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestBalanceQueryConfigCredentialLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.env")
	config, err := instanceconfig.Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	scope := runtimescope.New(config, nil, nil)
	t.Cleanup(scope.Cancel)
	service := New(&llm.Engine{Scope: scope}, "test", "")
	ctx := context.Background()
	const firstKey = "synthetic-first-balance-key"
	const nextKey = "synthetic-next-balance-key"
	const customCurl = "curl http://127.0.0.1:12345/balance -H 'Authorization: Bearer {{API_KEY}}'"
	checkResponse := func(response *connect.Response[v1.BalanceQueryConfigResponse], err error, curl string, configured bool) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if response.Msg.Curl != curl || response.Msg.ApiKeyConfigured != configured {
			t.Fatalf("unexpected public configuration: %v", response.Msg)
		}
		raw, err := protojson.Marshal(response.Msg)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), firstKey) || strings.Contains(string(raw), nextKey) {
			t.Fatal("configuration response exposed a credential")
		}
	}
	get := func(curl string, configured bool) {
		t.Helper()
		response, err := service.GetBalanceQueryConfig(ctx, connect.NewRequest(&v1.GetBalanceQueryConfigRequest{}))
		checkResponse(response, err, curl, configured)
	}
	save := func(request *v1.SaveBalanceQueryConfigRequest, configured bool) {
		t.Helper()
		response, err := service.SaveBalanceQueryConfig(ctx, connect.NewRequest(request))
		checkResponse(response, err, request.Curl, configured)
	}
	checkStored := func(t *testing.T, key string) {
		t.Helper()
		reopened, err := instanceconfig.Open(path, false)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := balancequery.Load(reopened.Get)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Curl != customCurl || stored.APIKey != key {
			t.Fatal("persisted configuration does not match the accepted edit")
		}
	}
	get(balancequery.DefaultCurl, false)
	save(&v1.SaveBalanceQueryConfigRequest{Curl: customCurl, ApiKey: firstKey}, true)
	get(customCurl, true)
	checkStored(t, firstKey)
	save(&v1.SaveBalanceQueryConfigRequest{Curl: customCurl}, true)
	checkStored(t, firstKey)
	for _, tc := range []struct {
		name    string
		request *v1.SaveBalanceQueryConfigRequest
	}{
		{"provider and manual key", &v1.SaveBalanceQueryConfigRequest{Curl: customCurl, ApiKey: nextKey, ProviderId: "fixture-provider"}},
		{"literal credential", &v1.SaveBalanceQueryConfigRequest{Curl: "curl https://example.invalid/" + firstKey}},
		{"redirect option", &v1.SaveBalanceQueryConfigRequest{Curl: customCurl + " -L"}},
		{"invalid credential", &v1.SaveBalanceQueryConfigRequest{Curl: customCurl, ApiKey: nextKey + "\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := config.Get(balancequery.ConfigKey)
			response, err := service.SaveBalanceQueryConfig(ctx, connect.NewRequest(tc.request))
			if connect.CodeOf(err) != connect.CodeInvalidArgument || response != nil {
				t.Fatalf("invalid edit returned %v, %v", response, err)
			}
			if strings.Contains(err.Error(), firstKey) || strings.Contains(err.Error(), nextKey) {
				t.Fatal("validation error exposed a credential")
			}
			if config.Get(balancequery.ConfigKey) != before {
				t.Fatal("invalid edit changed the live configuration")
			}
			checkStored(t, firstKey)
		})
	}
	save(&v1.SaveBalanceQueryConfigRequest{Curl: customCurl, ApiKey: nextKey}, true)
	get(customCurl, true)
	checkStored(t, nextKey)
}

func TestBalanceQueryConfigUnavailableOrCorrupt(t *testing.T) {
	for _, name := range []string{"missing scope", "missing store", "corrupt config"} {
		t.Run(name, func(t *testing.T) {
			engine := &llm.Engine{}
			if name != "missing scope" {
				engine.Scope = runtimescope.New(nil, nil, nil)
				t.Cleanup(engine.Scope.Cancel)
			}
			if name == "corrupt config" {
				config, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
				if err != nil {
					t.Fatal(err)
				}
				if err := config.Update(balancequery.ConfigKey, "{invalid-synthetic-config", false); err != nil {
					t.Fatal(err)
				}
				engine.Config = config
			}
			service := New(engine, "test", "")
			get, err := service.GetBalanceQueryConfig(context.Background(), connect.NewRequest(&v1.GetBalanceQueryConfigRequest{}))
			if connect.CodeOf(err) != connect.CodeFailedPrecondition || get != nil {
				t.Fatalf("unavailable get returned %v, %v", get, err)
			}
			saved, err := service.SaveBalanceQueryConfig(context.Background(), connect.NewRequest(&v1.SaveBalanceQueryConfigRequest{Curl: balancequery.DefaultCurl}))
			if connect.CodeOf(err) != connect.CodeFailedPrecondition || saved != nil {
				t.Fatalf("unavailable save returned %v, %v", saved, err)
			}
			if engine.Scope != nil && engine.Config != nil && engine.Config.Get(balancequery.ConfigKey) != "{invalid-synthetic-config" {
				t.Fatal("unavailable save overwrote the corrupt configuration")
			}
		})
	}
}

func TestBalanceQueryConfigReferencesProviderWithoutCopyingKey(t *testing.T) {
	dir := t.TempDir()
	config, err := instanceconfig.Open(filepath.Join(dir, "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	scope := runtimescope.New(config, nil, nil)
	t.Cleanup(scope.Cancel)
	router := modelrouter.New(filepath.Join(dir, "router.json"), scope)
	catalog := router.Draft()
	catalog.Endpoints = []modelrouter.Endpoint{
		{ID: "fixture-provider", DisplayName: "Fixture provider", BaseURL: "https://example.invalid/v1", Enabled: true},
		{ID: "no-key", DisplayName: "No key", BaseURL: "https://example.invalid/v1", Enabled: true},
		{ID: "disabled", DisplayName: "Disabled", BaseURL: "https://example.invalid/v1"},
	}
	if err := router.SaveDraft(catalog); err != nil {
		t.Fatal(err)
	}
	const key = "synthetic-reused-provider-key"
	if _, err := router.SetDraftEndpointSecret("fixture-provider", key); err != nil {
		t.Fatal(err)
	}
	if _, err := router.Publish(); err != nil {
		t.Fatal(err)
	}
	service := New(&llm.Engine{Scope: scope, ModelRouter: router}, "test", "")
	ctx := context.Background()
	for _, id := range []string{"no-key", "disabled", "missing"} {
		response, err := service.SaveBalanceQueryConfig(ctx, connect.NewRequest(&v1.SaveBalanceQueryConfigRequest{Curl: balancequery.DefaultCurl, ProviderId: id}))
		if response != nil || connect.CodeOf(err) != connect.CodeInvalidArgument || config.Get(balancequery.ConfigKey) != "" {
			t.Fatalf("invalid provider selection changed config: %v", err)
		}
	}
	response, err := service.SaveBalanceQueryConfig(ctx, connect.NewRequest(&v1.SaveBalanceQueryConfigRequest{Curl: balancequery.DefaultCurl, ProviderId: "fixture-provider"}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Msg.ProviderId != "fixture-provider" || !response.Msg.ApiKeyConfigured || len(response.Msg.Providers) != 3 {
		t.Fatalf("provider metadata missing: %v", response.Msg)
	}
	for _, provider := range response.Msg.Providers {
		if provider.Available != (provider.Id == "fixture-provider") {
			t.Fatal("provider selection did not preserve availability")
		}
	}
	public, _ := protojson.Marshal(response.Msg)
	if strings.Contains(string(public), key) || strings.Contains(config.Get(balancequery.ConfigKey), key) {
		t.Fatal("provider credential copied into response or balance configuration")
	}
	stored, err := balancequery.Load(config.Get)
	if err != nil || stored.ProviderID != "fixture-provider" || stored.APIKey != "" {
		t.Fatal("provider reference was not persisted without its credential")
	}
	for _, request := range []*v1.SaveBalanceQueryConfigRequest{
		{Curl: balancequery.DefaultCurl},
		{Curl: "curl https://example.invalid/" + key, ProviderId: "fixture-provider"},
	} {
		before := config.Get(balancequery.ConfigKey)
		if _, err := service.SaveBalanceQueryConfig(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeInvalidArgument || strings.Contains(err.Error(), key) {
			t.Fatalf("invalid provider edit was accepted or exposed key: %v", err)
		}
		if config.Get(balancequery.ConfigKey) != before {
			t.Fatal("invalid edit changed the provider reference")
		}
	}
	if _, err := service.SaveBalanceQueryConfig(ctx, connect.NewRequest(&v1.SaveBalanceQueryConfigRequest{Curl: balancequery.DefaultCurl, ApiKey: "synthetic-manual-key"})); err != nil {
		t.Fatal(err)
	}
	stored, _ = balancequery.Load(config.Get)
	if stored.ProviderID != "" || stored.APIKey != "synthetic-manual-key" {
		t.Fatal("switching to manual credentials retained the provider reference")
	}
}
